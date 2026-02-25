#!/usr/bin/env bash
# Stop hook: prevents premature exit when todo/doing items remain.
# Teammates bypass this — only the primary session enforces board completion.
# Output: {decision: "block", reason: "..."} on stdout to block (exit 0).
# No output + exit 0 = allow stop. Never exit 2 — use JSON decision field.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"

# Teammates shouldn't be blocked by board items they don't own.
if [ -n "${CLAUDE_TEAM_NAME:-}" ]; then
  exit 0
fi

# Check if bl is available
BL="${BL:-bl}"
if ! command -v "$BL" &>/dev/null; then
  exit 0
fi

# Check if beads-lite is initialized
if ! db_exists; then
  exit 0
fi

# Block on uncommitted changes — prevents lost work.
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  jq -n '{
    decision: "block",
    reason: "Uncommitted changes — commit or stash as part of finishing your current task",
    systemMessage: "The stop hook blocks exit when uncommitted changes exist. This prevents lost work. Commit your changes, then try stopping again. If the changes are experimental, stash them."
  }'
  exit 0
fi

# Block on deep review queue — process before adding more work.
review_count=$(count_review)
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

# Surface which cards this agent still owns.
# CLAUDE_AGENT_NAME lets callers override; defaults to "claude" for the primary session.
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

# Count remaining active items (not necessarily owned by this agent).
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
