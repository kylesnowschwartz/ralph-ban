#!/usr/bin/env bash
# UserPromptSubmit hook: reads board, diffs against snapshot, injects delta.
# Detects dispatch opportunities (unclaimed todo) and review queue depth.
# Output: hookSpecificOutput.additionalContext (injected into Claude's context).
# Exit 0 always — context injection only, never blocks prompts.
set -euo pipefail
trap 'echo "{\"hookSpecificOutput\":{\"hookEventName\":\"UserPromptSubmit\",\"additionalContext\":\"Hook error in $(basename "$0"): $BASH_COMMAND failed\"}}" 2>/dev/null; exit 0' ERR

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/lib/board-state.sh"
require_bl

# --- Rate limit detection ---
# Scan the hook's input JSON for known rate limit signals before doing anything
# else. Claude Code passes the full context as stdin JSON.
#
# IMPORTANT: We match JSON structure (quoted keys and colons), NOT bare prose.
# The input JSON includes board state with card titles and descriptions — a card
# titled "investigate rate limit errors" must NOT trigger a pause. Patterns that
# include JSON syntax (quotes, colons) can't appear in plain-text card titles.
#
# Patterns matched:
#   "type":"rate_limit_error"   — Anthropic SDK structured error type field
#   "type":"overloaded_error"   — Anthropic overload structured error type field
#   "status":429                — HTTP status 429 as a JSON numeric field value
#   "status_code":429           — alternate HTTP status field name used by some clients
input_json=""
if [ ! -t 0 ]; then
  input_json=$(cat 2>/dev/null || true)
fi

rate_limit_warning=""
if echo "$input_json" | grep -q '"type"[[:space:]]*:[[:space:]]*"rate_limit_error"\|"type"[[:space:]]*:[[:space:]]*"overloaded_error"\|"status"[[:space:]]*:[[:space:]]*429\|"status_code"[[:space:]]*:[[:space:]]*429' 2>/dev/null; then
  write_rate_limit_pause
  rate_limit_warning="RATE LIMIT DETECTED: Claude API rate limit signal found in hook context. Pause marker written — new dispatches are suppressed for up to 30 minutes. Existing workers will continue. Check active doing cards and wait for the limit to lift before spawning new agents."
fi

# Diff against last snapshot
changes=""
if diff_output=$(diff_board); then
  changes="$diff_output"
  save_snapshot
fi

# --- Dispatch and review nudges (orchestrator only) ---
# Workers/reviewers shouldn't get dispatch suggestions meant for the lead.
# Board state and circuit breaker context still flows to everyone.
dispatch_nudge=""
review_nudge=""
pause_notice=""
if [ -z "${CLAUDE_TEAM_NAME:-}" ]; then
  # Check for active rate limit pause before suggesting new dispatches.
  # check_rate_limit_pause exits 0 when paused (outputs info string), 1 when clear.
  if pause_notice=$(check_rate_limit_pause 2>/dev/null); then
    # Paused — skip dispatch nudge, surface pause_notice in output instead.
    true
  else
    # bl ready --unclaimed respects dependencies and only returns unblocked, unclaimed items.
    unclaimed_todo=$("$BL" ready --unclaimed --json 2>/dev/null | jq -r '
      select(.status == "todo")
      | "\(.id): \(.title)"
    ' 2>/dev/null || true)

    if [ -n "$unclaimed_todo" ]; then
      count=$(echo "$unclaimed_todo" | wc -l | tr -d ' ')
      first=$(echo "$unclaimed_todo" | head -1)
      if [ "$count" -eq 1 ]; then
        dispatch_nudge="1 unclaimed todo card ready for work: ${first}. Consider delegating to a worker agent in an isolated worktree."
      else
        dispatch_nudge="${count} unclaimed todo cards. Highest priority: ${first}. Consider delegating to worker agents in isolated worktrees."
      fi
    fi
  fi

  # --- Review queue depth ---
  review_count=$(count_review)
  if [ "$review_count" -ge "$REVIEW_QUEUE_THRESHOLD" ] 2>/dev/null; then
    state=$(read_board)
    review_cards=$(echo "$state" | jq -r '
      select(.status == "review")
      | "\(.id): \(.title)"
    ' 2>/dev/null || true)
    review_nudge="Review queue has ${review_count} items — consider dispatching reviewers:
${review_cards}
Each card needs a reviewer in an isolated worktree. Approved cards get merged and closed, rejected cards go back to doing with feedback."
  fi
fi

# --- Circuit breaker: detect review bounces ---
# Uses a CLOSED/OPEN/HALF_OPEN state machine so cards that bounced early in a
# long session can recover instead of staying permanently flagged.
#
#   CLOSED    — normal operation; counting bounces
#   OPEN      — tripped; escalate to human; cool-down timer running
#   HALF_OPEN — cool-down expired; probe state: one more attempt allowed
#               failure → OPEN (restart cool-down), success → CLOSED (reset)
breaker_warning=""
if [ -n "$changes" ]; then
  # Parse changes for review→doing transitions.
  # describe_changes includes card IDs: "Card 'title' (bl-xxxx) moved from X to Y"
  # Extract the ID directly — no title→ID lookup needed.
  while IFS= read -r line; do
    if echo "$line" | grep -q "moved from review to doing" 2>/dev/null; then
      card_id=$(echo "$line" | grep -o 'bl-[a-z0-9]*')
      if [ -n "$card_id" ]; then
        result=$(cb_record_bounce "$card_id")
        result_state=$(echo "$result" | cut -d' ' -f1)
        result_count=$(echo "$result" | cut -d' ' -f2)
        case "$result_state" in
        OPEN)
          breaker_warning="CIRCUIT BREAKER OPEN: Card ${card_id} has bounced ${result_count} times. Stop auto-dispatching. Ask the user for direction — the task may need rethinking, splitting, or manual intervention."
          ;;
        HALF_OPEN_REOPEN)
          breaker_warning="CIRCUIT BREAKER RE-OPENED: Card ${card_id} failed its probe attempt (${result_count} total bounces). Cool-down restarted. Ask the user for direction before retrying."
          ;;
          # CLOSED: still within normal threshold — no warning needed
        esac
      fi
    fi
    # A card reaching done resets the circuit breaker (success path)
    if echo "$line" | grep -q "moved from review to done\|moved from .* to done" 2>/dev/null; then
      card_id=$(echo "$line" | grep -o 'bl-[a-z0-9]*')
      if [ -n "$card_id" ]; then
        cb_record_success "$card_id"
      fi
    fi
  done <<<"$changes"
fi

# Also check for any cards currently in HALF_OPEN state (cool-down expired).
# Emit a nudge so the orchestrator knows a probe attempt is allowed.
half_open_nudge=""
if [ -f "${_GIT_ROOT}/${RALPH_BAN_DIR}/.circuit-breaker.json" ]; then
  while IFS= read -r card_id; do
    [ -z "$card_id" ] && continue
    current_state=$(cb_get_state "$card_id")
    if [ "$current_state" = "HALF_OPEN" ]; then
      count=$(jq -r --arg id "$card_id" '.[$id].bounce_count // 0' \
        <"${_GIT_ROOT}/${RALPH_BAN_DIR}/.circuit-breaker.json" 2>/dev/null || echo "?")
      half_open_nudge="${half_open_nudge}CIRCUIT BREAKER HALF-OPEN: Card ${card_id} had ${count} bounces but cool-down has expired. One probe attempt allowed — monitor closely. Success resets the breaker; failure reopens it.
"
    fi
  done < <(jq -r 'keys[]' <"${_GIT_ROOT}/${RALPH_BAN_DIR}/.circuit-breaker.json" 2>/dev/null || true)
fi

# --- Stall detection: query agent_state + last_activity ---
# Cards with agent_state=running but a stale last_activity are genuinely stuck.
# STALL_ACTIVITY_MINUTES: how long without activity before a running agent is stalled.
STALL_ACTIVITY_MINUTES="${STALL_ACTIVITY_MINUTES:-30}"
stall_warnings=""
stall_warnings=$(
  state=$(read_board)
  [ -z "$state" ] && exit 0
  now=$(date +%s)
  threshold=$((STALL_ACTIVITY_MINUTES * 60))

  # Extract running cards with their id, agent, and last_activity timestamp.
  # Use a null delimiter to handle spaces in titles safely.
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

# --- Spec audit: review cards with incomplete specs ---
spec_warnings=""
review_with_bad_specs=$("$BL" list --json 2>/dev/null | jq -r '
  .[] | select(.status == "review")
  | select(.specifications != null and (.specifications | length) > 0)
  | select([.specifications[] | select(.checked == false)] | length > 0)
  | "\(.id): \(.title) (\([.specifications[] | select(.checked)] | length)/\(.specifications | length) specs)"
' 2>/dev/null || true)

if [ -n "$review_with_bad_specs" ]; then
  # shellcheck disable=SC2034 # used in agent_parts/user_parts below
  spec_warnings="Review cards with incomplete specs (moved via --force):
${review_with_bad_specs}
Complete specs before merging."
fi

# Build separate output for agent context (everything) and user display (actionable only).
# The agent needs dispatch nudges, review queue depth, and board diffs for orchestration.
# The user only needs actionable warnings — circuit breaker, stalls, rate limits.
# Board diffs and dispatch nudges are internal guidance that confuse the user when surfaced.
agent_parts=()
user_parts=()

if [ -n "$rate_limit_warning" ]; then
  agent_parts+=("$rate_limit_warning")
  user_parts+=("$rate_limit_warning")
fi
if [ -n "$breaker_warning" ]; then
  agent_parts+=("$breaker_warning")
  user_parts+=("$breaker_warning")
fi
if [ -n "$half_open_nudge" ]; then
  agent_parts+=("$half_open_nudge")
  user_parts+=("$half_open_nudge")
fi
if [ -n "$changes" ]; then
  agent_parts+=("Board changes since last prompt:")
  agent_parts+=("$changes")
fi
if [ -n "$pause_notice" ]; then
  agent_parts+=("$pause_notice")
  user_parts+=("$pause_notice")
fi
if [ -n "$dispatch_nudge" ]; then
  agent_parts+=("$dispatch_nudge")
fi
if [ -n "$review_nudge" ]; then
  agent_parts+=("$review_nudge")
fi
if [ -n "$stall_warnings" ]; then
  agent_parts+=("STALL DETECTED:")
  agent_parts+=("$stall_warnings")
  user_parts+=("STALL DETECTED:")
  user_parts+=("$stall_warnings")
fi
if [ -n "$spec_warnings" ]; then
  agent_parts+=("$spec_warnings")
  user_parts+=("$spec_warnings")
fi

if [ ${#agent_parts[@]} -gt 0 ]; then
  # Agent context: full orchestration state with lifecycle reminder.
  agent_parts=("Orchestration checkpoint: board sync follows." "${agent_parts[@]}")
  agent_message=$(printf '%s\n' "${agent_parts[@]}")

  # User-visible: only actionable warnings. Empty string suppresses systemMessage.
  user_message=""
  if [ ${#user_parts[@]} -gt 0 ]; then
    user_message=$(printf '%s\n' "${user_parts[@]}")
  fi

  jq -n --arg ctx "$agent_message" --arg msg "$user_message" \
    '{hookSpecificOutput: {hookEventName: "UserPromptSubmit", additionalContext: $ctx}, systemMessage: $msg}'
fi
