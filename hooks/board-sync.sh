#!/usr/bin/env bash
# UserPromptSubmit hook: reads board, diffs against snapshot, injects delta.
# Detects dispatch opportunities (unclaimed todo) and review queue depth.
# Output: hookSpecificOutput.additionalContext (injected into Claude's context).
# Exit 0 always — context injection only, never blocks prompts.
set -euo pipefail
trap 'echo "{\"hookSpecificOutput\":{\"hookEventName\":\"UserPromptSubmit\",\"additionalContext\":\"Hook error in $(basename "$0"): $BASH_COMMAND failed\"}}" 2>/dev/null; exit 0' ERR

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"
source "$SCRIPT_DIR/lib/heartbeat.sh"
require_bl

# --- Rate limit detection ---
# Scan the hook's input JSON for known rate limit signals before doing anything
# else. Claude Code passes the full context as stdin JSON; both the transcript
# and top-level text fields may contain the signal.
#
# Patterns matched (case-insensitive):
#   "rate_limit" or "rate limit" — Claude SDK error codes and prose
#   "429"                        — HTTP status code
#   "too many requests"          — HTTP/API status text
#   "overloaded"                 — Anthropic overload responses
# Workers that crash on a rate limit often set stop_reason="error" but the
# message content carries the clearest signal, so we scan the raw JSON.
input_json=""
if [ ! -t 0 ]; then
  input_json=$(cat 2>/dev/null || true)
fi

rate_limit_warning=""
if echo "$input_json" | grep -qi "rate_limit\|\"error\".*\"429\"\|\"status\":.*429\|too many requests\|overloaded_error" 2>/dev/null; then
  write_rate_limit_pause
  rate_limit_warning="RATE LIMIT DETECTED: Claude API rate limit signal found in hook context. Pause marker written — new dispatches are suppressed for up to 30 minutes. Existing workers will continue. Check active doing cards and wait for the limit to lift before spawning new agents."
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
pause_notice=""
if [ -z "${CLAUDE_TEAM_NAME:-}" ]; then
  # Check for active rate limit pause before suggesting new dispatches.
  # check_rate_limit_pause exits 0 when paused (outputs info string), 1 when clear.
  if pause_notice=$(check_rate_limit_pause 2>/dev/null); then
    # Paused — skip dispatch nudge, surface pause_notice in output instead.
    true
  else
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
  fi

  # --- Review queue depth ---
  review_count=$(count_review)
  if [ "$review_count" -ge "$REVIEW_QUEUE_THRESHOLD" ] 2>/dev/null; then
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
# Uses a CLOSED/OPEN/HALF_OPEN state machine so cards that bounced early in a
# long session can recover instead of staying permanently flagged.
#
#   CLOSED    — normal operation; counting bounces
#   OPEN      — tripped; escalate to human; cool-down timer running
#   HALF_OPEN — cool-down expired; probe state: one more attempt allowed
#               failure → OPEN (restart cool-down), success → CLOSED (reset)
breaker_warning=""
if [ -n "$changes" ]; then
  # Parse changes for review→doing transitions.
  # describe_changes includes card IDs: "Card 'title' (bl-xxxx) moved from X to Y"
  # Extract the ID directly — no title→ID lookup needed.
  while IFS= read -r line; do
    if echo "$line" | grep -q "moved from review to doing" 2>/dev/null; then
      card_id=$(echo "$line" | grep -o 'bl-[a-z0-9]*')
      if [ -n "$card_id" ]; then
        result=$(cb_record_bounce "$card_id")
        result_state=$(echo "$result" | cut -d' ' -f1)
        result_count=$(echo "$result" | cut -d' ' -f2)
        case "$result_state" in
          OPEN)
            breaker_warning="CIRCUIT BREAKER OPEN: Card ${card_id} has bounced ${result_count} times. Stop auto-dispatching. Ask the user for direction — the task may need rethinking, splitting, or manual intervention."
            ;;
          HALF_OPEN_REOPEN)
            breaker_warning="CIRCUIT BREAKER RE-OPENED: Card ${card_id} failed its probe attempt (${result_count} total bounces). Cool-down restarted. Ask the user for direction before retrying."
            ;;
          # CLOSED: still within normal threshold — no warning needed
        esac
      fi
    fi
    # A card reaching done resets the circuit breaker (success path)
    if echo "$line" | grep -q "moved from review to done\|moved from .* to done" 2>/dev/null; then
      card_id=$(echo "$line" | grep -o 'bl-[a-z0-9]*')
      if [ -n "$card_id" ]; then
        cb_record_success "$card_id"
      fi
    fi
  done <<<"$changes"
fi

# Also check for any cards currently in HALF_OPEN state (cool-down expired).
# Emit a nudge so the orchestrator knows a probe attempt is allowed.
half_open_nudge=""
if [ -f "${_GIT_ROOT}/.ralph-ban/.circuit-breaker.json" ]; then
  while IFS= read -r card_id; do
    [ -z "$card_id" ] && continue
    current_state=$(cb_get_state "$card_id")
    if [ "$current_state" = "HALF_OPEN" ]; then
      count=$(jq -r --arg id "$card_id" '.[$id].bounce_count // 0' \
        <"${_GIT_ROOT}/.ralph-ban/.circuit-breaker.json" 2>/dev/null || echo "?")
      half_open_nudge="${half_open_nudge}CIRCUIT BREAKER HALF-OPEN: Card ${card_id} had ${count} bounces but cool-down has expired. One probe attempt allowed — monitor closely. Success resets the breaker; failure reopens it.
"
    fi
  done < <(jq -r 'keys[]' <"${_GIT_ROOT}/.ralph-ban/.circuit-breaker.json" 2>/dev/null || true)
fi

# --- Stall detection: track doing card progress ---
record_card_progress
stall_warnings=""
stall_warnings=$(detect_stalled_cards)

# --- Heartbeat stall detection: check for unresponsive worker agents ---
# Workers write a timestamp on every UserPromptSubmit. A file older than
# HEARTBEAT_STALE_SECONDS (default 5 min) means the agent has gone silent
# while still holding a doing card — likely hung or crashed.
board_state_for_heartbeat=$(read_board)
heartbeat_warnings=""
heartbeat_warnings=$(detect_stalled_heartbeats "$board_state_for_heartbeat")

# Build the system message from available parts
parts=()
if [ -n "$rate_limit_warning" ]; then
  parts+=("$rate_limit_warning")
fi
if [ -n "$breaker_warning" ]; then
  parts+=("$breaker_warning")
fi
if [ -n "$half_open_nudge" ]; then
  parts+=("$half_open_nudge")
fi
if [ -n "$changes" ]; then
  parts+=("Board changes since last prompt:")
  parts+=("$changes")
fi
if [ -n "$pause_notice" ]; then
  parts+=("$pause_notice")
fi
if [ -n "$dispatch_nudge" ]; then
  parts+=("$dispatch_nudge")
fi
if [ -n "$review_nudge" ]; then
  parts+=("$review_nudge")
fi
if [ -n "$stall_warnings" ]; then
  parts+=("STALL DETECTED:")
  parts+=("$stall_warnings")
fi
if [ -n "$heartbeat_warnings" ]; then
  parts+=("WORKER HEARTBEAT STALL:")
  parts+=("$heartbeat_warnings")
fi

if [ ${#parts[@]} -gt 0 ]; then
  # User-visible summary: just the parts, no orchestration framing.
  user_message=$(printf '%s\n' "${parts[@]}")
  # Agent context: prepend lifecycle reminder.
  parts=("Orchestration checkpoint: board sync follows." "${parts[@]}")
  agent_message=$(printf '%s\n' "${parts[@]}")
  jq -n --arg ctx "$agent_message" --arg msg "$user_message" \
    '{hookSpecificOutput: {hookEventName: "UserPromptSubmit", additionalContext: $ctx}, systemMessage: $msg}'
fi

# Write this agent's heartbeat and clean up stale files for completed agents.
# Done after output so heartbeat I/O doesn't affect hook exit status.
write_heartbeat
cleanup_heartbeats "$board_state_for_heartbeat"
