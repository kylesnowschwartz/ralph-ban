#!/usr/bin/env bash
# SessionStart hook: reads board state, sets baseline snapshot, suggests next task.
# Output: hookSpecificOutput.additionalContext (injected into Claude's initial context).
# Exit 0 always — SessionStart cannot block.
set -euo pipefail
trap 'echo "{\"hookSpecificOutput\":{\"hookEventName\":\"SessionStart\",\"additionalContext\":\"Hook error in $(basename "$0"): $BASH_COMMAND failed\"}}" 2>/dev/null; exit 0' ERR

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

# Save initial snapshot for future diffs
save_snapshot

# Helper: output additionalContext JSON and exit.
emit_context() {
  jq -n --arg ctx "$1" '{hookSpecificOutput: {hookEventName: "SessionStart", additionalContext: $ctx}}'
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
emit_context "${preamble}
Board has ${total} ready items. Highest priority: '${title}' (${id}, ${status}). Run \`bl claim ${id} --agent claude\` to start working on it."
