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

# Read WIP limits from config.json (if present).
_read_wip_limit() {
  local column="$1"
  local config_file="${_GIT_ROOT}/${RALPH_BAN_DIR}/config.json"
  if [ -f "$config_file" ]; then
    jq -r --arg col "$column" '.wip_limits[$col] // empty' "$config_file" 2>/dev/null || true
  fi
}

# Build WIP warning string if a column is at or over its limit.
# Outputs a non-empty string only when there is a violation to report.
_wip_warnings() {
  local doing_count="$1"
  local review_count="$2"
  local warnings=""

  local doing_limit review_limit
  doing_limit=$(_read_wip_limit "doing")
  review_limit=$(_read_wip_limit "review")

  if [ -n "$doing_limit" ] && [ "$doing_count" -ge "$doing_limit" ]; then
    warnings="${warnings}Doing at WIP limit (${doing_count}/${doing_limit}). "
  fi
  if [ -n "$review_limit" ] && [ "$review_count" -ge "$review_limit" ]; then
    warnings="${warnings}Review at WIP limit (${review_count}/${review_limit}). "
  fi
  echo "${warnings%% }"
}

# Build compact priority distribution string for a set of JSONL cards.
# Output: "3 todo (1 P0, 2 P2)" or just "3 todo" if no noteworthy priorities.
# Only surfaces P0 count explicitly — other priorities omitted to keep it brief.
_priority_summary() {
  local cards="$1"
  local label="$2"
  local count
  count=$(echo "$cards" | wc -l | tr -d ' ')
  local p0_count
  p0_count=$(echo "$cards" | jq -c 'select(.priority == 0)' 2>/dev/null | wc -l | tr -d ' ')
  if [ "$p0_count" -gt 0 ]; then
    echo "${count} ${label} (${p0_count} P0)"
  else
    echo "${count} ${label}"
  fi
}

# Get all board cards to compute full distribution (including backlog).
all_cards=$("$BL" list --json 2>/dev/null) || {
  emit_context "Hook error: bl list failed. Check that beads-lite is working."
  exit 0
}

# Get ready work — the active pipeline (todo/doing/review).
ready=$("$BL" ready --json 2>/dev/null) || {
  emit_context "Hook error: bl ready failed. Check that beads-lite is working."
  exit 0
}
if [ -z "$ready" ]; then
  emit_context "Board is empty. No tasks to work on."
  exit 0
fi

# Categorize by status. Doing/review items represent work already in flight —
# finishing them is always higher priority than starting new todo items.
doing=$(echo "$ready" | jq -c 'select(.status == "doing")' 2>/dev/null)
review=$(echo "$ready" | jq -c 'select(.status == "review")' 2>/dev/null)
todo=$(echo "$ready" | jq -c 'select(.status == "todo")' 2>/dev/null)

doing_count=0
review_count=0
todo_count=0
[ -n "$doing" ] && doing_count=$(echo "$doing" | wc -l | tr -d ' ')
[ -n "$review" ] && review_count=$(echo "$review" | wc -l | tr -d ' ')
[ -n "$todo" ] && todo_count=$(echo "$todo" | wc -l | tr -d ' ')

# Count backlog separately (not included in ready).
backlog_count=0
if [ -n "$all_cards" ]; then
  backlog_count=$(echo "$all_cards" | jq -c 'select(.status == "backlog")' 2>/dev/null | wc -l | tr -d ' ')
fi

# Count P0 cards across all active columns (todo + doing + review).
p0_total=0
if [ -n "$ready" ]; then
  p0_total=$(echo "$ready" | jq -c 'select(.priority == 0)' 2>/dev/null | wc -l | tr -d ' ')
fi

# WIP limit warnings for agent context.
wip_warn=$(_wip_warnings "$doing_count" "$review_count")

# Include stop mode so the orchestrator knows its behavior from the first message.
stop_mode=$(read_stop_mode)

# Build compact distribution for agent context.
# Format: "2 doing, 1 review, 3 todo (1 P0), 5 backlog"
dist_parts=()
[ "$doing_count" -gt 0 ] && dist_parts+=("$(_priority_summary "$doing" "doing")")
[ "$review_count" -gt 0 ] && dist_parts+=("$(_priority_summary "$review" "review")")
[ "$todo_count" -gt 0 ] && dist_parts+=("$(_priority_summary "$todo" "todo")")
[ "$backlog_count" -gt 0 ] && dist_parts+=("${backlog_count} backlog")

distribution=$(
  IFS=', '
  echo "${dist_parts[*]}"
)

# Build status-aware directive for agent context and clean summary for user.
# Doing > review > todo priority. The directive tells the agent what to do;
# the summary tells the user what the board looks like.
if [ -n "$doing" ]; then
  first=$(echo "$doing" | head -1)
  title=$(echo "$first" | jq -r '.title // "unknown"')
  id=$(echo "$first" | jq -r '.id // "unknown"')

  agent_ctx="Board: ${distribution}. Stop mode: ${stop_mode}. Resume in-progress work on '${title}' (${id})."
  [ -n "$wip_warn" ] && agent_ctx="${agent_ctx} ${wip_warn}"

elif [ -n "$review" ]; then
  first=$(echo "$review" | head -1)
  title=$(echo "$first" | jq -r '.title // "unknown"')
  id=$(echo "$first" | jq -r '.id // "unknown"')

  agent_ctx="Board: ${distribution}. Stop mode: ${stop_mode}. Review '${title}' (${id}) first — unblock the review queue before starting new work."
  [ -n "$wip_warn" ] && agent_ctx="${agent_ctx} ${wip_warn}"

else
  first=$(echo "$ready" | head -1)
  title=$(echo "$first" | jq -r '.title // "unknown"')
  id=$(echo "$first" | jq -r '.id // "unknown"')

  agent_ctx="Board: ${distribution}. Stop mode: ${stop_mode}. Highest priority: ${id}: ${title}. Consider delegating to worker agents in isolated worktrees."
  [ -n "$wip_warn" ] && agent_ctx="${agent_ctx} ${wip_warn}"
fi

# bl-tcmn: Add bl onboard hint for standalone sessions (not orchestrator/worker agents).
# Orchestrators and workers already have full bl command reference in their templates.
if [ -z "${CLAUDE_AGENT_NAME:-}" ]; then
  agent_ctx="${agent_ctx} Run \`bl onboard\` for full command reference."
fi

# bl-beot: Build user-visible summary — compact board state at a glance.
# Shows total counts by status and flags any P0 urgency. 1-2 lines max.
# Avoids dispatch nudges (agent-only) and doesn't repeat what `bl ready` shows.
if [ "$p0_total" -gt 0 ]; then
  p0_signal=" [${p0_total} P0]"
else
  p0_signal=""
fi

# Compact status line: "3 doing, 1 review, 5 todo, 12 backlog [2 P0] | stop: batch"
status_parts=()
[ "$doing_count" -gt 0 ] && status_parts+=("${doing_count} doing")
[ "$review_count" -gt 0 ] && status_parts+=("${review_count} review")
[ "$todo_count" -gt 0 ] && status_parts+=("${todo_count} todo")
[ "$backlog_count" -gt 0 ] && status_parts+=("${backlog_count} backlog")
status_line=$(
  IFS=', '
  echo "${status_parts[*]}"
)

user_msg="Board: ${status_line}${p0_signal} | stop: ${stop_mode}"
[ -n "$wip_warn" ] && user_msg="${user_msg} | ${wip_warn}"

emit_context "$agent_ctx" "$user_msg"
