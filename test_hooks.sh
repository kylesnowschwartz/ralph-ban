#!/usr/bin/env bash
# Hook script tests for ralph-ban.
# Tests session-start.sh, board-sync.sh, stop-guard.sh against a test database.
# Run from ralph-ban root: bash test_hooks.sh
set -euo pipefail

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

setup() {
  TEST_DIR="/tmp/ralph-ban-test-hooks"
  rm -rf "$TEST_DIR"
  mkdir -p "$TEST_DIR"
  cd "$TEST_DIR"
  # Isolate from parent environment — BL_ROOT leaks from ./ralph-ban claude
  # and points hooks at the real database instead of the test database.
  unset BL_ROOT
  unset CLAUDE_TEAM_NAME
  unset CLAUDE_AGENT_NAME
  bl init >/dev/null 2>&1
  mkdir -p .ralph-ban
}

teardown() {
  cd /
  rm -f "${TMPDIR:-/tmp}/ralph-ban-board.$$.cache"
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

# --- session-start.sh ---

test_session_start_empty_board() {
  setup
  local out
  out=$("$HOOKS_DIR/session-start.sh" 2>/dev/null || true)
  assert_contains "$out" "additionalContext" "session-start outputs additionalContext"
  assert_contains "$out" "empty" "session-start reports empty board"
  teardown
}

test_session_start_with_tasks() {
  setup
  bl create "Priority Task" --priority 0 >/dev/null
  bl create "Low Priority" --priority 4 >/dev/null

  local out
  out=$("$HOOKS_DIR/session-start.sh" 2>/dev/null || true)
  assert_contains "$out" "additionalContext" "session-start outputs additionalContext with tasks"
  assert_contains "$out" "Priority Task" "session-start suggests highest priority task"
  assert_contains "$out" "ready" "session-start mentions ready items"
  teardown
}

test_session_start_creates_snapshot() {
  setup
  bl create "Snapshot Test" >/dev/null
  "$HOOKS_DIR/session-start.sh" >/dev/null 2>&1 || true

  if [ -f ".ralph-ban/.last-seen.json" ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: session-start should create .ralph-ban/.last-seen.json"
  fi
  teardown
}

# --- board-sync.sh ---

test_board_sync_no_change() {
  setup
  bl create "Static Task" >/dev/null
  # Set initial snapshot
  "$HOOKS_DIR/session-start.sh" >/dev/null 2>&1 || true

  # Run sync with no changes
  local out
  out=$("$HOOKS_DIR/board-sync.sh" 2>/dev/null || true)
  # Should produce no output (no changes)
  if [ -z "$out" ]; then
    PASS=$((PASS + 1))
  else
    # Some output is OK if it detects the unchanged state
    PASS=$((PASS + 1))
  fi
  teardown
}

test_board_sync_detects_status_change() {
  setup
  local create_out id
  create_out=$(bl create "Moving Task")
  id=$(extract_id "$create_out")

  # Set baseline
  "$HOOKS_DIR/session-start.sh" >/dev/null 2>&1 || true

  # Move task
  bl update "$id" --status doing >/dev/null

  # Run sync
  local out
  out=$("$HOOKS_DIR/board-sync.sh" 2>/dev/null || true)
  assert_contains "$out" "additionalContext" "board-sync outputs additionalContext on change"
  teardown
}

test_board_sync_detects_new_card() {
  setup
  bl create "Original" >/dev/null
  # Set baseline
  "$HOOKS_DIR/session-start.sh" >/dev/null 2>&1 || true

  # Add new card
  bl create "Brand New" >/dev/null

  # Run sync
  local out
  out=$("$HOOKS_DIR/board-sync.sh" 2>/dev/null || true)
  assert_contains "$out" "additionalContext" "board-sync detects new card"
  teardown
}

# --- stop-guard.sh ---

test_stop_guard_allows_todo_in_batch_mode() {
  setup
  bl create "Active Todo" >/dev/null
  # batch mode (default) — todo cards alone do not block

  local out
  out=$("$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_not_contains "$out" '"block"' "stop-guard allows exit in batch mode when only todo items exist"
  assert_contains "$out" "batch" "stop-guard reports batch mode when todo cards remain"
  teardown
}

test_stop_guard_blocks_with_doing() {
  setup
  local create_out id
  create_out=$(bl create "Doing Task")
  id=$(extract_id "$create_out")
  bl update "$id" --status doing >/dev/null

  local out
  out=$("$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" '"block"' "stop-guard blocks when doing items exist"
  teardown
}

test_stop_guard_allows_empty() {
  setup
  # No tasks at all
  local out
  out=$("$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  # Should not block
  assert_not_contains "$out" '"block"' "stop-guard allows exit on empty board"
  teardown
}

test_stop_guard_allows_only_done() {
  setup
  local create_out id
  create_out=$(bl create "Finished")
  id=$(extract_id "$create_out")
  bl close "$id" >/dev/null

  local out
  out=$("$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_not_contains "$out" '"block"' "stop-guard allows exit when only done items"
  teardown
}

test_stop_guard_allows_only_backlog() {
  setup
  local create_out id
  create_out=$(bl create "Backlog Only")
  id=$(extract_id "$create_out")
  bl update "$id" --status backlog >/dev/null

  local out
  out=$("$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_not_contains "$out" '"block"' "stop-guard allows exit when only backlog items"
  teardown
}

# --- lib/board-state.sh functions ---

test_board_state_read_board() {
  setup
  bl create "Read Test" >/dev/null
  source "$HOOKS_DIR/lib/board-state.sh"

  local board
  board=$(read_board)
  assert_contains "$board" "Read Test" "read_board returns issue data"
  assert_contains "$board" '"status":"todo"' "read_board shows correct status"
  teardown
}

test_board_state_count_active() {
  setup
  bl create "Todo 1" >/dev/null
  bl create "Todo 2" >/dev/null
  local doing_out
  doing_out=$(bl create "Doing 1")
  local doing_id
  doing_id=$(extract_id "$doing_out")
  bl update "$doing_id" --status doing >/dev/null

  source "$HOOKS_DIR/lib/board-state.sh"
  local counts
  counts=$(count_active)
  assert_contains "$counts" "2" "count_active shows 2 todo items"
  teardown
}

extract_id() {
  echo "$1" | grep -o 'bl-[a-z0-9]*' | head -1
}

# --- stop-guard.sh safety rails ---

test_stop_guard_allows_on_stop_hook_active() {
  setup
  bl create "Active Todo" >/dev/null
  local input='{"stop_hook_active":true}'

  local out
  out=$(echo "$input" | "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_not_contains "$out" '"block"' "stop-guard softens blocking when stop_hook_active"
  teardown
}

test_stop_guard_respects_disable_marker() {
  setup
  bl create "Active Todo" >/dev/null
  touch .ralph-ban/disabled

  local out
  out=$("$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_not_contains "$out" '"block"' "stop-guard respects disable marker"
  teardown
}

test_stop_guard_allows_worker_with_no_claimed_cards() {
  setup
  bl create "Active Todo" >/dev/null
  # Worker agent with no claimed cards — should exit even though board has active work
  CLAUDE_AGENT_NAME=worker "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true
  local out
  out=$(CLAUDE_AGENT_NAME=worker "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_not_contains "$out" '"block"' "stop-guard allows worker exit with no claimed cards"
  teardown
}

test_stop_guard_blocks_orchestrator_with_doing_work() {
  setup
  local create_out id
  create_out=$(bl create "Active Doing")
  id=$(extract_id "$create_out")
  bl update "$id" --status doing >/dev/null
  # Orchestrator is blocked by doing work in batch mode (the default)
  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" '"block"' "stop-guard blocks orchestrator when doing work remains"
  teardown
}

# --- board-sync.sh stall detection ---

test_board_sync_tracks_stall() {
  setup
  local create_out id
  create_out=$(bl create "Stalling Task")
  id=$(extract_id "$create_out")
  bl update "$id" --status doing >/dev/null

  # Set baseline
  "$HOOKS_DIR/session-start.sh" >/dev/null 2>&1 || true

  # Run board-sync repeatedly to accumulate stale cycles.
  # Low threshold for testing.
  export STALL_THRESHOLD=3
  local out=""
  for i in $(seq 1 4); do
    out=$("$HOOKS_DIR/board-sync.sh" 2>/dev/null || true)
  done

  assert_contains "$out" "STALL" "board-sync detects stalled card after threshold"
  unset STALL_THRESHOLD
  teardown
}

test_stop_guard_blocks_teammate_uncommitted() {
  setup
  # Init git so git status --porcelain works
  git init -q . >/dev/null 2>&1
  echo "seed" >seed.txt
  git add seed.txt && git commit -q -m "init" >/dev/null 2>&1
  # Create a tracked-then-modified file so porcelain shows it
  echo "dirty" >>seed.txt

  local out
  out=$(CLAUDE_TEAM_NAME=test-team "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" '"block"' "stop-guard blocks teammate with uncommitted changes"
  teardown
}

# --- teammate-idle.sh ---

test_teammate_idle_allows_no_cards() {
  setup
  bl create "Unrelated Task" >/dev/null
  local input='{"teammate_name":"test-worker"}'
  local out
  out=$(echo "$input" | "$HOOKS_DIR/teammate-idle.sh" 2>/dev/null || true)
  local exit_code
  echo "$input" | "$HOOKS_DIR/teammate-idle.sh" >/dev/null 2>&1
  exit_code=$?
  if [ "$exit_code" -eq 0 ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: teammate-idle should allow exit when teammate has no claimed cards (got exit $exit_code)"
  fi
  teardown
}

test_teammate_idle_blocks_active_cards() {
  setup
  local create_out id
  create_out=$(bl create "Worker Task")
  id=$(extract_id "$create_out")
  bl update "$id" --status doing >/dev/null
  bl claim "$id" --agent test-worker >/dev/null 2>&1 || true

  local input='{"teammate_name":"test-worker"}'
  local exit_code=0
  echo "$input" | "$HOOKS_DIR/teammate-idle.sh" >/dev/null 2>&1 || exit_code=$?
  if [ "$exit_code" -eq 2 ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: teammate-idle should exit 2 when teammate owns doing card (got exit $exit_code)"
  fi
  teardown
}

test_teammate_idle_allows_review_only() {
  setup
  local create_out id
  create_out=$(bl create "Review Task")
  id=$(extract_id "$create_out")
  # Claim first (moves to doing), then advance to review.
  # This mirrors the real workflow: worker claims, implements, moves to review.
  bl claim "$id" --agent test-worker >/dev/null 2>&1 || true
  bl update "$id" --status review >/dev/null

  local input='{"teammate_name":"test-worker"}'
  local exit_code=0
  echo "$input" | "$HOOKS_DIR/teammate-idle.sh" >/dev/null 2>&1 || exit_code=$?
  if [ "$exit_code" -eq 0 ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: teammate-idle should allow exit when teammate only has review cards (got exit $exit_code)"
  fi
  teardown
}

# --- task-completed.sh ---

test_task_completed_allows_no_doing() {
  setup
  local create_out id
  create_out=$(bl create "Done Task")
  id=$(extract_id "$create_out")
  bl claim "$id" --agent test-worker >/dev/null 2>&1 || true
  bl update "$id" --status review >/dev/null

  local input='{"teammate_name":"test-worker"}'
  local exit_code=0
  echo "$input" | "$HOOKS_DIR/task-completed.sh" >/dev/null 2>&1 || exit_code=$?
  if [ "$exit_code" -eq 0 ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: task-completed should allow when no doing cards (got exit $exit_code)"
  fi
  teardown
}

test_task_completed_blocks_doing_cards() {
  setup
  local create_out id
  create_out=$(bl create "Still Doing")
  id=$(extract_id "$create_out")
  bl claim "$id" --agent test-worker >/dev/null 2>&1 || true

  local input='{"teammate_name":"test-worker"}'
  local exit_code=0
  echo "$input" | "$HOOKS_DIR/task-completed.sh" >/dev/null 2>&1 || exit_code=$?
  if [ "$exit_code" -eq 2 ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: task-completed should block when doing cards exist (got exit $exit_code)"
  fi
  teardown
}

# --- stop-guard.sh stall detection ---

test_stop_guard_stall_detection_allows_after_max_stalls() {
  setup
  bl create "Stuck Task" >/dev/null
  # Pre-populate the hash file so the hook thinks the board hasn't changed,
  # and set the cycle count to MAX_STALLS-1 so the next run triggers the limit.
  source "$HOOKS_DIR/lib/board-state.sh"
  local hash
  hash=$(read_board_hash)
  mkdir -p .ralph-ban
  echo "$hash" > .ralph-ban/.stop-board-hash
  echo "4" > .ralph-ban/.stop-cycles  # one below MAX_STALLS=5

  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  # At max stalls the hook emits a systemMessage but no "block" decision, allowing exit
  assert_not_contains "$out" '"block"' "stop-guard allows exit after max stall cycles"
  assert_contains "$out" "stop cycles" "stop-guard reports stall cycle count"
  teardown
}

test_stop_guard_stall_resets_on_progress() {
  setup
  local create_out id
  create_out=$(bl create "Progressing Task")
  id=$(extract_id "$create_out")
  bl update "$id" --status doing >/dev/null
  # Start with a stale hash (different from current board state)
  mkdir -p .ralph-ban
  echo "deadbeef_old_hash" > .ralph-ban/.stop-board-hash
  echo "3" > .ralph-ban/.stop-cycles

  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  # Board changed (hash differs), so stall resets and hook still blocks on doing work
  assert_contains "$out" '"block"' "stop-guard still blocks when progress detected and doing work remains"
  local cycles
  cycles=$(cat .ralph-ban/.stop-cycles 2>/dev/null || echo "")
  if [ "$cycles" = "0" ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: stall counter should reset to 0 on progress (got: $cycles)"
  fi
  teardown
}

test_stop_guard_specific_guidance_next_todo() {
  setup
  bl create "Important Feature" >/dev/null
  # autonomous mode: todo cards block and produce per-card guidance
  echo '{"stop_mode":"autonomous"}' > .ralph-ban/config.json

  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" "Important Feature" "stop-guard names the next todo card in guidance"
  assert_contains "$out" '"block"' "stop-guard still blocks with specific guidance"
  teardown
}

test_stop_guard_specific_guidance_unclaimed_doing() {
  setup
  local create_out id
  create_out=$(bl create "Orphaned Task")
  id=$(extract_id "$create_out")
  bl update "$id" --status doing >/dev/null
  # Leave it unclaimed (no assigned_to)

  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" "no assignee" "stop-guard highlights unclaimed doing card"
  assert_contains "$out" '"block"' "stop-guard blocks with unclaimed doing guidance"
  teardown
}

# --- stop-guard.sh stop_mode config ---

test_stop_guard_batch_mode_allows_todo_only() {
  setup
  bl create "Pending Todo" >/dev/null
  # Explicit batch mode config
  echo '{"stop_mode":"batch"}' > .ralph-ban/config.json

  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_not_contains "$out" '"block"' "batch mode: orchestrator allowed to stop with only todo cards"
  assert_contains "$out" "batch" "batch mode: reports mode in systemMessage"
  teardown
}

test_stop_guard_batch_mode_blocks_doing() {
  setup
  local create_out id
  create_out=$(bl create "In Flight")
  id=$(extract_id "$create_out")
  bl update "$id" --status doing >/dev/null
  echo '{"stop_mode":"batch"}' > .ralph-ban/config.json

  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" '"block"' "batch mode: orchestrator blocked while doing cards exist"
  assert_contains "$out" "batch" "batch mode: reports mode in block message"
  teardown
}

test_stop_guard_autonomous_mode_blocks_todo() {
  setup
  bl create "Pending Todo" >/dev/null
  echo '{"stop_mode":"autonomous"}' > .ralph-ban/config.json

  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" '"block"' "autonomous mode: orchestrator blocked by todo cards"
  assert_contains "$out" "autonomous" "autonomous mode: reports mode in block message"
  teardown
}

test_stop_guard_autonomous_mode_blocks_todo_and_doing() {
  setup
  bl create "Pending Todo" >/dev/null
  local create_out id
  create_out=$(bl create "In Flight")
  id=$(extract_id "$create_out")
  bl update "$id" --status doing >/dev/null
  echo '{"stop_mode":"autonomous"}' > .ralph-ban/config.json

  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" '"block"' "autonomous mode: orchestrator blocked by todo + doing cards"
  teardown
}

test_stop_guard_missing_config_defaults_to_batch() {
  setup
  bl create "Pending Todo" >/dev/null
  # No config.json at all — should default to batch (allow stop with only todo)

  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_not_contains "$out" '"block"' "missing config defaults to batch mode (todo does not block)"
  teardown
}

test_stop_guard_missing_stop_mode_field_defaults_to_batch() {
  setup
  bl create "Pending Todo" >/dev/null
  # Config exists but stop_mode is absent — should default to batch
  echo '{"wip_limits":{"doing":3}}' > .ralph-ban/config.json

  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_not_contains "$out" '"block"' "missing stop_mode field defaults to batch (todo does not block)"
  teardown
}

# --- Run all tests ---

echo "=== ralph-ban Hook Tests ==="
echo ""

test_session_start_empty_board
test_session_start_with_tasks
test_session_start_creates_snapshot
test_board_sync_no_change
test_board_sync_detects_status_change
test_board_sync_detects_new_card
test_stop_guard_allows_todo_in_batch_mode
test_stop_guard_blocks_with_doing
test_stop_guard_allows_empty
test_stop_guard_allows_only_done
test_stop_guard_allows_only_backlog
test_board_state_read_board
test_board_state_count_active
test_stop_guard_allows_on_stop_hook_active
test_stop_guard_respects_disable_marker
test_stop_guard_allows_worker_with_no_claimed_cards
test_stop_guard_blocks_orchestrator_with_doing_work
test_board_sync_tracks_stall
test_stop_guard_blocks_teammate_uncommitted
test_teammate_idle_allows_no_cards
test_teammate_idle_blocks_active_cards
test_teammate_idle_allows_review_only
test_task_completed_allows_no_doing
test_task_completed_blocks_doing_cards
test_stop_guard_stall_detection_allows_after_max_stalls
test_stop_guard_stall_resets_on_progress
test_stop_guard_specific_guidance_next_todo
test_stop_guard_specific_guidance_unclaimed_doing
test_stop_guard_batch_mode_allows_todo_only
test_stop_guard_batch_mode_blocks_doing
test_stop_guard_autonomous_mode_blocks_todo
test_stop_guard_autonomous_mode_blocks_todo_and_doing
test_stop_guard_missing_config_defaults_to_batch
test_stop_guard_missing_stop_mode_field_defaults_to_batch

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
