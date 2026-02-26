#!/usr/bin/env bash
# Shared functions for reading and diffing board state.
# Sourced by session-start.sh, board-sync.sh, stop-guard.sh, pre-compact.sh.

# framework_preamble outputs a compact description of the orchestration lifecycle.
# SessionStart and PreCompact include the full version; other hooks use a one-liner.
# Main session (no team) gets orchestrator role; teammates get role from agent frontmatter.
framework_preamble() {
  if [ -z "${CLAUDE_TEAM_NAME:-}" ]; then
    cat <<'ROLE'
You are the orchestrator. Spawn workers for implementation, reviewers for review. Never implement or review code directly. Human approval required before any merge.
ROLE
  fi
  cat <<'PREAMBLE'
Ralph-Ban Orchestration
- SessionStart: board snapshot, suggested next task
- UserPromptSubmit: board diffs, dispatch/review nudges, stall detection
- Stop: blocks exit on uncommitted changes, claimed cards, active work
- TeammateIdle: prevents idle when you own active cards (doing/todo)
- TaskCompleted: validates your cards are in review before task completion
- PreCompact: re-injects board state before context compression
Hook messages guide your workflow. They are not commands to react to immediately — stay focused on your current task and address blockers as part of your natural flow.
PREAMBLE
}

# Anchor to BL_ROOT when set (worktree support), else git root, else cwd.
_GIT_ROOT="${BL_ROOT:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"

# db_exists checks whether the beads-lite database is reachable.
# Uses BL_ROOT if set (worktree support), else checks cwd.
db_exists() {
  [ -f "${BL_ROOT:-.}/.beads-lite/beads.db" ]
}

SNAPSHOT_FILE="${_GIT_ROOT}/.ralph-ban/.last-seen.json"
BOUNCE_FILE="${_GIT_ROOT}/.ralph-ban/.bounce-counts.json"
BL="${BL:-bl}"

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

  # Build maps of id -> status,title for old and new
  local changes=""

  # Parse new state into temp file
  local tmp_new tmp_old
  tmp_new=$(mktemp)
  tmp_old=$(mktemp)
  echo "$new_state" | while IFS= read -r line; do
    [ -z "$line" ] && continue
    echo "$line" | jq -r '[.id, .status, .title] | @tsv' 2>/dev/null
  done >"$tmp_new"

  echo "$old_state" | while IFS= read -r line; do
    [ -z "$line" ] && continue
    echo "$line" | jq -r '[.id, .status, .title] | @tsv' 2>/dev/null
  done >"$tmp_old"

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

# --- Circuit breaker: review bounce tracking ---

# record_bounce increments the bounce count for a card ID.
# A "bounce" is a review→doing transition (rejection).
record_bounce() {
  local card_id="$1"
  mkdir -p "$(dirname "$BOUNCE_FILE")"
  if [ ! -f "$BOUNCE_FILE" ]; then
    echo '{}' >"$BOUNCE_FILE"
  fi
  local current
  current=$(jq -r --arg id "$card_id" '.[$id] // 0' <"$BOUNCE_FILE")
  local new_count=$((current + 1))
  jq --arg id "$card_id" --argjson count "$new_count" \
    '.[$id] = $count' <"$BOUNCE_FILE" >"${BOUNCE_FILE}.tmp"
  mv "${BOUNCE_FILE}.tmp" "$BOUNCE_FILE"
  echo "$new_count"
}

# get_bounce_count returns the current bounce count for a card ID.
get_bounce_count() {
  local card_id="$1"
  if [ ! -f "$BOUNCE_FILE" ]; then
    echo "0"
    return
  fi
  jq -r --arg id "$card_id" '.[$id] // 0' <"$BOUNCE_FILE"
}

# clear_bounce removes the bounce count for a card (called when card reaches done).
clear_bounce() {
  local card_id="$1"
  if [ -f "$BOUNCE_FILE" ]; then
    jq --arg id "$card_id" 'del(.[$id])' <"$BOUNCE_FILE" >"${BOUNCE_FILE}.tmp"
    mv "${BOUNCE_FILE}.tmp" "$BOUNCE_FILE"
  fi
}

# --- Stall detection for in-progress cards ---

STALL_THRESHOLD="${STALL_THRESHOLD:-5}"
PROGRESS_FILE="${_GIT_ROOT}/.ralph-ban/.worker-progress.json"

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

# clear_progress_tracking removes the progress file.
clear_progress_tracking() {
  rm -f "$PROGRESS_FILE"
}

# read_stop_mode returns the configured stop mode: "batch" or "autonomous".
# Reads from .ralph-ban/config.json; defaults to "batch" if missing or unreadable.
# batch:      block only on doing cards — orchestrator stops once dispatched work completes.
# autonomous: block on todo + doing — orchestrator grinds until the board is empty.
read_stop_mode() {
  local config_file="${_GIT_ROOT}/.ralph-ban/config.json"
  if [ -f "$config_file" ]; then
    jq -r '.stop_mode // "batch"' "$config_file" 2>/dev/null || echo "batch"
  else
    echo "batch"
  fi
}
