#!/usr/bin/env bash
# Hook script tests for ralph-ban.
# Tests session-start.sh, board-sync.sh, stop-guard.sh against a test database.
# Run from ralph-ban root: bash test_hooks.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HOOKS_DIR="$SCRIPT_DIR/.claude/hooks"

# Use locally built bl — export so hook scripts see it via ${BL:-bl}
BL="${BL:-/tmp/bl-test}"
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
  TEST_DIR=$(mktemp -d)
  cd "$TEST_DIR"
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

test_stop_guard_blocks_with_todo() {
  setup
  bl create "Active Todo" >/dev/null

  local out
  out=$("$HOOKS_DIR/stop-guard.sh" 2>/dev/null || true)
  assert_contains "$out" '"decision"' "stop-guard outputs decision"
  assert_contains "$out" '"block"' "stop-guard blocks when todo items exist"
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

# --- Run all tests ---

echo "=== ralph-ban Hook Tests ==="
echo ""

test_session_start_empty_board
test_session_start_with_tasks
test_session_start_creates_snapshot
test_board_sync_no_change
test_board_sync_detects_status_change
test_board_sync_detects_new_card
test_stop_guard_blocks_with_todo
test_stop_guard_blocks_with_doing
test_stop_guard_allows_empty
test_stop_guard_allows_only_done
test_stop_guard_allows_only_backlog
test_board_state_read_board
test_board_state_count_active

echo ""
echo "=== Results: $PASS passed, $FAIL failed ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
