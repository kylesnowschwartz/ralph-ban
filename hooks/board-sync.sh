#!/usr/bin/env bash
# UserPromptSubmit hook: reads board, diffs against snapshot, injects delta.
# Detects dispatch opportunities (unclaimed todo) and review queue depth.
# Output: hookSpecificOutput.additionalContext (injected into Claude's context).
# Exit 0 always — context injection only, never blocks prompts.
set -euo pipefail
trap 'echo "{\"hookSpecificOutput\":{\"additionalContext\":\"Hook error in $(basename "$0"): $BASH_COMMAND failed\"}}" 2>/dev/null; exit 0' ERR

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"

# Check if bl is available
BL="${BL:-bl}"
if ! command -v "$BL" &>/dev/null; then
  exit 0
fi

# Check if beads-lite is initialized
if [ ! -f ".beads-lite/beads.db" ]; then
  exit 0
fi

# Diff against last snapshot
changes=""
if diff_output=$(diff_board); then
  changes="$diff_output"
  save_snapshot
fi

# --- Dispatch and review nudges (orchestrator only) ---
# Workers/reviewers shouldn't get dispatch suggestions meant for the lead.
# Board state and circuit breaker context still flows to everyone.
dispatch_nudge=""
review_nudge=""
if [ -z "${CLAUDE_TEAM_NAME:-}" ]; then
  # bl ready --unclaimed respects dependencies and only returns unblocked, unclaimed items.
  unclaimed_todo=$("$BL" ready --unclaimed --json 2>/dev/null | jq -r '
    select(.status == "todo")
    | "\(.id): \(.title)"
  ' 2>/dev/null || true)

  if [ -n "$unclaimed_todo" ]; then
    count=$(echo "$unclaimed_todo" | wc -l | tr -d ' ')
    first=$(echo "$unclaimed_todo" | head -1)
    if [ "$count" -eq 1 ]; then
      dispatch_nudge="1 unclaimed todo card ready for work: ${first}. Consider delegating to a worker agent in an isolated worktree."
    else
      dispatch_nudge="${count} unclaimed todo cards. Highest priority: ${first}. Consider delegating to worker agents in isolated worktrees."
    fi
  fi

  # --- Review queue depth ---
  review_count=$(count_review)
  if [ "$review_count" -ge 3 ] 2>/dev/null; then
    state=$(read_board)
    review_cards=$(echo "$state" | jq -r '
      select(.status == "review")
      | "\(.id): \(.title)"
    ' 2>/dev/null || true)
    review_nudge="Review queue has ${review_count} items — consider dispatching reviewers:
${review_cards}
Each card needs a reviewer in an isolated worktree. Approved cards get merged and closed, rejected cards go back to doing with feedback."
  fi
fi

# --- Circuit breaker: detect review bounces ---
# When a card moves review→doing, that's a rejection. Track it.
# After 3 bounces, escalate to human instead of re-dispatching.
breaker_warning=""
if [ -n "$changes" ]; then
  # Parse changes for review→doing transitions.
  # describe_changes includes card IDs: "Card 'title' (bl-xxxx) moved from X to Y"
  # Extract the ID directly — no title→ID lookup needed.
  while IFS= read -r line; do
    if echo "$line" | grep -q "moved from review to doing" 2>/dev/null; then
      card_id=$(echo "$line" | grep -o 'bl-[a-z0-9]*')
      if [ -n "$card_id" ]; then
        bounce_count=$(record_bounce "$card_id")
        if [ "$bounce_count" -ge 3 ]; then
          breaker_warning="CIRCUIT BREAKER: Card ${card_id} has bounced between review and doing ${bounce_count} times. Stop auto-dispatching this card. Ask the user for direction — the task may need rethinking, splitting, or manual intervention."
        fi
      fi
    fi
    # Clear bounce counts for cards that reach done
    if echo "$line" | grep -q "moved from review to done" 2>/dev/null; then
      card_id=$(echo "$line" | grep -o 'bl-[a-z0-9]*')
      if [ -n "$card_id" ]; then
        clear_bounce "$card_id"
      fi
    fi
  done <<<"$changes"
fi

# Build the system message from available parts
parts=()
if [ -n "$breaker_warning" ]; then
  parts+=("$breaker_warning")
fi
if [ -n "$changes" ]; then
  parts+=("Board changes since last prompt:")
  parts+=("$changes")
fi
if [ -n "$dispatch_nudge" ]; then
  parts+=("$dispatch_nudge")
fi
if [ -n "$review_nudge" ]; then
  parts+=("$review_nudge")
fi

if [ ${#parts[@]} -gt 0 ]; then
  message=$(printf '%s\n' "${parts[@]}")
  jq -n --arg msg "$message" '{hookSpecificOutput: {additionalContext: $msg}}'
fi
