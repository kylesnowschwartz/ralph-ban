#!/usr/bin/env bash
# Hook tests for ralph-ban.
# Run from ralph-ban root: bash test_hooks.sh
#
# Tests are auto-discovered: any function named test_* runs automatically.
# No manual test list to maintain.
set -eo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HOOKS_DIR="$SCRIPT_DIR/hooks"

# Prefer a freshly-built bl from sibling beads-lite source so tests track
# current behavior, never a stale `/usr/local/bin/bl` left over from a
# prior install. In worktrees or CI without the sibling clone, fall back
# to the installed binary (which may be stale — flagged with a notice).
if [ -z "${BL:-}" ]; then
  if [ -d "$SCRIPT_DIR/../beads-lite" ]; then
    BL="${TMPDIR:-/tmp}/ralph-ban-test-bin/bl"
    mkdir -p "$(dirname "$BL")"
    echo "Building beads-lite from source..."
    (cd "$SCRIPT_DIR/../beads-lite" && go build -o "$BL" ./cmd/bl)
  else
    BL="/usr/local/bin/bl"
    echo "Note: beads-lite source not at $SCRIPT_DIR/../beads-lite; using installed $BL (may be stale)"
  fi
fi
export BL
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

# Invoke a hook that reads stdin (stop-guard).
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
  assert_contains "$out" "todo" "session-start: mentions todo items"
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
# board-state.sh library functions
# ============================================================

test_board_state_read_board() {
  setup
  bl create "Read Test" >/dev/null
  # shellcheck source=hooks/lib/board-state.sh
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
  # shellcheck source=hooks/lib/board-state.sh
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
  # Batch mode blocks on doing cards but doesn't append "Autonomous mode" trailer
  assert_not_contains "$out" "Autonomous mode" "stop-guard batch: explicit config does not report autonomous"
  teardown
}

test_stop_guard_autonomous_blocks_todo() {
  setup
  bl create "Pending Todo" >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "stop-guard autonomous: blocks on todo cards"
  assert_contains "$out" "Autonomous mode" "stop-guard autonomous: reports mode"
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
# stop-guard.sh — agent activity gate (Phase 4.5, role-scoped)
# ============================================================

# Phase 4.5 must silence the stop hook whenever ANY role on ANY non-done card
# is in state 'running'. The schema's role-scoped assignments table makes
# reviewers and oracles visible to bl, so the same query catches all three
# agent types uniformly.

test_stop_guard_phase45_silences_for_running_reviewer() {
  setup
  local id
  id=$(extract_id "$(bl create "Reviewer In Flight")")
  bl update "$id" --status doing >/dev/null
  bl claim "$id" --role reviewer --agent rb-reviewer >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json

  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard 4.5: running reviewer suppresses block"
  assert_contains "$out" "Agents running" "stop-guard 4.5: emits running-agents pause message"
  teardown
}

test_stop_guard_phase45_silences_for_running_oracle() {
  setup
  local id
  id=$(extract_id "$(bl create "Oracle In Flight")")
  bl update "$id" --status doing >/dev/null
  bl claim "$id" --role oracle --agent rb-oracle >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json

  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard 4.5: running oracle suppresses block"
  teardown
}

test_stop_guard_phase45_silences_for_running_worker_via_assignments() {
  setup
  local id
  id=$(extract_id "$(bl create "Worker In Flight")")
  bl claim "$id" --agent rb-worker >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json

  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard 4.5: running worker (via assignments) suppresses block"
  teardown
}

test_stop_guard_phase45_blocks_when_all_assignments_done() {
  setup
  local id
  id=$(extract_id "$(bl create "All Done")")
  bl update "$id" --status doing >/dev/null
  bl claim "$id" --role reviewer --agent rb-reviewer >/dev/null
  bl agent-state "$id" --role reviewer --state "done" >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json

  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "stop-guard 4.5: completed assignments do not suppress legitimate block"
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
  assert_contains "$out" "Autonomous mode" "stop-guard anti-loop: reports autonomous mode"
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
  # shellcheck source=hooks/lib/board-state.sh
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
  # shellcheck source=hooks/lib/board-state.sh
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
  assert_contains "$out" "no active agent" "stop-guard guidance: highlights unclaimed doing card"
  assert_contains "$out" '"block"' "stop-guard guidance: blocks with unclaimed doing guidance"
  teardown
}

test_stop_guard_guidance_waiting_for_workers() {
  setup
  local id
  id=$(extract_id "$(bl create "Dispatched Task")")
  bl update "$id" --status doing >/dev/null
  bl claim "$id" --agent test-worker >/dev/null
  bl agent-state "$id" --state running >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  # Phase 4.5 now detects agent_state=running and passes through silently
  # (same as the worker marker). Workers are in-flight — blocking just
  # produces empty acknowledgments the orchestrator can't act on.
  assert_not_contains "$out" '"block"' "stop-guard guidance: does not block when workers running"
  assert_contains "$out" "Agents running" "stop-guard guidance: shows waiting message for running agents"
  teardown
}

test_stop_guard_epic_excluded_from_counts() {
  setup
  # An epic in todo should NOT trigger a stop block in autonomous mode.
  # Epics are organizational containers — they close when children complete,
  # not when the orchestrator dispatches them.
  local epic_id
  epic_id=$(extract_id "$(bl create "My Epic" --type epic)")
  # Epic stays in todo. No non-epic cards exist.
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard: epic in todo does not block autonomous mode"
  teardown
}

test_stop_guard_epic_with_doing_child() {
  setup
  # An epic in todo + a child task in doing with a running agent.
  # The epic shouldn't add to the count, and the running agent should
  # trigger Phase 4.5 pass-through.
  local epic_id child_id
  epic_id=$(extract_id "$(bl create "My Epic" --type epic)")
  child_id=$(extract_id "$(bl create "Child Task" --epic "$epic_id")")
  bl update "$child_id" --status doing >/dev/null
  bl claim "$child_id" --agent test-worker >/dev/null
  bl agent-state "$child_id" --state running >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_not_contains "$out" '"block"' "stop-guard: epic+running child does not block"
  assert_contains "$out" "Agents running" "stop-guard: running child detected via assignments"
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

test_stop_guard_debounce_stall_counter_defeats() {
  setup
  local id
  id=$(extract_id "$(bl create "Doing Task")")
  bl update "$id" --status doing >/dev/null
  echo '{"stop_mode":"autonomous"}' >.ralph-ban/config.json
  rm -f .ralph-ban/.stop-last-msg-hash
  # First call emits and saves hash
  run_hook "$HOOKS_DIR/stop-guard.sh" >/dev/null
  # Second call — stall counter increments, changing the message hash.
  # This is by design: each stop attempt should show full guidance so
  # the agent always sees how many attempts remain.
  local out
  out=$(run_hook "$HOOKS_DIR/stop-guard.sh")
  assert_contains "$out" '"block"' "debounce: second call still blocks"
  assert_contains "$out" "systemMessage" "debounce: stall counter defeats debounce"
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
  # shellcheck source=hooks/lib/board-state.sh
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
  # shellcheck source=hooks/lib/board-state.sh
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
  # shellcheck source=hooks/lib/board-state.sh
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
  # shellcheck source=hooks/lib/board-state.sh
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
  # shellcheck source=hooks/lib/board-state.sh
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
  # shellcheck source=hooks/lib/board-state.sh
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
  # shellcheck source=hooks/lib/board-state.sh
  source "$HOOKS_DIR/lib/board-state.sh"
  rm -f "$CB_FILE"
  local state
  state=$(cb_get_state "bl-nonexistent")
  assert_contains "$state" "CLOSED" "circuit breaker: missing file defaults to CLOSED"
  teardown
}

# ============================================================
# prevent-out-of-worktree-write.sh
# ============================================================
#
# The hook reads JSON on stdin and either exits 0 silently (allow) or
# emits a deny payload with permissionDecision: "deny". We test the
# observable contract: stdout content, not exit code semantics, since
# the hook always exits 0 (per PreToolUse contract — JSON drives the
# decision).

POOWW="$HOOKS_DIR/prevent-out-of-worktree-write.sh"

# Build a PreToolUse input JSON for Edit/Write-style tools.
poow_input() {
  local cwd="$1" tool="$2" path="$3"
  jq -n \
    --arg cwd "$cwd" \
    --arg tool "$tool" \
    --arg path "$path" \
    '{
      cwd: $cwd,
      tool_name: $tool,
      tool_input: { file_path: $path }
    }'
}

# Same shape but for NotebookEdit (uses notebook_path).
poow_input_notebook() {
  local cwd="$1" path="$2"
  jq -n \
    --arg cwd "$cwd" \
    --arg path "$path" \
    '{
      cwd: $cwd,
      tool_name: "NotebookEdit",
      tool_input: { notebook_path: $path }
    }'
}

test_poow_noop_outside_worktree() {
  setup
  local out
  out=$(run_hook_with_input "$(poow_input /Users/kyle/Code/proj Edit /Users/kyle/Code/proj/foo.go)" "$POOWW")
  assert_eq "$out" "" "poow: no output (allow) when CWD outside worktree"
  teardown
}

test_poow_allows_relative_path_in_worktree() {
  setup
  local cwd="/Users/x/proj/.claude/worktrees/agent-aaa"
  local out
  out=$(run_hook_with_input "$(poow_input "$cwd" Edit server/ws.ts)" "$POOWW")
  assert_eq "$out" "" "poow: relative path inside worktree is allowed (no output)"
  teardown
}

test_poow_allows_absolute_inside_worktree() {
  setup
  local cwd="/Users/x/proj/.claude/worktrees/agent-aaa"
  local out
  out=$(run_hook_with_input "$(poow_input "$cwd" Write "$cwd/server/ws.ts")" "$POOWW")
  assert_eq "$out" "" "poow: absolute path inside worktree is allowed"
  teardown
}

test_poow_denies_absolute_to_main() {
  setup
  local cwd="/Users/x/proj/.claude/worktrees/agent-aaa"
  local out
  out=$(run_hook_with_input "$(poow_input "$cwd" Edit /Users/x/proj/server/ws.ts)" "$POOWW")
  assert_contains "$out" "permissionDecision" "poow: deny payload emitted for absolute-to-main"
  assert_contains "$out" "deny" "poow: deny decision present"
  assert_contains "$out" "outside worktree" "poow: deny reason names containment failure"
  teardown
}

test_poow_denies_sibling_worktree() {
  setup
  local cwd="/Users/x/proj/.claude/worktrees/agent-aaa"
  local sibling="/Users/x/proj/.claude/worktrees/agent-bbb/server/ws.ts"
  local out
  out=$(run_hook_with_input "$(poow_input "$cwd" Edit "$sibling")" "$POOWW")
  assert_contains "$out" "deny" "poow: writes into a sibling worktree are denied"
  teardown
}

test_poow_denies_dotdot_traversal() {
  setup
  local cwd="/Users/x/proj/.claude/worktrees/agent-aaa"
  local out
  out=$(run_hook_with_input "$(poow_input "$cwd" Edit "$cwd/../../../server/ws.ts")" "$POOWW")
  assert_contains "$out" "deny" "poow: '..' traversal in path is denied"
  assert_contains "$out" "\.\." "poow: deny reason calls out the .. token"
  teardown
}

test_poow_handles_cwd_in_worktree_subdir() {
  setup
  # Worker may cd into a subdirectory of its worktree (e.g., server/).
  # Containment must be against the worktree root, not the subdir.
  local cwd="/Users/x/proj/.claude/worktrees/agent-aaa/server"
  local out
  out=$(run_hook_with_input "$(poow_input "$cwd" Edit "/Users/x/proj/.claude/worktrees/agent-aaa/tests/foo.test.ts")" "$POOWW")
  assert_eq "$out" "" "poow: peer dir of cwd inside same worktree is allowed"
  teardown
}

test_poow_denies_notebook_path_to_main() {
  setup
  local cwd="/Users/x/proj/.claude/worktrees/agent-aaa"
  local out
  out=$(run_hook_with_input "$(poow_input_notebook "$cwd" /Users/x/proj/notebooks/main.ipynb)" "$POOWW")
  assert_contains "$out" "deny" "poow: NotebookEdit absolute-to-main denied via notebook_path"
  teardown
}

test_poow_fails_open_on_malformed_json() {
  setup
  local out
  out=$(run_hook_with_input "not json {{" "$POOWW")
  # Malformed JSON: jq returns empty for missing fields, the hook
  # treats empty cwd/target as "nothing to validate" and allows.
  assert_eq "$out" "" "poow: malformed JSON does not block (fail open)"
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
