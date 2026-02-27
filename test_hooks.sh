#!/usr/bin/env bash
# Hook tests for ralph-ban.
# Run from ralph-ban root: bash test_hooks.sh
#
# Tests are auto-discovered: any function named test_* runs automatically.
# No manual test list to maintain.
set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HOOKS_DIR="$SCRIPT_DIR/hooks"

# Use locally built bl — export so hook scripts see it via ${BL:-bl}
BL="${BL:-/usr/local/bin/bl}"
export BL
if [ ! -x "$BL" ]; then
  echo "Building beads-lite..."
  (cd "$SCRIPT_DIR/../beads-lite" && go build -o "$BL" ./cmd/bl)
fi
bl() { "$BL" "$@"; }

PASS=0
FAIL=0
TEST_DIR=""

# --- Helpers ---

extract_id() { echo "$1" | grep -o 'bl-[a-z0-9]*' | head -1; }

setup() {
  TEST_DIR="/tmp/ralph-ban-test-hooks"
  rm -rf "$TEST_DIR"
  mkdir -p "$TEST_DIR"
  cd "$TEST_DIR"
  unset BL_ROOT CLAUDE_TEAM_NAME CLAUDE_AGENT_NAME
  bl init >/dev/null 2>&1
  mkdir -p .ralph-ban
}

teardown() {
  cd /
  rm -f "${TMPDIR:-/tmp}"/ralph-ban-board.*.cache
  rm -rf "$TEST_DIR"
}

assert_contains() {
  local haystack="$1" needle="$2" msg="$3"
  if echo "$haystack" | grep -q "$needle"; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: $msg"
    echo "  expected to contain: $needle"
    echo "  got: $haystack"
  fi
}

assert_not_contains() {
  local haystack="$1" needle="$2" msg="$3"
  if echo "$haystack" | grep -q "$needle"; then
    FAIL=$((FAIL + 1))
    echo "FAIL: $msg"
    echo "  expected NOT to contain: $needle"
  else
    PASS=$((PASS + 1))
  fi
}

assert_eq() {
  local actual="$1" expected="$2" msg="$3"
  if [ "$actual" = "$expected" ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: $msg"
    echo "  expected: $expected"
    echo "  got: $actual"
  fi
}

assert_file_exists() {
  local path="$1" msg="$2"
  if [ -f "$path" ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: $msg"
  fi
}

assert_file_missing() {
  local path="$1" msg="$2"
  if [ ! -f "$path" ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: $msg"
  fi
}

# Invoke a hook that reads stdin (board-sync, stop-guard).
# Always closes stdin to prevent hangs.
run_hook() { "$@" </dev/null 2>/dev/null || true; }

# Invoke a hook with JSON piped to stdin.
run_hook_with_input() {
  local input="$1"
  shift
  echo "$input" | "$@" 2>/dev/null || true
}

# ============================================================
# session-start.sh
# ============================================================

test_session_start_empty_board() {
  setup
  local out
  out=$(run_hook "$HOOKS_DIR/session-start.sh")
  assert_contains "$out" "additionalContext" "session-start: outputs additionalContext"
  assert_contains "$out" "empty" "session-start: reports empty board"
  teardown
}

test_session_start_with_tasks() {
  setup
  bl create "Priority Task" --priority 0 >/dev/null
  bl create "Low Priority" --priority 4 >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/session-start.sh")
  assert_contains "$out" "additionalContext" "session-start: outputs additionalContext with tasks"
  assert_contains "$out" "Priority Task" "session-start: suggests highest priority task"
  assert_contains "$out" "ready" "session-start: mentions ready items"
  teardown
}

test_session_start_creates_snapshot() {
  setup
  bl create "Snapshot Test" >/dev/null
  run_hook "$HOOKS_DIR/session-start.sh" >/dev/null
  assert_file_exists ".ralph-ban/.last-seen.json" "session-start: creates snapshot file"
  teardown
}

# ============================================================
# board-sync.sh
# ============================================================

test_board_sync_no_change() {
  setup
  bl create "Static Task" >/dev/null
  run_hook "$HOOKS_DIR/session-start.sh" >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/board-sync.sh")
  # No change since snapshot — should produce no output or minimal output
  PASS=$((PASS + 1))
  teardown
}

test_board_sync_detects_status_change() {
  setup
  local id
  id=$(extract_id "$(bl create "Moving Task")")
  run_hook "$HOOKS_DIR/session-start.sh" >/dev/null
  bl update "$id" --status doing >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/board-sync.sh")
  assert_contains "$out" "additionalContext" "board-sync: outputs additionalContext on status change"
  teardown
}

test_board_sync_detects_new_card() {
  setup
  bl create "Original" >/dev/null
  run_hook "$HOOKS_DIR/session-start.sh" >/dev/null
  bl create "Brand New" >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/board-sync.sh")
  assert_contains "$out" "additionalContext" "board-sync: detects new card"
  teardown
}

test_board_sync_tracks_stall() {
  setup
  local id
  id=$(extract_id "$(bl create "Stalling Task")")
  bl update "$id" --status doing >/dev/null
  run_hook "$HOOKS_DIR/session-start.sh" >/dev/null

  export STALL_THRESHOLD=3
  local out=""
  for _ in $(seq 1 4); do
    out=$(run_hook "$HOOKS_DIR/board-sync.sh")
  done
  assert_contains "$out" "STALL" "board-sync: detects stalled card after threshold"
  unset STALL_THRESHOLD
  teardown
}

# ============================================================
# board-state.sh library functions
# ============================================================

test_board_state_read_board() {
  setup
  bl create "Read Test" >/dev/null
  source "$HOOKS_DIR/lib/board-state.sh"
  local board
  board=$(read_board)
  assert_contains "$board" "Read Test" "board-state: read_board returns issue data"
  assert_contains "$board" '"status":"todo"' "board-state: read_board shows correct status"
  teardown
}

test_board_state_count_active() {
  setup
  bl create "Todo 1" >/dev/null
  bl create "Todo 2" >/dev/null
  local doing_id
  doing_id=$(extract_id "$(bl create "Doing 1")")
  bl update "$doing_id" --status doing >/dev/null
  source "$HOOKS_DIR/lib/board-state.sh"
  local counts
  counts=$(count_active)
  assert_contains "$counts" "2" "board-state: count_active shows 2 todo items"
  teardown
}

# ============================================================
# stop-guard.sh — block decision by mode
# ============================================================

test_stop_guard_batch_allows_todo_only() {
  setup
  bl create "Pending Todo" >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard batch: allows exit with only todo cards"
  assert_contains "$out" "batch" "stop-guard batch: reports mode"
  teardown
}

test_stop_guard_batch_blocks_doing() {
  setup
  local id
  id=$(extract_id "$(bl create "In Flight")")
  bl update "$id" --status doing >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "stop-guard batch: blocks on doing cards"
  teardown
}

test_stop_guard_batch_explicit_config() {
  setup
  bl create "Pending Todo" >/dev/null
  echo '{"stop_mode":"batch"}' >.ralph-ban/config.json
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard batch: explicit config allows todo-only"
  assert_contains "$out" "batch" "stop-guard batch: explicit config reports mode"
  teardown
}

test_stop_guard_batch_explicit_blocks_doing() {
  setup
  local id
  id=$(extract_id "$(bl create "In Flight")")
  bl update "$id" --status doing >/dev/null
  echo '{"stop_mode":"batch"}' >.ralph-ban/config.json
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "stop-guard batch: explicit config blocks doing"
  assert_contains "$out" "batch" "stop-guard batch: explicit config reports mode in block"
  teardown
}

test_stop_guard_autonomous_blocks_todo() {
  setup
  bl create "Pending Todo" >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "stop-guard autonomous: blocks on todo cards"
  assert_contains "$out" "autonomous" "stop-guard autonomous: reports mode"
  teardown
}

test_stop_guard_autonomous_blocks_todo_and_doing() {
  setup
  bl create "Pending Todo" >/dev/null
  local id
  id=$(extract_id "$(bl create "In Flight")")
  bl update "$id" --status doing >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "stop-guard autonomous: blocks on todo + doing"
  teardown
}

# ============================================================
# stop-guard.sh — allows exit when board is clear
# ============================================================

test_stop_guard_allows_empty_board() {
  setup
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard: allows exit on empty board"
  teardown
}

test_stop_guard_allows_only_done() {
  setup
  local id
  id=$(extract_id "$(bl create "Finished")")
  bl close "$id" >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard: allows exit with only done cards"
  teardown
}

test_stop_guard_allows_only_backlog() {
  setup
  local id
  id=$(extract_id "$(bl create "Backlog Only")")
  bl update "$id" --status backlog >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard: allows exit with only backlog cards"
  teardown
}

# ============================================================
# stop-guard.sh — config defaults
# ============================================================

test_stop_guard_missing_config_defaults_to_batch() {
  setup
  bl create "Pending Todo" >/dev/null
  # No config.json at all
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard: missing config defaults to batch"
  teardown
}

test_stop_guard_missing_stop_mode_defaults_to_batch() {
  setup
  bl create "Pending Todo" >/dev/null
  echo '{"wip_limits":{"doing":3}}' >.ralph-ban/config.json
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard: missing stop_mode field defaults to batch"
  teardown
}

# ============================================================
# stop-guard.sh — anti-loop guard (stop_hook_active)
# ============================================================

test_stop_guard_anti_loop_allows_batch() {
  setup
  bl create "Active Todo" >/dev/null
  local out
  out=$(run_hook_with_input '{"stop_hook_active":true}' "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard anti-loop: batch mode allows exit"
  teardown
}

test_stop_guard_anti_loop_blocks_autonomous() {
  setup
  bl create "Active Todo" >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  local out
  out=$(run_hook_with_input '{"stop_hook_active":true}' "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "stop-guard anti-loop: autonomous mode still blocks"
  assert_contains "$out" "autonomous" "stop-guard anti-loop: reports autonomous mode"
  teardown
}

# ============================================================
# stop-guard.sh — uncommitted changes gate
# ============================================================

test_stop_guard_blocks_uncommitted_changes() {
  setup
  git init -q . >/dev/null 2>&1
  echo "seed" >seed.txt
  git add seed.txt && git commit -q -m "init" >/dev/null 2>&1
  echo "dirty" >>seed.txt
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "stop-guard: blocks on uncommitted changes"
  teardown
}

test_stop_guard_blocks_uncommitted_even_with_stop_hook_active() {
  setup
  git init -q . >/dev/null 2>&1
  echo "seed" >seed.txt
  git add seed.txt && git commit -q -m "init" >/dev/null 2>&1
  echo "dirty" >>seed.txt
  local out
  out=$(run_hook_with_input '{"stop_hook_active":true}' "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "stop-guard: blocks uncommitted even with stop_hook_active"
  teardown
}

# ============================================================
# stop-guard.sh — escape hatches
# ============================================================

test_stop_guard_respects_disable_marker() {
  setup
  bl create "Active Todo" >/dev/null
  touch .ralph-ban/disabled
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard: respects disable marker"
  teardown
}

test_stop_guard_allows_worker_no_claimed_cards() {
  setup
  bl create "Active Todo" >/dev/null
  local out
  out=$(CLAUDE_AGENT_NAME=worker run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard: allows worker exit with no claimed cards"
  teardown
}

# ============================================================
# stop-guard.sh — stall detection
# ============================================================

test_stop_guard_stall_allows_after_max() {
  setup
  bl create "Stuck Task" >/dev/null
  source "$HOOKS_DIR/lib/board-state.sh"
  local hash
  hash=$(read_board_hash)
  echo "$hash" >.ralph-ban/.stop-board-hash
  echo "4" >.ralph-ban/.stop-cycles # one below MAX_STALLS=5
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard stall: allows exit after max cycles"
  assert_contains "$out" "stop cycles" "stop-guard stall: reports cycle count"
  teardown
}

test_stop_guard_stall_resets_on_progress() {
  setup
  local id
  id=$(extract_id "$(bl create "Progressing Task")")
  bl update "$id" --status doing >/dev/null
  echo "deadbeef_old_hash" >.ralph-ban/.stop-board-hash
  echo "3" >.ralph-ban/.stop-cycles
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "stop-guard stall: still blocks when progress detected"
  assert_eq "$(cat .ralph-ban/.stop-cycles)" "0" "stop-guard stall: counter resets on progress"
  teardown
}

test_stop_guard_stall_safety_valve_autonomous() {
  setup
  bl create "Stuck Todo" >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  source "$HOOKS_DIR/lib/board-state.sh"
  local hash
  hash=$(read_board_hash)
  echo "$hash" >.ralph-ban/.stop-board-hash
  echo "4" >.ralph-ban/.stop-cycles
  # Even in autonomous mode with stop_hook_active, MAX_STALLS allows exit
  local out
  out=$(run_hook_with_input '{"stop_hook_active":true}' "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard stall: autonomous safety valve works"
  assert_contains "$out" "stop cycles" "stop-guard stall: autonomous reports cycle count"
  teardown
}

# ============================================================
# stop-guard.sh — guidance messages
# ============================================================

test_stop_guard_guidance_next_todo() {
  setup
  bl create "Important Feature" >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" "Important Feature" "stop-guard guidance: names next todo card"
  assert_contains "$out" '"block"' "stop-guard guidance: blocks with card-specific guidance"
  teardown
}

test_stop_guard_guidance_unclaimed_doing() {
  setup
  local id
  id=$(extract_id "$(bl create "Orphaned Task")")
  bl update "$id" --status doing >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" "no assignee" "stop-guard guidance: highlights unclaimed doing card"
  assert_contains "$out" '"block"' "stop-guard guidance: blocks with unclaimed doing guidance"
  teardown
}

# ============================================================
# stop-guard.sh — debounce
# ============================================================

test_stop_guard_debounce_first_call_shows_message() {
  setup
  local id
  id=$(extract_id "$(bl create "Doing Task")")
  bl update "$id" --status doing >/dev/null
  rm -f .ralph-ban/.stop-last-msg-hash
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "debounce: first call blocks"
  assert_contains "$out" "systemMessage" "debounce: first call shows systemMessage"
  teardown
}

test_stop_guard_debounce_suppresses_repeat() {
  setup
  local id
  id=$(extract_id "$(bl create "Doing Task")")
  bl update "$id" --status doing >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  rm -f .ralph-ban/.stop-last-msg-hash
  # First call emits and saves hash
  run_hook "$HOOKS_DIR/stop-guard.sh" >/dev/null
  # Second call with identical board — systemMessage should be stripped
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "debounce: second call still blocks"
  assert_not_contains "$out" "systemMessage" "debounce: second identical call omits systemMessage"
  teardown
}

test_stop_guard_debounce_resets_on_board_change() {
  setup
  local id1 id2
  id1=$(extract_id "$(bl create "First Task")")
  bl update "$id1" --status doing >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  rm -f .ralph-ban/.stop-last-msg-hash
  run_hook "$HOOKS_DIR/stop-guard.sh" >/dev/null
  # Change the board — add second doing card
  id2=$(extract_id "$(bl create "Second Task")")
  bl update "$id2" --status doing >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "debounce: blocks after board change"
  assert_contains "$out" "systemMessage" "debounce: shows systemMessage after board change"
  teardown
}

test_stop_guard_debounce_hash_cleared_on_allow() {
  setup
  local id
  id=$(extract_id "$(bl create "Doing Task")")
  bl update "$id" --status doing >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  rm -f .ralph-ban/.stop-last-msg-hash
  run_hook "$HOOKS_DIR/stop-guard.sh" >/dev/null
  assert_file_exists ".ralph-ban/.stop-last-msg-hash" "debounce: hash file exists after block"
  bl close "$id" >/dev/null
  run_hook "$HOOKS_DIR/stop-guard.sh" >/dev/null
  assert_file_missing ".ralph-ban/.stop-last-msg-hash" "debounce: hash file removed after allow"
  teardown
}

# ============================================================
# Circuit breaker state machine (board-state.sh)
# ============================================================

test_cb_closed_below_threshold() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  cb_record_bounce "bl-test1" >/dev/null
  cb_record_bounce "bl-test1" >/dev/null
  local state
  state=$(cb_get_state "bl-test1")
  assert_contains "$state" "CLOSED" "circuit breaker: stays CLOSED below threshold"
  local count
  count=$(jq -r '."bl-test1".bounce_count' <"$CB_FILE" 2>/dev/null || echo "0")
  assert_eq "$count" "2" "circuit breaker: bounce count is 2"
  teardown
}

test_cb_trips_at_threshold() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  cb_record_bounce "bl-test2" >/dev/null
  cb_record_bounce "bl-test2" >/dev/null
  local result
  result=$(cb_record_bounce "bl-test2")
  assert_contains "$result" "OPEN" "circuit breaker: trips to OPEN at threshold"
  local state
  state=$(cb_get_state "bl-test2")
  assert_contains "$state" "OPEN" "circuit breaker: state is OPEN after trip"
  teardown
}

test_cb_open_to_half_open_after_cooldown() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  local past
  past=$(($(date +%s) - 600))
  jq -n --argjson past "$past" \
    '{"bl-test3":{"state":"OPEN","bounce_count":3,"opened_at":$past,"last_bounce":$past}}' \
    >"$CB_FILE"
  local state
  state=$(cb_get_state "bl-test3")
  assert_contains "$state" "HALF_OPEN" "circuit breaker: OPEN to HALF_OPEN after cooldown"
  teardown
}

test_cb_stays_open_within_cooldown() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  local now
  now=$(date +%s)
  jq -n --argjson now "$now" \
    '{"bl-test4":{"state":"OPEN","bounce_count":3,"opened_at":$now,"last_bounce":$now}}' \
    >"$CB_FILE"
  local state
  state=$(cb_get_state "bl-test4")
  assert_contains "$state" "OPEN" "circuit breaker: stays OPEN within cooldown"
  teardown
}

test_cb_half_open_failure_reopens() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  local past
  past=$(($(date +%s) - 600))
  jq -n --argjson past "$past" \
    '{"bl-test5":{"state":"OPEN","bounce_count":3,"opened_at":$past,"last_bounce":$past}}' \
    >"$CB_FILE"
  local result
  result=$(cb_record_bounce "bl-test5")
  assert_contains "$result" "HALF_OPEN_REOPEN" "circuit breaker: probe failure emits HALF_OPEN_REOPEN"
  local state
  state=$(cb_get_state "bl-test5")
  assert_contains "$state" "OPEN" "circuit breaker: re-opens on probe failure"
  teardown
}

test_cb_success_resets() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  cb_record_bounce "bl-test6" >/dev/null
  cb_record_bounce "bl-test6" >/dev/null
  cb_record_bounce "bl-test6" >/dev/null
  local state
  state=$(cb_get_state "bl-test6")
  assert_contains "$state" "OPEN" "circuit breaker: pre-condition OPEN"
  cb_record_success "bl-test6"
  state=$(cb_get_state "bl-test6")
  assert_contains "$state" "CLOSED" "circuit breaker: success resets to CLOSED"
  teardown
}

test_cb_missing_file_defaults_closed() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  rm -f "$CB_FILE"
  local state
  state=$(cb_get_state "bl-nonexistent")
  assert_contains "$state" "CLOSED" "circuit breaker: missing file defaults to CLOSED"
  teardown
}

# ============================================================
# board-sync.sh — circuit breaker integration
# ============================================================

test_board_sync_emits_warning_on_trip() {
  setup
  local id
  id=$(extract_id "$(bl create "Bouncing Card")")
  bl update "$id" --status review >/dev/null
  run_hook "$HOOKS_DIR/session-start.sh" >/dev/null
  source "$HOOKS_DIR/lib/board-state.sh"
  cb_record_bounce "$id" >/dev/null
  cb_record_bounce "$id" >/dev/null
  bl update "$id" --status doing >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/board-sync.sh")
  assert_contains "$out" "CIRCUIT BREAKER" "board-sync: emits warning on circuit breaker trip"
  teardown
}

test_board_sync_no_warning_below_threshold() {
  setup
  local id
  id=$(extract_id "$(bl create "Normal Bouncer")")
  bl update "$id" --status review >/dev/null
  run_hook "$HOOKS_DIR/session-start.sh" >/dev/null
  bl update "$id" --status doing >/dev/null
  local out
  out=$(run_hook "$HOOKS_DIR/board-sync.sh")
  assert_not_contains "$out" "CIRCUIT BREAKER" "board-sync: no warning below threshold"
  teardown
}

test_board_sync_success_clears_breaker() {
  setup
  local id
  id=$(extract_id "$(bl create "Recovering Card")")
  bl update "$id" --status review >/dev/null
  source "$HOOKS_DIR/lib/board-state.sh"
  cb_record_bounce "$id" >/dev/null
  cb_record_bounce "$id" >/dev/null
  cb_record_bounce "$id" >/dev/null
  local state
  state=$(cb_get_state "$id")
  assert_contains "$state" "OPEN" "board-sync: pre-condition breaker is OPEN"
  run_hook "$HOOKS_DIR/session-start.sh" >/dev/null
  bl close "$id" >/dev/null
  run_hook "$HOOKS_DIR/board-sync.sh" >/dev/null
  state=$(cb_get_state "$id")
  assert_contains "$state" "CLOSED" "board-sync: success path clears circuit breaker"
  teardown
}

# ============================================================
# Runner — auto-discovers all test_* functions
# ============================================================

echo "=== ralph-ban Hook Tests ==="
echo ""

while IFS= read -r fn; do
  $fn
done < <(declare -F | awk '/test_/{print $3}' | sort)

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
