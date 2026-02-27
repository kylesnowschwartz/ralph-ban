#!/usr/bin/env bash
# Stop hook: prevents premature exit when work remains.
# Blocks on uncommitted changes and active board work (doing/todo cards).
# Output: {decision: "block", reason: "..."} on stdout to block (exit 0).
# No output + exit 0 = allow stop. Never exit 2 — use JSON decision field.
#
# Control flow phases:
#   1. Escape hatch (disable marker)
#   2. Setup + read stdin
#   3. Uncommitted changes gate (always blocks, regardless of mode)
#   4. Tool availability
#   5. Anti-loop guard (mode-aware: batch exits, autonomous falls through)
#   6. Stall detection (safety valve: allows exit after MAX_STALLS)
#   7. Block decision + guidance message

# --- Phase 1: Escape hatch ---
# Derive git root without sourcing board-state.sh (minimize failure surface).
_STOP_ROOT="${BL_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"
if [ -f "${_STOP_ROOT}/${RALPH_BAN_DIR:-.ralph-ban}/disabled" ]; then
  exit 0
fi

STOP_MSG_HASH_FILE="${_STOP_ROOT}/${RALPH_BAN_DIR:-.ralph-ban}/.stop-last-msg-hash"

# --- Phase 2: Setup ---
# Fail-open: any error silently allows exit.
trap 'exit 0' ERR
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"

# Debounce: suppress repeated identical board-state block messages.
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
  elapsed=$((now - ${last_ts:-0}))

  if [ "$msg_hash" = "$last_hash" ] && [ "$elapsed" -lt 60 ]; then
    # Same message within the debounce window — strip systemMessage to reduce noise
    echo "$json" | jq 'del(.systemMessage)'
  else
    # New message or window expired — emit full JSON and record hash + timestamp
    echo "${msg_hash}:${now}" >"$STOP_MSG_HASH_FILE"
    echo "$json"
  fi
}

# Read stdin (JSON from Claude Code).
input=""
if [ ! -t 0 ]; then
  input=$(cat 2>/dev/null || true)
fi
stop_hook_active=$(echo "$input" | jq -r '.stop_hook_active // false' 2>/dev/null || true)

# Read stop mode early — the anti-loop guard needs it.
# read_stop_mode reads a config file (no bl dependency), safe before require_bl.
stop_mode=$(read_stop_mode)

# --- Phase 3: Uncommitted changes gate ---
# Always blocks, regardless of stop_hook_active or stop_mode.
# Checked once here so neither the anti-loop nor the board logic duplicates it.
if [ -n "$(git status --porcelain 2>/dev/null)" ]; then
  jq -n '{
    decision: "block",
    reason: "Uncommitted changes — commit or stash before stopping",
    systemMessage: "The stop hook blocks exit when uncommitted changes exist. This prevents lost work. Commit your changes, then try stopping again. If the changes are experimental, stash them."
  }'
  exit 0
fi

# --- Phase 4: Tool availability ---
require_bl

# --- Phase 5: Anti-loop guard (mode-aware) ---
# stop_hook_active means the hook already blocked once in this stop cycle
# and the agent is re-attempting to stop.
#
# Batch:      let the agent go — it got one directive, that's enough.
# Autonomous: fall through to stall detection — the board must drain.
#             The stall counter (Phase 6) is the safety valve: after MAX_STALLS
#             stop attempts with no board progress, it allows exit.
if [ "$stop_hook_active" = "true" ] && [ "$stop_mode" != "autonomous" ]; then
  exit 0
fi

# --- Phase 6: Stall detection ---
# Counts how many consecutive stop attempts see the same board hash.
# After MAX_STALLS with no progress, allows exit to prevent permanent trapping.
MAX_STALLS=5
CYCLE_FILE="${_STOP_ROOT}/${RALPH_BAN_DIR}/.stop-cycles"
HASH_FILE="${_STOP_ROOT}/${RALPH_BAN_DIR}/.stop-board-hash"
mkdir -p "${_STOP_ROOT}/${RALPH_BAN_DIR}"

current_hash=$(read_board_hash)
last_hash=$(cat "$HASH_FILE" 2>/dev/null || echo "")

if [ "$current_hash" = "$last_hash" ]; then
  # No board progress since last stop attempt — increment stall counter
  stall_count=$(cat "$CYCLE_FILE" 2>/dev/null || echo "0")
  stall_count=$((stall_count + 1))
  echo "$stall_count" >"$CYCLE_FILE"

  if [ "$stall_count" -ge "$MAX_STALLS" ]; then
    # Hard limit reached — allow exit rather than trapping the orchestrator forever
    echo "0" >"$CYCLE_FILE"
    rm -f "$HASH_FILE"
    jq -n --arg stalls "$MAX_STALLS" '{
      systemMessage: ("Reached " + $stalls + " stop cycles with no board progress. Ask the user if they want to continue or stop.")
    }'
    exit 0
  fi
else
  # Board changed — progress was made, reset stall counter
  echo "0" >"$CYCLE_FILE"
fi
echo "$current_hash" >"$HASH_FILE"

# --- Phase 7: Block decision + guidance ---
read todo_count doing_count <<<"$(count_active)"

# Batch:      block only on doing cards (todo backlog is the user's concern).
# Autonomous: block on todo + doing until the board drains.
if [ "$stop_mode" = "autonomous" ]; then
  [ "$todo_count" -gt 0 ] || [ "$doing_count" -gt 0 ] && should_block="yes" || should_block="no"
else
  [ "$doing_count" -gt 0 ] && should_block="yes" || should_block="no"
fi

if [ "$should_block" = "yes" ]; then
  # Identify the concrete next action for the agent
  next_action=""

  # Check for unclaimed doing cards first (both modes)
  unclaimed_doing=$("$BL" list --json 2>/dev/null | jq -r 'select(.status == "doing" and (.assigned_to == null or .assigned_to == "")) | "\(.id): \(.title)"' 2>/dev/null | head -1 || true)
  if [ -n "$unclaimed_doing" ]; then
    next_action="$unclaimed_doing is in doing with no assignee. Claim it or spawn a worker."
  elif [ "$stop_mode" = "autonomous" ]; then
    # Suggest next todo card when no unclaimed doing work
    next_card=$("$BL" ready --json 2>/dev/null | jq -r 'select(.status == "todo") | "\(.id) — \(.title)"' 2>/dev/null | head -1 || true)
    if [ -n "$next_card" ]; then
      next_action="Dispatch now: $next_card"
    fi
  fi

  # Build the block message — mode sets the framing, next_action adds specifics.
  auto_suffix=""
  if [ "$stop_mode" = "autonomous" ]; then
    reason="Board has active work remaining"
    summary="${todo_count} todo and ${doing_count} doing cards remain."
    auto_suffix=" Autonomous mode: merge reviewed cards without asking — reviewer approval is sufficient."
  else
    reason="Stop mode: batch. ${doing_count} cards in doing — finish or unclaim them before stopping."
    summary="${doing_count} cards in doing — finish or unclaim them before stopping."
  fi

  if [ -n "$next_action" ]; then
    guidance="${summary} ${next_action}"
  else
    if [ "$stop_mode" = "autonomous" ]; then
      guidance="${summary} Dispatch the next card now. The stop hook keeps you running until the board is drained."
    else
      guidance="${summary}"
    fi
  fi

  jq -n \
    --arg reason "$reason" \
    --arg mode "$stop_mode" \
    --arg guidance "$guidance" \
    --arg suffix "$auto_suffix" \
    '{
      decision: "block",
      reason: $reason,
      systemMessage: ("Stop mode: " + $mode + ". " + $guidance + $suffix)
    }' | debounce_stop_message
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
