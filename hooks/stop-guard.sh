#!/usr/bin/env bash
# Stop hook: prevents premature exit when todo/doing items remain.
# Promise tokens allow structured agent exit. Disable marker provides escape hatch.
# Output: {decision: "block", reason: "..."} on stdout to block (exit 0).
# No output + exit 0 = allow stop. Never exit 2 — use JSON decision field.

# --- Disable marker: escape hatch before anything else ---
# Derive git root without sourcing board-state.sh (minimize failure surface).
_STOP_ROOT="${BL_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
if [ -f "${_STOP_ROOT}/.ralph-ban/disabled" ]; then
  exit 0
fi

# --- Fail-open: any error silently allows exit ---
trap 'exit 0' ERR
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"

# --- Read stdin (JSON from Claude Code) ---
input=""
if [ ! -t 0 ]; then
  input=$(cat 2>/dev/null || true)
fi
last_message=$(echo "$input" | jq -r '.last_assistant_message // ""' 2>/dev/null || true)
stop_hook_active=$(echo "$input" | jq -r '.stop_hook_active // false' 2>/dev/null || true)

# --- Promise tokens: structured completion signal ---
if [ -n "$last_message" ]; then
  tokens=$(extract_tokens "$last_message")
  if [ -n "$tokens" ]; then
    # Agent signaled completion — allow exit
    exit 0
  fi
fi

# --- Anti-loop guard ---
# When stop_hook_active is true, the agent is already trying to stop.
# Only block on things the agent can actually resolve (uncommitted changes).
# Skip review queue / remaining work blocks — agent can't fix those alone.
if [ "$stop_hook_active" = "true" ]; then
  if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
    jq -n '{
      decision: "block",
      reason: "Uncommitted changes — commit or stash before stopping",
      systemMessage: "You have uncommitted changes. Commit or stash them, then try stopping again."
    }'
  fi
  exit 0
fi

# --- Team bypass: teammates exit freely ---
if [ -n "${CLAUDE_TEAM_NAME:-}" ]; then
  exit 0
fi

# --- Check if bl is available ---
BL="${BL:-bl}"
if ! command -v "$BL" &>/dev/null; then
  exit 0
fi

# --- Check if beads-lite is initialized ---
if ! db_exists; then
  exit 0
fi

# --- Block on uncommitted changes ---
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  jq -n '{
    decision: "block",
    reason: "Uncommitted changes — commit or stash as part of finishing your current task",
    systemMessage: "The stop hook blocks exit when uncommitted changes exist. This prevents lost work. Commit your changes, then try stopping again. If the changes are experimental, stash them."
  }'
  exit 0
fi

# --- Block on deep review queue ---
review_count=$(count_review || echo "0")
if [ "$review_count" -ge 3 ] 2>/dev/null; then
  state=$(read_board)
  review_cards=$(echo "$state" | jq -r '
    select(.status == "review")
    | "\(.id): \(.title)"
  ' 2>/dev/null || true)
  jq -n --arg count "$review_count" --arg cards "$review_cards" '{
    decision: "block",
    reason: ("Review queue has " + $count + " cards — review before adding more work"),
    systemMessage: ("Review queue has " + $count + " cards:\n" + $cards + "\nLaunch a review team: create reviewer agents in isolated worktrees, one per card. Approved cards get merged and closed. Rejected cards go back to doing with specific feedback. Process the queue before continuing other work.")
  }'
  exit 0
fi

# --- Surface owned cards ---
AGENT_NAME="${CLAUDE_AGENT_NAME:-claude}"
claimed=$("$BL" list --assigned-to "$AGENT_NAME" --json 2>/dev/null | jq -r 'select(.status == "doing" or .status == "todo") | "\(.id): \(.title) (\(.status))"' 2>/dev/null || true)
if [ -n "$claimed" ]; then
  jq -n --arg claimed "$claimed" '{
    decision: "block",
    reason: "You still own active cards",
    systemMessage: ("You have claimed cards that are not done:\n" + $claimed + "\nComplete them, move to review, or unclaim them. The orchestration framework blocks stopping while you own active work.")
  }'
  exit 0
fi

# --- Count remaining active items ---
read todo_count doing_count <<<"$(count_active)"

if [ "$todo_count" -gt 0 ] || [ "$doing_count" -gt 0 ]; then
  jq -n --arg todo "$todo_count" --arg doing "$doing_count" '{
    decision: "block",
    reason: "Board has active work remaining",
    systemMessage: ("Board has " + $todo + " todo and " + $doing + " doing items. Pick up the next task, or ask the user if they want you to stop despite remaining work.")
  }'
else
  exit 0
fi
