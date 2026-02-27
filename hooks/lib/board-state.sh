#!/usr/bin/env bash
# Shared functions for reading and diffing board state.
# Sourced by session-start.sh, board-sync.sh, stop-guard.sh, pre-compact.sh.

# Anchor to BL_ROOT when set (worktree support), else git root, else cwd.
_GIT_ROOT="${BL_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"

# Directory name for ralph-ban config. Override with RALPH_BAN_DIR env var.
RALPH_BAN_DIR="${RALPH_BAN_DIR:-.ralph-ban}"

# db_exists checks whether the beads-lite database is reachable.
# Uses BL_ROOT if set (worktree support), else checks cwd.
db_exists() {
  [ -f "${BL_ROOT:-.}/.beads-lite/beads.db" ]
}

# require_bl exits cleanly if bl is missing or the database doesn't exist.
# Hooks should fail open when the tool chain isn't available.
require_bl() {
  if ! command -v "$BL" &>/dev/null; then
    exit 0
  fi
  if ! db_exists; then
    exit 0
  fi
}

SNAPSHOT_FILE="${_GIT_ROOT}/${RALPH_BAN_DIR}/.last-seen.json"
BOUNCE_FILE="${_GIT_ROOT}/${RALPH_BAN_DIR}/.bounce-counts.json"
BL="${BL:-bl}"
REVIEW_QUEUE_THRESHOLD="${REVIEW_QUEUE_THRESHOLD:-3}"

# Per-invocation board cache using a temp file.
# Variable assignments inside $() don't propagate (subshell), but the file
# path is set at source-time and its existence drives the cache. PID scoping
# ensures each hook invocation gets its own cache, cleaned up on exit.
_BOARD_CACHE_FILE="${TMPDIR:-/tmp}/ralph-ban-board.$$.cache"
trap 'rm -f "$_BOARD_CACHE_FILE"' EXIT

# read_board outputs the current board state as JSONL.
# Caches the result so multiple callers within one hook invocation
# hit SQLite exactly once.
read_board() {
  if [ -f "$_BOARD_CACHE_FILE" ]; then
    cat "$_BOARD_CACHE_FILE"
    return
  fi
  "$BL" list --json 2>/dev/null | tee "$_BOARD_CACHE_FILE"
}

# read_board_hash returns a hash of the current board state for cheap comparison.
read_board_hash() {
  local state
  state=$(read_board)
  if [ -z "$state" ]; then
    echo ""
    return
  fi
  echo "$state" | shasum -a 256 | cut -d' ' -f1
}

# save_snapshot writes the current board state and hash to the snapshot file.
save_snapshot() {
  local state hash
  state=$(read_board)
  hash=$(echo "$state" | shasum -a 256 | cut -d' ' -f1)
  mkdir -p "$(dirname "$SNAPSHOT_FILE")"
  jq -n --arg hash "$hash" --arg state "$state" \
    '{hash: $hash, state: $state}' >"$SNAPSHOT_FILE"
}

# load_snapshot_hash returns the stored hash, or empty if no snapshot.
load_snapshot_hash() {
  if [ -f "$SNAPSHOT_FILE" ]; then
    jq -r '.hash // ""' <"$SNAPSHOT_FILE"
  else
    echo ""
  fi
}

# diff_board compares current state to snapshot and outputs human-readable deltas.
# Returns 0 if changed, 1 if unchanged.
diff_board() {
  local old_hash new_hash
  old_hash=$(load_snapshot_hash)
  new_hash=$(read_board_hash)

  if [ "$old_hash" = "$new_hash" ]; then
    return 1
  fi

  # Parse the old and new states to describe changes
  local old_state new_state
  old_state=""
  if [ -f "$SNAPSHOT_FILE" ]; then
    old_state=$(jq -r '.state // ""' <"$SNAPSHOT_FILE")
  fi
  new_state=$(read_board)

  # Build a description of what changed
  describe_changes "$old_state" "$new_state"
  return 0
}

# describe_changes compares two JSONL board states and outputs change descriptions.
describe_changes() {
  local old_state="$1"
  local new_state="$2"

  # Parse JSONL into TSV (id\tstatus\ttitle) with a single jq -s per side.
  local tmp_new="" tmp_old=""
  tmp_new=$(mktemp)
  tmp_old=$(mktemp)

  echo "$new_state" | jq -s -r '.[] | [.id, .status, .title] | @tsv' 2>/dev/null >"$tmp_new"
  echo "$old_state" | jq -s -r '.[] | [.id, .status, .title] | @tsv' 2>/dev/null >"$tmp_old"

  # Find status changes
  while IFS=$'\t' read -r new_id new_status new_title; do
    [ -z "$new_id" ] && continue
    old_line=$(grep "^${new_id}	" "$tmp_old" 2>/dev/null || true)
    if [ -z "$old_line" ]; then
      echo "New card '${new_title}' (${new_id}) added to ${new_status}"
    else
      old_status=$(echo "$old_line" | cut -f2)
      if [ "$old_status" != "$new_status" ]; then
        echo "Card '${new_title}' (${new_id}) moved from ${old_status} to ${new_status}"
      fi
    fi
  done <"$tmp_new"

  # Find deleted cards
  while IFS=$'\t' read -r old_id old_status old_title; do
    [ -z "$old_id" ] && continue
    if ! grep -q "^${old_id}	" "$tmp_new" 2>/dev/null; then
      echo "Card '${old_title}' removed"
    fi
  done <"$tmp_old"

  rm -f "$tmp_new" "$tmp_old"
}

# count_active returns the number of items in todo or doing columns.
count_active() {
  local state
  state=$(read_board)
  if [ -z "$state" ]; then
    echo "0 0"
    return
  fi

  local todo_count doing_count
  todo_count=$(echo "$state" | jq -r 'select(.status == "todo")' | jq -s 'length')
  doing_count=$(echo "$state" | jq -r 'select(.status == "doing")' | jq -s 'length')
  echo "$todo_count $doing_count"
}

# count_review returns the number of items in the review column.
count_review() {
  local state
  state=$(read_board)
  if [ -z "$state" ]; then
    echo "0"
    return
  fi
  echo "$state" | jq -r 'select(.status == "review")' | jq -s 'length'
}

# --- File locking ---
# macOS doesn't ship flock. No-op shim so the existing
# (flock -x 200; ...) 200>lockfile pattern doesn't crash.
if ! command -v flock &>/dev/null; then
  flock() { :; }
fi

# --- Legacy bounce tracking (used by cb_record_success cleanup path) ---

# clear_bounce removes the bounce count for a card (called when card reaches done).
clear_bounce() {
  local card_id="$1"
  if [ -f "$BOUNCE_FILE" ]; then
    (
      flock -x 200
      jq --arg id "$card_id" 'del(.[$id])' <"$BOUNCE_FILE" >"${BOUNCE_FILE}.tmp"
      mv "${BOUNCE_FILE}.tmp" "$BOUNCE_FILE"
    ) 200>"${BOUNCE_FILE}.lock"
  fi
}

# --- Circuit breaker state machine ---
# States: CLOSED (normal) → OPEN (tripped, cool-down) → HALF_OPEN (probe) → CLOSED
#
# Schema: { "card-id": { "state": "CLOSED|OPEN|HALF_OPEN", "bounce_count": N,
#                        "opened_at": unix_timestamp, "last_bounce": unix_timestamp } }
#
# Transitions:
#   CLOSED  + bounce >= BOUNCE_THRESHOLD → OPEN (start cool-down)
#   OPEN    + cool-down expired          → HALF_OPEN (allow one probe attempt)
#   HALF_OPEN + bounce (failure)         → OPEN (restart cool-down)
#   HALF_OPEN + done  (success)          → CLOSED (reset count)
#   CLOSED  + done                       → CLOSED (clear entry)

CB_FILE="${_GIT_ROOT}/${RALPH_BAN_DIR}/.circuit-breaker.json"
BOUNCE_THRESHOLD="${BOUNCE_THRESHOLD:-3}"
CB_COOLDOWN_SECONDS="${CB_COOLDOWN_SECONDS:-300}" # 5 minutes default

# _cb_read_entry returns the JSON object for a card, or a default CLOSED entry.
_cb_read_entry() {
  local card_id="$1"
  if [ ! -f "$CB_FILE" ]; then
    echo '{"state":"CLOSED","bounce_count":0,"opened_at":0,"last_bounce":0}'
    return
  fi
  local entry
  entry=$(jq -r --arg id "$card_id" '.[$id] // empty' <"$CB_FILE" 2>/dev/null)
  if [ -z "$entry" ]; then
    echo '{"state":"CLOSED","bounce_count":0,"opened_at":0,"last_bounce":0}'
  else
    echo "$entry"
  fi
}

# _cb_write_entry updates or creates the entry for a card in the circuit breaker file.
_cb_write_entry() {
  local card_id="$1"
  local entry="$2"
  mkdir -p "$(dirname "$CB_FILE")"
  (
    flock -x 200
    if [ ! -f "$CB_FILE" ]; then
      echo '{}' >"$CB_FILE"
    fi
    jq --arg id "$card_id" --argjson entry "$entry" \
      '.[$id] = $entry' <"$CB_FILE" >"${CB_FILE}.tmp"
    mv "${CB_FILE}.tmp" "$CB_FILE"
  ) 200>"${CB_FILE}.lock"
}

# _cb_delete_entry removes a card's entry from the circuit breaker file.
_cb_delete_entry() {
  local card_id="$1"
  if [ -f "$CB_FILE" ]; then
    (
      flock -x 200
      jq --arg id "$card_id" 'del(.[$id])' <"$CB_FILE" >"${CB_FILE}.tmp"
      mv "${CB_FILE}.tmp" "$CB_FILE"
    ) 200>"${CB_FILE}.lock"
  fi
}

# cb_get_state returns the current circuit breaker state for a card: CLOSED, OPEN, or HALF_OPEN.
# If the file is missing or corrupt, returns CLOSED (fail open).
cb_get_state() {
  local card_id="$1"
  local now
  now=$(date +%s)
  local entry
  entry=$(_cb_read_entry "$card_id")
  local state bounce_count opened_at
  state=$(echo "$entry" | jq -r '.state // "CLOSED"')
  opened_at=$(echo "$entry" | jq -r '.opened_at // 0')

  # OPEN transitions to HALF_OPEN when cool-down expires. This check happens
  # on every read so we don't need a scheduled job — it's lazy evaluation.
  if [ "$state" = "OPEN" ] && [ "$now" -gt 0 ] && [ "$opened_at" -gt 0 ]; then
    local elapsed
    elapsed=$((now - opened_at))
    if [ "$elapsed" -ge "$CB_COOLDOWN_SECONDS" ]; then
      # Promote to HALF_OPEN and persist the transition.
      local new_entry
      new_entry=$(echo "$entry" | jq '.state = "HALF_OPEN"')
      _cb_write_entry "$card_id" "$new_entry"
      echo "HALF_OPEN"
      return
    fi
  fi

  echo "$state"
}

# cb_record_bounce records a review→doing bounce and advances the state machine.
# Returns: "OPEN <bounce_count>" or "HALF_OPEN_REOPEN" or "CLOSED <bounce_count>"
cb_record_bounce() {
  local card_id="$1"
  local now
  now=$(date +%s)
  local entry
  entry=$(_cb_read_entry "$card_id")
  local state bounce_count
  state=$(cb_get_state "$card_id")
  bounce_count=$(echo "$entry" | jq -r '.bounce_count // 0')
  bounce_count=$((bounce_count + 1))

  case "$state" in
  CLOSED)
    if [ "$bounce_count" -ge "$BOUNCE_THRESHOLD" ]; then
      # Trip the breaker: CLOSED → OPEN
      local new_entry
      new_entry=$(jq -n \
        --arg state "OPEN" \
        --argjson bc "$bounce_count" \
        --argjson oa "$now" \
        --argjson lb "$now" \
        '{"state":$state,"bounce_count":$bc,"opened_at":$oa,"last_bounce":$lb}')
      _cb_write_entry "$card_id" "$new_entry"
      echo "OPEN $bounce_count"
    else
      # Still CLOSED, update count
      local new_entry
      new_entry=$(echo "$entry" | jq \
        --argjson bc "$bounce_count" \
        --argjson lb "$now" \
        '.bounce_count = $bc | .last_bounce = $lb')
      _cb_write_entry "$card_id" "$new_entry"
      echo "CLOSED $bounce_count"
    fi
    ;;
  OPEN)
    # Another bounce while OPEN — restart the cool-down timer
    local new_entry
    new_entry=$(jq -n \
      --arg state "OPEN" \
      --argjson bc "$bounce_count" \
      --argjson oa "$now" \
      --argjson lb "$now" \
      '{"state":$state,"bounce_count":$bc,"opened_at":$oa,"last_bounce":$lb}')
    _cb_write_entry "$card_id" "$new_entry"
    echo "OPEN $bounce_count"
    ;;
  HALF_OPEN)
    # Probe failed: HALF_OPEN → OPEN, restart cool-down
    local new_entry
    new_entry=$(jq -n \
      --arg state "OPEN" \
      --argjson bc "$bounce_count" \
      --argjson oa "$now" \
      --argjson lb "$now" \
      '{"state":$state,"bounce_count":$bc,"opened_at":$oa,"last_bounce":$lb}')
    _cb_write_entry "$card_id" "$new_entry"
    echo "HALF_OPEN_REOPEN $bounce_count"
    ;;
  esac
}

# cb_record_success records a review→done success and resets the state machine.
# Any state → CLOSED, entry deleted.
cb_record_success() {
  local card_id="$1"
  _cb_delete_entry "$card_id"
  # Also clear the legacy bounce count file for the same card.
  clear_bounce "$card_id"
}

# --- Stall detection for in-progress cards ---

STALL_THRESHOLD="${STALL_THRESHOLD:-5}"
PROGRESS_FILE="${_GIT_ROOT}/${RALPH_BAN_DIR}/.worker-progress.json"

# record_card_progress updates stale cycle counts for doing cards.
# Call once per board-sync. Cards that leave doing are dropped.
record_card_progress() {
  local state
  state=$(read_board)
  if [ -z "$state" ]; then
    return
  fi

  # Get current doing cards as JSON array
  local doing_cards
  doing_cards=$(echo "$state" | jq -s '[.[] | select(.status == "doing") | {id, status, assigned_to}]' 2>/dev/null || echo "[]")

  # Load existing progress or start fresh
  local progress
  if [ -f "$PROGRESS_FILE" ]; then
    progress=$(cat "$PROGRESS_FILE")
  else
    progress="{}"
  fi

  # Increment stale_cycles for unchanged cards, reset for changed/new.
  # Cards that left doing are dropped (only current doing cards appear).
  local updated
  updated=$(jq -n \
    --argjson doing "$doing_cards" \
    --argjson prev "$progress" \
    '
    reduce ($doing[]) as $card ({};
      . + {
        ($card.id): (
          if $prev[$card.id] then
            if $prev[$card.id].status == $card.status then
              $prev[$card.id] | .stale_cycles += 1
            else
              {status: $card.status, agent: ($card.assigned_to // "unknown"), stale_cycles: 0}
            end
          else
            {status: $card.status, agent: ($card.assigned_to // "unknown"), stale_cycles: 0}
          end
        )
      }
    )
    ' 2>/dev/null || echo "{}")

  mkdir -p "$(dirname "$PROGRESS_FILE")"
  echo "$updated" >"$PROGRESS_FILE"
}

# detect_stalled_cards outputs warnings for cards exceeding the stall threshold.
detect_stalled_cards() {
  if [ ! -f "$PROGRESS_FILE" ]; then
    return
  fi

  local threshold="${STALL_THRESHOLD}"
  jq -r --argjson thresh "$threshold" '
    to_entries[]
    | select(.value.stale_cycles >= $thresh)
    | "Card \(.key) (agent: \(.value.agent)) has been stalled for \(.value.stale_cycles) cycles without progress"
  ' "$PROGRESS_FILE" 2>/dev/null || true
}

# read_stop_mode returns the configured stop mode: "batch" or "autonomous".
# Precedence: RALPH_BAN_STOP_MODE env > .ralph-ban/config.json > "batch" default.
# batch:      block only on doing cards — orchestrator stops once dispatched work completes.
# autonomous: block on todo + doing — orchestrator grinds until the board is empty.
read_stop_mode() {
  # Env var set by CLI flag — per-session, highest precedence.
  if [ -n "${RALPH_BAN_STOP_MODE:-}" ]; then
    echo "$RALPH_BAN_STOP_MODE"
    return
  fi
  # Config file — user-owned persistent default.
  local config_file="${_GIT_ROOT}/${RALPH_BAN_DIR}/config.json"
  if [ -f "$config_file" ]; then
    jq -r '.stop_mode // "batch"' "$config_file" 2>/dev/null || echo "batch"
  else
    echo "batch"
  fi
}

# --- Worker activity marker ---
# The orchestrator writes this marker before spawning workers and removes it
# when all workers complete. The stop hook reads it to pause quietly while
# workers are running, rather than repeatedly firing board-state guidance that
# the orchestrator cannot act on mid-dispatch.
#
# Staleness expiry: if the orchestrator crashes or forgets to clear the marker,
# it auto-expires after WORKER_MARKER_TTL_SECONDS (default 5 minutes). Any stop
# hook invocation beyond that window treats the marker as stale and ignores it.

WORKER_MARKER_FILE="${_GIT_ROOT}/${RALPH_BAN_DIR}/.workers-active"
WORKER_MARKER_TTL_SECONDS="${WORKER_MARKER_TTL_SECONDS:-300}" # 5 minutes default

# write_worker_marker records the current timestamp as the dispatch start.
write_worker_marker() {
  mkdir -p "$(dirname "$WORKER_MARKER_FILE")"
  date +%s >"$WORKER_MARKER_FILE"
}

# clear_worker_marker removes the worker activity marker.
clear_worker_marker() {
  rm -f "$WORKER_MARKER_FILE"
}

# check_worker_marker returns 0 (workers active) or 1 (no active workers).
# Returns 1 if the marker is missing or older than WORKER_MARKER_TTL_SECONDS.
check_worker_marker() {
  if [ ! -f "$WORKER_MARKER_FILE" ]; then
    return 1
  fi

  local marker_ts now elapsed
  marker_ts=$(cat "$WORKER_MARKER_FILE" 2>/dev/null || echo "0")
  now=$(date +%s)
  elapsed=$((now - marker_ts))

  if [ "$elapsed" -ge "$WORKER_MARKER_TTL_SECONDS" ]; then
    # Stale marker — auto-expire so a crashed orchestrator can't trap agents forever.
    rm -f "$WORKER_MARKER_FILE"
    return 1
  fi

  return 0
}

# --- Rate limit pause ---
# When a worker hits Claude's 5-hour rate limit the board-sync hook writes a
# pause marker. The orchestrator reads it before dispatching new work, so it
# won't spawn agents that will immediately hit the same wall.
#
# The pause auto-clears after RATE_LIMIT_PAUSE_SECONDS (default 30 minutes).
# Removing the file manually also clears it.

RATE_LIMIT_PAUSE_FILE="${_GIT_ROOT}/${RALPH_BAN_DIR}/.rate-limit-pause"
RATE_LIMIT_PAUSE_SECONDS="${RATE_LIMIT_PAUSE_SECONDS:-1800}" # 30 minutes default

# write_rate_limit_pause records the current timestamp as the pause start.
write_rate_limit_pause() {
  mkdir -p "$(dirname "$RATE_LIMIT_PAUSE_FILE")"
  date +%s >"$RATE_LIMIT_PAUSE_FILE"
}

# check_rate_limit_pause returns 0 (paused) or 1 (clear).
# Auto-expires the marker after RATE_LIMIT_PAUSE_SECONDS.
# Outputs a human-readable status string when paused.
check_rate_limit_pause() {
  if [ ! -f "$RATE_LIMIT_PAUSE_FILE" ]; then
    return 1
  fi

  local pause_start now elapsed
  pause_start=$(cat "$RATE_LIMIT_PAUSE_FILE" 2>/dev/null || echo "0")
  now=$(date +%s)
  elapsed=$((now - pause_start))

  if [ "$elapsed" -ge "$RATE_LIMIT_PAUSE_SECONDS" ]; then
    # Limit has likely lifted — remove stale marker.
    rm -f "$RATE_LIMIT_PAUSE_FILE"
    return 1
  fi

  local remaining lift_at
  remaining=$((RATE_LIMIT_PAUSE_SECONDS - elapsed))
  lift_at=$(date -r "$pause_start" "+%H:%M" 2>/dev/null || date -d "@$pause_start" "+%H:%M" 2>/dev/null || echo "unknown")
  # Output info string for callers that want to surface it to the agent/user.
  printf 'Rate limit pause active (detected at %s, clears in ~%dm). Skipping new dispatch.' \
    "$lift_at" "$(((remaining + 59) / 60))"
  return 0
}
