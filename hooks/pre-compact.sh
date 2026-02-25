#!/usr/bin/env bash
# PreCompact hook: re-injects board state before context compression.
# Without this, the agent loses board awareness when the conversation
# compresses — SessionStart context and board-sync deltas get summarized away.
# Output: systemMessage (universal field — PreCompact has no hook-specific fields).
# Exit 0 always — PreCompact cannot block.
set -euo pipefail
trap 'echo "{\"systemMessage\":\"Hook error in $(basename "$0"): $BASH_COMMAND failed\"}" 2>/dev/null; exit 0' ERR

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

# Build a compact board summary for the compressed context.
state=$(read_board)
if [ -z "$state" ]; then
  jq -n '{systemMessage: "Board is empty."}'
  exit 0
fi

# Count by status — gives the agent a snapshot without listing every card.
summary=$(echo "$state" | jq -s '
  group_by(.status) |
  map({status: .[0].status, count: length, titles: [.[].title]}) |
  sort_by(.status) |
  map(.status + " (" + (.count | tostring) + "): " + (.titles | join(", "))) |
  join("\n")
' -r 2>/dev/null || echo "Could not parse board state")

# Also surface any cards claimed by this agent.
AGENT_NAME="${CLAUDE_AGENT_NAME:-claude}"
claimed=$("$BL" list --assigned-to "$AGENT_NAME" --json 2>/dev/null | jq -r 'select(.status != "done") | "\(.id): \(.title) (\(.status))"' 2>/dev/null || true)

parts=("Board state at compaction:")
parts+=("$summary")
if [ -n "$claimed" ]; then
  parts+=("Your claimed cards:")
  parts+=("$claimed")
fi

message=$(printf '%s\n' "${parts[@]}")
jq -n --arg msg "$message" '{systemMessage: $msg}'
