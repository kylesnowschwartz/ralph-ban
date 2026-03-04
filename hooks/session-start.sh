#!/usr/bin/env bash
# SessionStart hook: reads board state, sets baseline snapshot, orients the agent.
# Agent context gets a status-aware directive so minimal user input is needed.
# User-visible message gets a clean board summary.
# Exit 0 always — SessionStart cannot block.
set -euo pipefail
trap 'echo "{\"hookSpecificOutput\":{\"hookEventName\":\"SessionStart\",\"additionalContext\":\"Hook error in $(basename "$0"): $BASH_COMMAND failed\"}}" 2>/dev/null; exit 0' ERR

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"
require_bl

# Save initial snapshot for future diffs
save_snapshot

# Set agent_state=running for any doing card claimed by this agent.
# CLAUDE_AGENT_NAME is set by Claude Code when running as a named agent (e.g., a worker).
# Only set state on cards that are explicitly owned — don't touch unassigned doing cards.
if [ -n "${CLAUDE_AGENT_NAME:-}" ]; then
  "$BL" list --json 2>/dev/null | jq -r --arg agent "$CLAUDE_AGENT_NAME" \
    'select(.status == "doing" and .assigned_to == $agent) | .id' 2>/dev/null |
    while IFS= read -r card_id; do
      [ -n "$card_id" ] && "$BL" agent-state "$card_id" --state running >/dev/null 2>&1 || true
    done
fi

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
todo=$(echo "$ready" | jq -c 'select(.status == "todo")' 2>/dev/null)

todo_count=0
if [ -n "$todo" ]; then
  todo_count=$(echo "$todo" | wc -l | tr -d ' ')
fi

# Include stop mode so the orchestrator knows its behavior from the first message.
stop_mode=$(read_stop_mode)

# Build status-aware directive for agent context and clean summary for user.
# Doing > review > todo priority. The directive tells the agent what to do;
# the summary tells the user what the board looks like.
if [ -n "$doing" ]; then
  first=$(echo "$doing" | head -1)
  title=$(echo "$first" | jq -r '.title // "unknown"')
  id=$(echo "$first" | jq -r '.id // "unknown"')
  doing_count=$(echo "$doing" | wc -l | tr -d ' ')

  user_msg="Board has ${total} ready items. ${doing_count} in progress. Stop mode: ${stop_mode}."
  agent_ctx="Board: ${doing_count} doing, ${todo_count} todo. Resume in-progress work on '${title}' (${id}). Stop mode: ${stop_mode}."

elif [ -n "$review" ]; then
  first=$(echo "$review" | head -1)
  title=$(echo "$first" | jq -r '.title // "unknown"')
  id=$(echo "$first" | jq -r '.id // "unknown"')
  review_count=$(echo "$review" | wc -l | tr -d ' ')

  user_msg="Board has ${total} ready items. ${review_count} awaiting review. Stop mode: ${stop_mode}."
  agent_ctx="Board: ${review_count} review, ${todo_count} todo. Review '${title}' (${id}) first — unblock the review queue before starting new work. Stop mode: ${stop_mode}."

else
  first=$(echo "$ready" | head -1)
  title=$(echo "$first" | jq -r '.title // "unknown"')
  id=$(echo "$first" | jq -r '.id // "unknown"')

  user_msg="Board has ${todo_count} ready items. Highest priority: '${title}' (${id}, todo). Stop mode: ${stop_mode}."
  agent_ctx="Board has ${todo_count} unclaimed todo cards. Highest priority: ${id}: ${title}. Consider delegating to worker agents in isolated worktrees. Stop mode: ${stop_mode}."
fi

# Append rate limit pause notice if active.
pause_info=$(check_rate_limit_pause 2>/dev/null || true)
if [ -n "$pause_info" ]; then
  agent_ctx="${agent_ctx} ${pause_info}"
  user_msg="${user_msg} ${pause_info}"
fi

emit_context "$agent_ctx" "$user_msg"
