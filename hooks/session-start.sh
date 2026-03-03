#!/usr/bin/env bash
# SessionStart hook: reads board state, sets baseline snapshot, suggests next task.
# Output: hookSpecificOutput.additionalContext (injected into Claude's initial context).
# Exit 0 always — SessionStart cannot block.
set -euo pipefail
trap 'echo "{\"hookSpecificOutput\":{\"hookEventName\":\"SessionStart\",\"additionalContext\":\"Hook error in $(basename "$0"): $BASH_COMMAND failed\"}}" 2>/dev/null; exit 0' ERR

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"
require_bl

# Save initial snapshot for future diffs
save_snapshot

# Helper: output additionalContext (agent context) + systemMessage (user-visible) and exit.
emit_context() {
  local agent_ctx="$1"
  local user_msg="${2:-$1}"
  jq -n --arg ctx "$agent_ctx" --arg msg "$user_msg" \
    '{hookSpecificOutput: {hookEventName: "SessionStart", additionalContext: $ctx}, systemMessage: $msg}'
}

# Get ready work and suggest highest-priority item
ready=$("$BL" ready --json 2>/dev/null) || {
  emit_context "Hook error: bl ready failed. Check that beads-lite is working."
  exit 0
}
if [ -z "$ready" ]; then
  emit_context "Board is empty. No tasks to work on."
  exit 0
fi

# Count totals
total=$(echo "$ready" | wc -l | tr -d ' ')

# Categorize by status. Doing/review items represent work already in flight —
# finishing them is always higher priority than starting new todo items.
doing=$(echo "$ready" | jq -c 'select(.status == "doing")' 2>/dev/null)
review=$(echo "$ready" | jq -c 'select(.status == "review")' 2>/dev/null)

# Pick the most urgent item: doing > review > first ready.
if [ -n "$doing" ]; then
  first=$(echo "$doing" | head -1)
  action="Resume in-progress"
elif [ -n "$review" ]; then
  first=$(echo "$review" | head -1)
  action="Review waiting"
else
  first=$(echo "$ready" | head -1)
  action="Next up"
fi

title=$(echo "$first" | jq -r '.title // "unknown"')
id=$(echo "$first" | jq -r '.id // "unknown"')
status=$(echo "$first" | jq -r '.status // "unknown"')

# Include stop mode so the orchestrator knows its behavior from the first message.
stop_mode=$(read_stop_mode)

board_summary="Board has ${total} ready items. ${action}: '${title}' (${id}, ${status}). Stop mode: ${stop_mode}."

# Append rate limit pause notice if active.
pause_info=$(check_rate_limit_pause 2>/dev/null || true)
if [ -n "$pause_info" ]; then
  board_summary="${board_summary} ${pause_info}"
fi

emit_context "$board_summary"
