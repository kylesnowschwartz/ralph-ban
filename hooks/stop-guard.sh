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
#   4.5. Agent activity gate (pauses quietly while any role is running)
#   5. Anti-loop guard (mode-aware: batch exits, autonomous falls through)
#   6. Stall detection (safety valve: allows exit after MAX_STALLS)
#   6.5. Circuit breaker warnings (cards bouncing review→doing)
#   6.75. Worker-level stall detection (cards with stale last_activity)
#   7. Block decision + guidance message

# --- Phase 1: Escape hatch ---
# Derive git root without sourcing board-state.sh (minimize failure surface).
_STOP_ROOT="${BL_ROOT:-$(git --no-optional-locks rev-parse --show-toplevel 2>/dev/null || pwd)}"
if [ -f "${_STOP_ROOT}/${RALPH_BAN_DIR:-.ralph-ban}/disabled" ]; then
  exit 0
fi

STOP_MSG_HASH_FILE="${_STOP_ROOT}/${RALPH_BAN_DIR:-.ralph-ban}/.stop-last-msg-hash"

# --- Phase 1.5: Plan mode bypass ---
# The planner reads code and creates board cards — it doesn't own the working
# tree, dispatch workers, or manage board lifecycle. All stop-guard logic
# (uncommitted changes, active work, stall detection) is irrelevant.
if [ "${RALPH_BAN_PLAN_MODE:-}" = "1" ]; then
  exit 0
fi

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
if [ -n "$(git --no-optional-locks status --porcelain 2>/dev/null)" ]; then
  jq -n '{
    decision: "block",
    reason: "Uncommitted changes — commit or stash before stopping",
    systemMessage: "The stop hook blocks exit when uncommitted changes exist. This prevents lost work. Commit your changes, then try stopping again. If the changes are experimental, stash them."
  }'
  exit 0
fi

# --- Phase 4: Tool availability ---
require_bl

# --- Phase 4.5: Agent activity gate ---
# Counts assignments in state 'running' across every role: worker, reviewer,
# oracle. The role-scoped assignments table makes verifiers visible to bl
# without conflicting with the worker's claim — a card may carry up to three
# concurrent rows. When any agent on any card is running, the orchestrator
# is waiting on background work and firing board-state guidance is noise.
#
# The check skips cards in status='done' so a stale assignment on a closed
# card cannot trap the orchestrator forever.
#
# Note: this comes AFTER the uncommitted changes gate — a dirty working tree
# still blocks regardless of whether agents are running.
_running_agents=$("$BL" list --json 2>/dev/null | jq -s '
  [
    .[] | select(.status != "done")
    | (.assignments // [])[] | select(.state == "running")
  ] | length
' 2>/dev/null || echo "0")
if [ "$_running_agents" -gt 0 ]; then
  jq -n --argjson n "$_running_agents" '{
    systemMessage: ("Agents running (\($n) across worker/reviewer/oracle roles). Pausing until they complete.")
  }'
  exit 0
fi

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
stall_count=0

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

# --- Phase 6.5: Circuit breaker warnings ---
# Check for cards with tripped circuit breakers (OPEN or HALF_OPEN).
# The circuit breaker tracks review→doing bounces. OPEN means the card
# has bounced 3+ times and is in cool-down. HALF_OPEN means one probe
# attempt is allowed. These warnings go into the systemMessage so the
# orchestrator sees them alongside the block directive.
cb_warnings=""
if [ -f "$CB_FILE" ]; then
  # Read all card IDs from the circuit breaker file
  cb_card_ids=$(jq -r 'keys[]' "$CB_FILE" 2>/dev/null || true)
  for cb_card_id in $cb_card_ids; do
    cb_state=$(cb_get_state "$cb_card_id")
    case "$cb_state" in
    OPEN)
      cb_bounce_count=$(jq -r --arg id "$cb_card_id" '.[$id].bounce_count // 0' "$CB_FILE" 2>/dev/null || echo "0")
      cb_warnings+="CIRCUIT BREAKER OPEN: Card ${cb_card_id} has bounced ${cb_bounce_count} times between review and doing. Escalate to user or try a fundamentally different approach."$'\n'
      ;;
    HALF_OPEN)
      cb_warnings+="CIRCUIT BREAKER HALF_OPEN: Card ${cb_card_id} — one probe attempt allowed. If it bounces again, the breaker re-opens."$'\n'
      ;;
    esac
  done
fi

# --- Phase 6.75: Worker-level stall detection ---
# Complements Phase 6 (orchestrator stall detection). Phase 6 counts consecutive
# stop attempts with no board hash change. This check detects individual workers
# that haven't updated their last_activity in STALL_ACTIVITY_MINUTES.
STALL_ACTIVITY_MINUTES="${STALL_ACTIVITY_MINUTES:-30}"
stall_warnings=""
stall_output=$(
  state=$(read_board)
  [ -z "$state" ] && exit 0
  now=$(date +%s)
  threshold=$((STALL_ACTIVITY_MINUTES * 60))

  while IFS=$'\t' read -r card_id agent last_activity; do
    [ -z "$last_activity" ] && continue
    # Convert ISO 8601 timestamp to epoch. Strip fractional seconds, replace
    # Z with +0000, and strip colon from tz offset so macOS date -j can parse it.
    ts_clean=$(echo "$last_activity" | sed 's/\.[0-9]*//' | sed 's/Z$/+0000/' | sed 's/:\([0-9][0-9]\)$/\1/')
    last_epoch=$(date -j -f "%Y-%m-%dT%H:%M:%S%z" "$ts_clean" "+%s" 2>/dev/null || true)
    [ -z "$last_epoch" ] && continue
    age=$((now - last_epoch))
    if [ "$age" -ge "$threshold" ]; then
      age_min=$((age / 60))
      echo "Card ${card_id} (agent: ${agent:-unknown}) has been running for ${age_min}m without activity update"
    fi
  done < <(echo "$state" | jq -r 'select(.agent_state == "running" and .last_activity != null) | [.id, (.assigned_to // "unknown"), .last_activity] | @tsv' 2>/dev/null || true)
)
if [ -n "$stall_output" ]; then
  stall_warnings="WORKER STALL DETECTED:\n${stall_output}"
fi

# --- Phase 7: Block decision + guidance ---
read -r todo_count doing_count <<<"$(count_active)"

# Reuse the running-agent count from Phase 4.5 if we got here (it was 0 or the
# check was skipped). Re-query only if the variable isn't set (defensive).
# The query mirrors Phase 4.5: any role's assignment in state 'running' on a
# non-done card.
running_count="${_running_agents:-$("$BL" list --json 2>/dev/null | jq -s '
  [
    .[] | select(.status != "done")
    | (.assignments // [])[] | select(.state == "running")
  ] | length
' 2>/dev/null || echo "0")}"

# Batch:      block on running agents (explicit state) or doing cards (catch orphans).
# Autonomous: block on todo + doing until the board drains.
if [ "$stop_mode" = "autonomous" ]; then
  [ "$todo_count" -gt 0 ] || [ "$doing_count" -gt 0 ] && should_block="yes" || should_block="no"
else
  [ "$running_count" -gt 0 ] || [ "$doing_count" -gt 0 ] && should_block="yes" || should_block="no"
fi

if [ "$should_block" = "yes" ]; then
  # Identify the concrete next action for the agent
  next_action=""

  # Check for non-epic doing cards with no running agent (agent_state not running).
  # These are orphaned: in doing but the agent isn't explicitly active.
  unclaimed_doing=$("$BL" list --json 2>/dev/null | jq -r 'select(.status == "doing" and .issue_type != "epic" and (.agent_state == null or .agent_state == "" or .agent_state == "idle" or .agent_state == "done" or .agent_state == "dead")) | "\(.id): \(.title)"' 2>/dev/null | head -1 || true)
  if [ -n "$unclaimed_doing" ]; then
    next_action="$unclaimed_doing is in doing with no active agent. Claim it or spawn a worker."
  elif [ "$stop_mode" = "autonomous" ]; then
    # Suggest next dispatchable todo card (exclude epics — they close when children complete)
    next_card=$("$BL" ready --json 2>/dev/null | jq -r 'select(.status == "todo" and .issue_type != "epic") | "\(.id) — \(.title)"' 2>/dev/null | head -1 || true)
    if [ -n "$next_card" ]; then
      next_action="Dispatch now: $next_card"
    fi
  fi

  # Build the block message.
  # The message must be directive enough that the agent does real work, not
  # just acknowledges and retries stop. Including the stall count serves two
  # purposes: (1) the agent sees how many attempts remain before the safety
  # valve opens, and (2) each message has a unique hash, which naturally
  # defeats the debounce — every attempt gets the full guidance.
  remaining=$((MAX_STALLS - stall_count))

  if [ "$stop_mode" = "autonomous" ]; then
    reason="STOP BLOCKED (attempt $((stall_count + 1)) of ${MAX_STALLS}) — board has active work"
    summary="${todo_count} todo, ${doing_count} doing."
  else
    reason="STOP BLOCKED (attempt $((stall_count + 1)) of ${MAX_STALLS}) — ${running_count} running agents, ${doing_count} cards in doing"
    summary="${running_count} running agent(s), ${doing_count} card(s) in doing — wait for workers to finish or investigate orphaned cards."
  fi

  # Assemble the directive via heredoc — readable and handles newlines naturally.
  # The next_action and autonomous blocks are conditionally appended after.
  directive=$(
    cat <<EOF
STOP BLOCKED — attempt $((stall_count + 1)) of ${MAX_STALLS}. ${summary}

You MUST continue working. Do not respond with a short acknowledgment — that burns a stop cycle without advancing the board. After ${remaining} more stalled attempts the safety valve allows exit, but the goal is to drain the board, not run out the clock.
EOF
  )

  if [ -n "$cb_warnings" ]; then
    directive+=$'\n\n'"${cb_warnings}"
  fi

  if [ -n "$next_action" ]; then
    directive+=$'\n\n'"NEXT ACTION: ${next_action}"
  fi

  # Detect the "all dispatched, waiting for workers" state: cards are in doing
  # but there's nothing actionable to dispatch or claim. The generic "ralph loop"
  # guidance is wrong here — telling the agent to "dispatch or claim work" when
  # there's nothing to dispatch just produces empty acknowledgments that burn
  # stop cycles. Instead, point to Phase 2.5 productive-waiting activities.
  if [ "$todo_count" -eq 0 ] && [ "$doing_count" -gt 0 ] && [ -z "$next_action" ]; then
    directive+=$(
      cat <<'WAITING'


All cards are dispatched — workers are running. Use the wait productively (Phase 2.5):
- Groom backlog: break large cards into smaller ones, add specs to cards that lack them, add missing dependencies
- Review prep: read the files workers are modifying so you review faster when they return
- Small direct fixes: doc typos, config changes, hook tweaks — anything that won't conflict with worker scope

When workers complete, transition to Phase 3 (review). The board will advance when you merge their work.
WAITING
    )
  else
    directive+=$(
      cat <<'LOOP'


The ralph loop: read the board, dispatch or claim work, implement, review, merge, repeat. This hook fires because work remains. The only way it stops firing is board progress — cards moving right or closing.
LOOP
    )
  fi

  if [ "$stop_mode" = "autonomous" ]; then
    directive+=$'\n\n'"Autonomous mode: merge reviewed cards without asking — reviewer approval is sufficient. Dispatch the next card immediately."
  fi

  if [ -n "$stall_warnings" ]; then
    directive+=$'\n\n'"${stall_warnings}"
  fi

  jq -n \
    --arg reason "$reason" \
    --arg directive "$directive" \
    '{
      decision: "block",
      reason: $reason,
      systemMessage: $directive
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
