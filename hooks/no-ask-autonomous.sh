#!/usr/bin/env bash
# PreToolUse hook: deny AskUserQuestion in autonomous mode when board has active work.
#
# In autonomous mode the stop hook is the sole arbiter of completion — the
# orchestrator should dispatch workers, not solicit user direction. Denying
# AskUserQuestion here is the programmatic enforcement of that invariant:
# the agent physically cannot ask while unclaimed work remains on the board.
#
# Fails open on any error so a broken hook never silently blocks tool calls.

trap 'exit 0' ERR
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"

# Only relevant in autonomous mode.
stop_mode=$(read_stop_mode)
if [ "$stop_mode" != "autonomous" ]; then
  exit 0
fi

# Board clear? Nothing to dispatch — asking is fine.
read todo_count doing_count <<<"$(count_active)"
if [ "$todo_count" -eq 0 ] && [ "$doing_count" -eq 0 ]; then
  exit 0
fi

# Autonomous mode + active work: deny the question, name the next card.
next_card=$("$BL" ready --json 2>/dev/null \
  | jq -r 'select(.status == "todo") | "\(.id) — \(.title)"' \
  | head -1 || true)

if [ -n "$next_card" ]; then
  reason="Autonomous mode: dispatch workers instead of asking. Next card: ${next_card}"
else
  reason="Autonomous mode: dispatch workers instead of asking. ${todo_count} todo, ${doing_count} doing cards remain."
fi

jq -n --arg reason "$reason" '{
  hookSpecificOutput: {
    permissionDecision: "deny"
  },
  systemMessage: $reason
}'
