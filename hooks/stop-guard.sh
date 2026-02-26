#!/usr/bin/env bash
# Stop hook: prevents premature exit when work remains.
# Blocks on uncommitted changes and active board work (doing/todo cards).
# Output: {decision: "block", reason: "..."} on stdout to block (exit 0).
# No output + exit 0 = allow stop. Never exit 2 — use JSON decision field.

# --- Disable marker: escape hatch before anything else ---
# Derive git root without sourcing board-state.sh (minimize failure surface).
_STOP_ROOT="${BL_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
if [ -f "${_STOP_ROOT}/.ralph-ban/disabled" ]; then
  exit 0
fi

STOP_MSG_HASH_FILE="${_STOP_ROOT}/.ralph-ban/.stop-last-msg-hash"

# --- Fail-open: any error silently allows exit ---
trap 'exit 0' ERR
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"

# --- Debounce: suppress repeated identical board-state block messages ---
# Early-exit blocks (uncommitted changes, anti-loop) bypass this —
# they are always relevant. Only board-state blocks at the bottom are debounced.
#
# Takes JSON on stdin. If systemMessage content matches the last emission AND
# that emission was within the last 60 seconds, strips systemMessage from the
# output (decision+reason still flow through). After 60 seconds the window
# expires and the orchestrator gets the full guidance again — suppressing
# indefinitely would silently drop actionable guidance on repeated stop attempts.
#
# State file stores "hash:timestamp" on a single line.
debounce_stop_message() {
  local json
  json=$(cat)
  local msg_hash
  msg_hash=$(echo "$json" | jq -r '.systemMessage // ""' | shasum -a 256 | cut -d' ' -f1)

  local stored last_hash last_ts now elapsed
  stored=$(cat "$STOP_MSG_HASH_FILE" 2>/dev/null || echo "")
  last_hash=$(echo "$stored" | cut -d: -f1)
  last_ts=$(echo "$stored" | cut -d: -f2)
  now=$(date +%s)
  elapsed=$(( now - ${last_ts:-0} ))

  if [ "$msg_hash" = "$last_hash" ] && [ "$elapsed" -lt 60 ]; then
    # Same message within the debounce window — strip systemMessage to reduce noise
    echo "$json" | jq 'del(.systemMessage)'
  else
    # New message or window expired — emit full JSON and record hash + timestamp
    echo "${msg_hash}:${now}" > "$STOP_MSG_HASH_FILE"
    echo "$json"
  fi
}

# --- Read stdin (JSON from Claude Code) ---
input=""
if [ ! -t 0 ]; then
  input=$(cat 2>/dev/null || true)
fi
stop_hook_active=$(echo "$input" | jq -r '.stop_hook_active // false' 2>/dev/null || true)

# --- Anti-loop guard ---
# When stop_hook_active is true, the agent is already trying to stop.
# Only block on things the agent can actually resolve (uncommitted changes).
# Skip review queue / remaining work blocks — agent can't fix those alone.
if [ "$stop_hook_active" = "true" ]; then
  if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
    jq -n '{
      decision: "block",
      reason: "Uncommitted changes — commit or stash before stopping",
      systemMessage: "You have uncommitted changes. Commit or stash them, then try stopping again."
    }'
  fi
  exit 0
fi

# --- Block on uncommitted changes ---
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  jq -n '{
    decision: "block",
    reason: "Uncommitted changes — commit or stash as part of finishing your current task",
    systemMessage: "The stop hook blocks exit when uncommitted changes exist. This prevents lost work. Commit your changes, then try stopping again. If the changes are experimental, stash them."
  }'
  exit 0
fi

require_bl

# --- Stall detection with cycle limit ---
MAX_STALLS=5
CYCLE_FILE="${_STOP_ROOT}/.ralph-ban/.stop-cycles"
HASH_FILE="${_STOP_ROOT}/.ralph-ban/.stop-board-hash"
mkdir -p "${_STOP_ROOT}/.ralph-ban"

current_hash=$(read_board_hash)
last_hash=$(cat "$HASH_FILE" 2>/dev/null || echo "")

if [ "$current_hash" = "$last_hash" ]; then
  # No board progress since last stop attempt — increment stall counter
  stall_count=$(cat "$CYCLE_FILE" 2>/dev/null || echo "0")
  stall_count=$((stall_count + 1))
  echo "$stall_count" > "$CYCLE_FILE"

  if [ "$stall_count" -ge "$MAX_STALLS" ]; then
    # Hard limit reached — allow exit rather than trapping the orchestrator forever
    echo "0" > "$CYCLE_FILE"
    rm -f "$HASH_FILE"
    jq -n --arg stalls "$MAX_STALLS" '{
      systemMessage: ("Reached " + $stalls + " stop cycles with no board progress. Ask the user if they want to continue or stop.")
    }'
    exit 0
  fi
else
  # Board changed — progress was made, reset stall counter
  echo "0" > "$CYCLE_FILE"
fi
echo "$current_hash" > "$HASH_FILE"

# --- Specific next-action guidance ---
read todo_count doing_count <<<"$(count_active)"
stop_mode=$(read_stop_mode)

# In batch mode: only block on doing cards. Todo backlog doesn't prevent stopping —
# the user dispatches a batch, the orchestrator finishes it, then exits cleanly.
# In autonomous mode: block on todo + doing until the whole board is empty.
if [ "$stop_mode" = "autonomous" ]; then
  if [ "$todo_count" -gt 0 ] || [ "$doing_count" -gt 0 ]; then
    should_block="yes"
  else
    should_block="no"
  fi
else
  # batch (default)
  if [ "$doing_count" -gt 0 ]; then
    should_block="yes"
  else
    should_block="no"
  fi
fi

if [ "$should_block" = "yes" ]; then
  # Try to identify the concrete next action from bl ready output
  next_action=""

  # Check for unclaimed doing cards
  unclaimed_doing=$("$BL" list --json 2>/dev/null | jq -r 'select(.status == "doing" and (.assigned_to == null or .assigned_to == "")) | "\(.id): \(.title)"' 2>/dev/null | head -1 || true)
  if [ -n "$unclaimed_doing" ]; then
    next_action="$unclaimed_doing is in doing with no assignee. Claim it or spawn a worker."
  elif [ "$stop_mode" = "autonomous" ]; then
    # In autonomous mode, suggest next todo card when no unclaimed doing work
    next_card=$("$BL" ready --json 2>/dev/null | jq -r 'select(.status == "todo") | "\(.id) — \(.title)"' 2>/dev/null | head -1 || true)
    if [ -n "$next_card" ]; then
      next_action="Dispatch now: $next_card"
    fi
  fi

  if [ -n "$next_action" ]; then
    if [ "$stop_mode" = "autonomous" ]; then
      jq -n --arg action "$next_action" --arg todo "$todo_count" --arg doing "$doing_count" '{
        decision: "block",
        reason: "Board has active work remaining",
        systemMessage: ("Stop mode: autonomous. " + $todo + " todo and " + $doing + " doing cards remain. " + $action + " Autonomous mode: merge reviewed cards without asking — reviewer approval is sufficient.")
      }' | debounce_stop_message
    else
      jq -n --arg action "$next_action" --arg doing "$doing_count" '{
        decision: "block",
        reason: ("Stop mode: batch. " + $doing + " cards in doing — finish or unclaim them before stopping."),
        systemMessage: ("Stop mode: batch. " + $doing + " cards in doing — finish or unclaim them before stopping. " + $action)
      }' | debounce_stop_message
    fi
  else
    if [ "$stop_mode" = "autonomous" ]; then
      jq -n --arg todo "$todo_count" --arg doing "$doing_count" '{
        decision: "block",
        reason: "Board has active work remaining",
        systemMessage: ("Stop mode: autonomous. " + $todo + " todo and " + $doing + " doing cards remain. Dispatch the next card now. The stop hook keeps you running until the board is drained. Autonomous mode: merge reviewed cards without asking — reviewer approval is sufficient.")
      }' | debounce_stop_message
    else
      jq -n --arg doing "$doing_count" '{
        decision: "block",
        reason: ("Stop mode: batch. " + $doing + " cards in doing — finish or unclaim them before stopping."),
        systemMessage: ("Stop mode: batch. " + $doing + " cards in doing — finish or unclaim them before stopping.")
      }' | debounce_stop_message
    fi
  fi
else
  # Board is clear (or batch mode with no doing cards) — allow exit.
  # Reset the debounce hash so the next block always shows its first message.
  rm -f "$STOP_MSG_HASH_FILE"
  if [ "$stop_mode" = "batch" ] && [ "$todo_count" -gt 0 ]; then
    jq -n --arg todo "$todo_count" '{
      systemMessage: ("Stop mode: batch. No cards in doing — free to stop. " + $todo + " todo cards remain for next session.")
    }'
  fi
  exit 0
fi
