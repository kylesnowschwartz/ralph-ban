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
if [ ! -f ".beads-lite/beads.db" ]; then
  exit 0
fi

# Block on uncommitted changes — prevents lost work.
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  jq -n '{
    decision: "block",
    reason: "Uncommitted changes exist",
    systemMessage: "You have uncommitted changes. Commit or stash before stopping."
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
    reason: ("Claimed cards still active: " + $claimed),
    systemMessage: ("You still own these cards:\n" + $claimed + "\nMove them to review/done or unclaim them before stopping.")
  }'
  exit 0
fi

# Count remaining active items (not necessarily owned by this agent).
read todo_count doing_count <<<"$(count_active)"

if [ "$todo_count" -gt 0 ] || [ "$doing_count" -gt 0 ]; then
  jq -n --arg todo "$todo_count" --arg doing "$doing_count" '{
    decision: "block",
    reason: ($todo + " todo, " + $doing + " doing items remain"),
    systemMessage: ("Board has " + $todo + " items in Todo and " + $doing + " in Doing. Pick up the next task or ask the user if you should stop.")
  }'
else
  exit 0
fi
