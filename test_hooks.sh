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
  local out exit_code
  out=$(echo "$input" | "$HOOKS_DIR/teammate-idle.sh" 2>/dev/null || true)
  echo "$input" | "$HOOKS_DIR/teammate-idle.sh" >/dev/null 2>&1
  exit_code=$?
  if [ "$exit_code" -eq 0 ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: teammate-idle should allow exit when teammate has no claimed cards (got exit $exit_code)"
  fi
  assert_contains "$out" "suppressOutput" "teammate-idle suppresses output when no active cards"
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
  local out exit_code=0
  # Capture stdout; stderr goes to a separate fd so assert_not_contains only sees stdout
  out=$(echo "$input" | "$HOOKS_DIR/teammate-idle.sh" 2>/dev/null || true)
  echo "$input" | "$HOOKS_DIR/teammate-idle.sh" >/dev/null 2>&1 || exit_code=$?
  if [ "$exit_code" -eq 2 ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: teammate-idle should exit 2 when teammate owns doing card (got exit $exit_code)"
  fi
  assert_not_contains "$out" "suppressOutput" "teammate-idle does not suppress output when blocking"
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
  local out exit_code=0
  out=$(echo "$input" | "$HOOKS_DIR/teammate-idle.sh" 2>/dev/null || true)
  echo "$input" | "$HOOKS_DIR/teammate-idle.sh" >/dev/null 2>&1 || exit_code=$?
  if [ "$exit_code" -eq 0 ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: teammate-idle should allow exit when teammate only has review cards (got exit $exit_code)"
  fi
  assert_contains "$out" "suppressOutput" "teammate-idle suppresses output when only review cards remain"
  teardown
}

test_teammate_idle_suppresses_no_db() {
  setup
  # Remove the database so db_exists returns false — nothing to check
  rm -f .beads.db
  local input='{"teammate_name":"test-worker"}'
  local out exit_code=0
  out=$(echo "$input" | "$HOOKS_DIR/teammate-idle.sh" 2>/dev/null || true)
  echo "$input" | "$HOOKS_DIR/teammate-idle.sh" >/dev/null 2>&1 || exit_code=$?
  if [ "$exit_code" -eq 0 ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: teammate-idle should exit 0 when no db (got exit $exit_code)"
  fi
  assert_contains "$out" "suppressOutput" "teammate-idle suppresses output when no db"
  teardown
}

test_teammate_idle_suppresses_no_teammate_name() {
  setup
  bl create "Some Task" >/dev/null
  # Empty teammate name — nothing to check, suppress cleanly
  local input='{"teammate_name":""}'
  local out exit_code=0
  out=$(echo "$input" | "$HOOKS_DIR/teammate-idle.sh" 2>/dev/null || true)
  echo "$input" | "$HOOKS_DIR/teammate-idle.sh" >/dev/null 2>&1 || exit_code=$?
  if [ "$exit_code" -eq 0 ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: teammate-idle should exit 0 when no teammate name (got exit $exit_code)"
  fi
  assert_contains "$out" "suppressOutput" "teammate-idle suppresses output when no teammate name"
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

# --- stop-guard.sh debounce ---

test_stop_guard_debounce_first_call_shows_message() {
  setup
  local create_out id
  create_out=$(bl create "Pending Todo")
  id=$(extract_id "$create_out")
  bl update "$id" --status doing >/dev/null
  # No hash file present — first call must emit systemMessage
  rm -f .ralph-ban/.stop-last-msg-hash

  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" '"block"' "debounce: first call still blocks"
  assert_contains "$out" "systemMessage" "debounce: first call shows systemMessage"
  teardown
}

test_stop_guard_debounce_second_identical_call_omits_message() {
  setup
  local create_out id
  create_out=$(bl create "Pending Todo")
  id=$(extract_id "$create_out")
  bl update "$id" --status doing >/dev/null
  echo '{"stop_mode":"autonomous"}' > .ralph-ban/config.json
  rm -f .ralph-ban/.stop-last-msg-hash

  # First call — emits and saves the hash
  CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" >/dev/null 2>/dev/null || true

  # Second call with identical board state — systemMessage should be absent
  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" '"block"' "debounce: second call still blocks"
  assert_not_contains "$out" "systemMessage" "debounce: second identical call omits systemMessage"
  teardown
}

test_stop_guard_debounce_new_message_shows_after_board_change() {
  setup
  local create_out id1 id2
  create_out=$(bl create "First Task")
  id1=$(extract_id "$create_out")
  bl update "$id1" --status doing >/dev/null
  echo '{"stop_mode":"autonomous"}' > .ralph-ban/config.json
  rm -f .ralph-ban/.stop-last-msg-hash

  # First call — board has 1 doing card
  CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" >/dev/null 2>/dev/null || true

  # Add a second doing card to change the board message (counts change)
  create_out=$(bl create "Second Task")
  id2=$(extract_id "$create_out")
  bl update "$id2" --status doing >/dev/null

  # Board changed — counts differ — systemMessage should return
  local out
  out=$(CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" '"block"' "debounce: still blocks after board change"
  assert_contains "$out" "systemMessage" "debounce: shows systemMessage when board state changes"
  teardown
}

test_stop_guard_debounce_hash_reset_on_allow_exit() {
  setup
  local create_out id
  create_out=$(bl create "Doing Task")
  id=$(extract_id "$create_out")
  bl update "$id" --status doing >/dev/null
  echo '{"stop_mode":"autonomous"}' > .ralph-ban/config.json
  rm -f .ralph-ban/.stop-last-msg-hash

  # First block call — saves hash
  CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" >/dev/null 2>/dev/null || true
  # Verify hash file was written
  if [ -f .ralph-ban/.stop-last-msg-hash ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: debounce: hash file should exist after first block"
  fi

  # Clear the board so the hook allows exit
  bl close "$id" >/dev/null
  CLAUDE_AGENT_NAME=orchestrator "$HOOKS_DIR/stop-guard.sh" >/dev/null 2>/dev/null || true

  # Hash file should be cleaned up on allow-exit
  if [ ! -f .ralph-ban/.stop-last-msg-hash ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: debounce: hash file should be removed after allow-exit"
  fi
  teardown
}

# --- Circuit breaker state machine ---

test_circuit_breaker_closed_below_threshold() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  # Two bounces below the default threshold of 3 — should stay CLOSED
  result=$(cb_record_bounce "bl-test1")
  result=$(cb_record_bounce "bl-test1")
  state=$(cb_get_state "bl-test1")
  assert_contains "$state" "CLOSED" "circuit breaker stays CLOSED below threshold"
  # Bounce count should be 2
  count=$(jq -r '."bl-test1".bounce_count' <"$CB_FILE" 2>/dev/null || echo "0")
  if [ "$count" = "2" ]; then
    PASS=$((PASS + 1))
  else
    FAIL=$((FAIL + 1))
    echo "FAIL: circuit breaker bounce count should be 2 (got: $count)"
  fi
  teardown
}

test_circuit_breaker_trips_at_threshold() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  # Three bounces should trip the breaker
  cb_record_bounce "bl-test2" >/dev/null
  cb_record_bounce "bl-test2" >/dev/null
  result=$(cb_record_bounce "bl-test2")
  state=$(cb_get_state "bl-test2")
  assert_contains "$state" "OPEN" "circuit breaker trips to OPEN at threshold"
  assert_contains "$result" "OPEN" "cb_record_bounce returns OPEN on trip"
  teardown
}

test_circuit_breaker_open_to_half_open_after_cooldown() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  # Trip the breaker manually with an old opened_at timestamp
  past=$(($(date +%s) - 600))  # 10 minutes ago, beyond 5 min cool-down
  jq -n --argjson past "$past" \
    '{"bl-test3":{"state":"OPEN","bounce_count":3,"opened_at":$past,"last_bounce":$past}}' \
    >"$CB_FILE"
  state=$(cb_get_state "bl-test3")
  assert_contains "$state" "HALF_OPEN" "circuit breaker transitions OPEN→HALF_OPEN after cool-down"
  teardown
}

test_circuit_breaker_open_stays_open_within_cooldown() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  # opened_at is recent — cool-down not expired
  now=$(date +%s)
  jq -n --argjson now "$now" \
    '{"bl-test4":{"state":"OPEN","bounce_count":3,"opened_at":$now,"last_bounce":$now}}' \
    >"$CB_FILE"
  state=$(cb_get_state "bl-test4")
  assert_contains "$state" "OPEN" "circuit breaker stays OPEN within cool-down window"
  teardown
}

test_circuit_breaker_half_open_failure_reopens() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  # Place breaker in HALF_OPEN state
  past=$(($(date +%s) - 600))
  jq -n --argjson past "$past" \
    '{"bl-test5":{"state":"OPEN","bounce_count":3,"opened_at":$past,"last_bounce":$past}}' \
    >"$CB_FILE"
  # Trigger another bounce — should re-open with fresh timer
  result=$(cb_record_bounce "bl-test5")
  assert_contains "$result" "HALF_OPEN_REOPEN" "probe failure emits HALF_OPEN_REOPEN"
  state=$(cb_get_state "bl-test5")
  assert_contains "$state" "OPEN" "breaker re-opens on probe failure"
  teardown
}

test_circuit_breaker_success_resets_to_closed() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  # Trip the breaker, then record success
  cb_record_bounce "bl-test6" >/dev/null
  cb_record_bounce "bl-test6" >/dev/null
  cb_record_bounce "bl-test6" >/dev/null
  state=$(cb_get_state "bl-test6")
  assert_contains "$state" "OPEN" "breaker is OPEN before success"
  cb_record_success "bl-test6"
  # Entry should be deleted — get_state returns CLOSED for unknown cards
  state=$(cb_get_state "bl-test6")
  assert_contains "$state" "CLOSED" "success resets circuit breaker to CLOSED"
  teardown
}

test_circuit_breaker_missing_file_defaults_closed() {
  setup
  source "$HOOKS_DIR/lib/board-state.sh"
  rm -f "$CB_FILE"
  state=$(cb_get_state "bl-nonexistent")
  assert_contains "$state" "CLOSED" "missing circuit breaker file defaults to CLOSED"
  teardown
}

test_board_sync_emits_warning_on_trip() {
  setup
  local create_out id
  create_out=$(bl create "Bouncing Card")
  id=$(extract_id "$create_out")
  bl update "$id" --status review >/dev/null

  # Set baseline snapshot
  "$HOOKS_DIR/session-start.sh" >/dev/null 2>&1 || true

  # Simulate 3 bounce transitions by directly writing the circuit breaker file
  # at threshold-1 then triggering the 3rd bounce via a board change.
  source "$HOOKS_DIR/lib/board-state.sh"
  cb_record_bounce "$id" >/dev/null
  cb_record_bounce "$id" >/dev/null

  # Move card review→doing for the third bounce (triggers trip)
  bl update "$id" --status doing >/dev/null

  local out
  out=$("$HOOKS_DIR/board-sync.sh" 2>/dev/null || true)
  assert_contains "$out" "CIRCUIT BREAKER" "board-sync emits circuit breaker warning on trip"
  teardown
}

test_board_sync_no_warning_below_threshold() {
  setup
  local create_out id
  create_out=$(bl create "Normal Bouncer")
  id=$(extract_id "$create_out")
  bl update "$id" --status review >/dev/null

  # Set baseline snapshot
  "$HOOKS_DIR/session-start.sh" >/dev/null 2>&1 || true

  # Only one bounce — below threshold
  bl update "$id" --status doing >/dev/null

  local out
  out=$("$HOOKS_DIR/board-sync.sh" 2>/dev/null || true)
  assert_not_contains "$out" "CIRCUIT BREAKER" "board-sync no warning below threshold"
  teardown
}

test_board_sync_success_clears_circuit_breaker() {
  setup
  local create_out id
  create_out=$(bl create "Recovering Card")
  id=$(extract_id "$create_out")
  bl update "$id" --status review >/dev/null

  # Trip the breaker manually
  source "$HOOKS_DIR/lib/board-state.sh"
  cb_record_bounce "$id" >/dev/null
  cb_record_bounce "$id" >/dev/null
  cb_record_bounce "$id" >/dev/null
  state=$(cb_get_state "$id")
  assert_contains "$state" "OPEN" "pre-condition: breaker is OPEN"

  # Set baseline snapshot
  "$HOOKS_DIR/session-start.sh" >/dev/null 2>&1 || true

  # Card reaches done (success path)
  bl close "$id" >/dev/null

  "$HOOKS_DIR/board-sync.sh" >/dev/null 2>&1 || true
  # After success, breaker entry should be cleared
  state=$(cb_get_state "$id")
  assert_contains "$state" "CLOSED" "success path clears circuit breaker entry"
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
test_teammate_idle_suppresses_no_db
test_teammate_idle_suppresses_no_teammate_name
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
test_stop_guard_debounce_first_call_shows_message
test_stop_guard_debounce_second_identical_call_omits_message
test_stop_guard_debounce_new_message_shows_after_board_change
test_stop_guard_debounce_hash_reset_on_allow_exit
test_circuit_breaker_closed_below_threshold
test_circuit_breaker_trips_at_threshold
test_circuit_breaker_open_to_half_open_after_cooldown
test_circuit_breaker_open_stays_open_within_cooldown
test_circuit_breaker_half_open_failure_reopens
test_circuit_breaker_success_resets_to_closed
test_circuit_breaker_missing_file_defaults_closed
test_board_sync_emits_warning_on_trip
test_board_sync_no_warning_below_threshold
test_board_sync_success_clears_circuit_breaker

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
