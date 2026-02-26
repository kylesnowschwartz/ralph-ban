#!/usr/bin/env bash
# SessionStart hook: reads board state, sets baseline snapshot, suggests next task.
# Output: hookSpecificOutput.additionalContext (injected into Claude's initial context).
# Exit 0 always — SessionStart cannot block.
set -euo pipefail
trap 'echo "{\"hookSpecificOutput\":{\"hookEventName\":\"SessionStart\",\"additionalContext\":\"Hook error in $(basename "$0"): $BASH_COMMAND failed\"}}" 2>/dev/null; exit 0' ERR

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"
require_bl

# Clean up stale agent worktrees from previous sessions that crashed or were interrupted.
# Runs silently before the board snapshot so accumulation doesn't require manual intervention.
# Only run from the main checkout — workers in linked worktrees must not clean up other workers'
# directories. In the main checkout, --show-toplevel and the resolved parent of --git-common-dir
# are the same path; in a linked worktree they differ.
_TOPLEVEL="$(git rev-parse --show-toplevel 2>/dev/null || true)"
_COMMON_PARENT="$(cd "$(git rev-parse --git-common-dir 2>/dev/null || echo '.')" 2>/dev/null && dirname "$(pwd)" || true)"
if [ "$_TOPLEVEL" = "$_COMMON_PARENT" ]; then
  "${_GIT_ROOT}/scripts/cleanup-worktrees.sh" >/dev/null 2>&1 || true
fi

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

# Get the first (highest priority) ready item
first=$(echo "$ready" | head -1)
title=$(echo "$first" | jq -r '.title // "unknown"')
id=$(echo "$first" | jq -r '.id // "unknown"')
status=$(echo "$first" | jq -r '.status // "unknown"')

# Count totals
total=$(echo "$ready" | wc -l | tr -d ' ')

preamble=$(framework_preamble)
board_summary="Board has ${total} ready items. Highest priority: '${title}' (${id}, ${status})."

# Append rate limit pause notice if active (main session only — teammates don't dispatch).
if [ -z "${CLAUDE_TEAM_NAME:-}" ]; then
  pause_info=$(check_rate_limit_pause 2>/dev/null || true)
  if [ -n "$pause_info" ]; then
    board_summary="${board_summary} ${pause_info}"
  fi
fi

if [ -z "${CLAUDE_TEAM_NAME:-}" ]; then
  # Main session: preamble (includes orchestrator role) + board summary
  emit_context "${preamble}
${board_summary}" "$board_summary"
else
  # Teammate: board context only (agent frontmatter handles role)
  emit_context "$board_summary" "$board_summary"
fi
