#!/usr/bin/env bash
# TeammateIdle: prevent idle when teammate still owns active cards.
# Exit 2 + stderr = keep working. Exit 0 = allow idle.
trap 'exit 0' ERR
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"

if ! db_exists; then exit 0; fi

# Read teammate name from stdin JSON.
input=""
if [ ! -t 0 ]; then
  input=$(cat 2>/dev/null || true)
fi
teammate=$(echo "$input" | jq -r '.teammate_name // ""' 2>/dev/null || true)
[ -z "$teammate" ] && exit 0

# Cards in doing or todo mean the teammate still has work to do.
# Review cards don't block — a separate reviewer agent handles those.
claimed=$("$BL" list --assigned-to "$teammate" --json 2>/dev/null \
  | jq -r 'select(.status == "doing" or .status == "todo") | .id' 2>/dev/null || true)

if [ -n "$claimed" ]; then
  echo "You still own active cards. Complete them or move to review before going idle." >&2
  exit 2
fi
exit 0
