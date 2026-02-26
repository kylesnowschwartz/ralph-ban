#!/usr/bin/env bash
# Cleans up stale agent worktrees under .claude/worktrees/.
#
# Crashed or interrupted workers leave dangling worktree directories and
# branches. This script prunes stale git references, force-removes any
# remaining .claude/worktrees/* entries (excluding the main working tree),
# and deletes their orphaned branches.
#
# Safe to run any time: defensive against missing branches, already-removed
# worktrees, or partially cleaned state. Idempotent — a second run is a no-op.
#
# Usage:
#   scripts/cleanup-worktrees.sh                # clean all agent worktrees
#   scripts/cleanup-worktrees.sh --dry-run      # report without removing

set -euo pipefail

DRY_RUN=false
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=true ;;
    *) echo "Unknown argument: $arg" >&2; exit 1 ;;
  esac
done

# Resolve the main repo root regardless of where the script is invoked from.
# `--show-toplevel` returns the worktree root when inside a linked worktree,
# so we use `--git-common-dir` instead: it always points to the main .git dir.
COMMON_GIT_DIR="$(git rev-parse --git-common-dir 2>/dev/null)" \
  || { echo "[cleanup-worktrees] ERROR: not inside a git repository" >&2; exit 1; }
REPO_ROOT="$(cd "$(dirname "$COMMON_GIT_DIR")" && pwd)"
WORKTREES_DIR="${REPO_ROOT}/.claude/worktrees"

removed_worktrees=0
removed_branches=0
skipped=0

log() { echo "[cleanup-worktrees] $*"; }

# --- Phase 1: prune stale references ---
log "Running git worktree prune..."
git -C "$REPO_ROOT" worktree prune --verbose 2>&1 | sed 's/^/  /'

if [ ! -d "$WORKTREES_DIR" ]; then
  log "No worktrees directory found at $WORKTREES_DIR — nothing to clean."
  exit 0
fi

# --- Phase 2: collect registered worktrees under .claude/worktrees/ ---
# `git worktree list --porcelain` emits:
#   worktree /path
#   HEAD <sha>
#   branch refs/heads/<name>   (or "detached" for detached HEAD)
#   (blank line)
# We extract (path, branch) pairs for entries inside $WORKTREES_DIR.
declare -a paths=()
declare -a branches=()

current_path=""
current_branch=""

while IFS= read -r line; do
  if [[ "$line" == worktree\ * ]]; then
    current_path="${line#worktree }"
    current_branch=""
  elif [[ "$line" == branch\ * ]]; then
    current_branch="${line#branch refs/heads/}"
  elif [[ -z "$line" ]]; then
    # End of a stanza — record if it's under our worktrees dir and not main.
    if [[ -n "$current_path" && "$current_path" == "$WORKTREES_DIR"/* ]]; then
      paths+=("$current_path")
      branches+=("${current_branch:-}")
    fi
    current_path=""
    current_branch=""
  fi
done < <(git -C "$REPO_ROOT" worktree list --porcelain; echo "")
# The trailing echo ensures the last stanza is flushed by the blank-line check.

# --- Phase 3: remove each worktree and its branch ---
if [ "${#paths[@]}" -eq 0 ]; then
  log "No agent worktrees registered — nothing to remove."
else
  log "Found ${#paths[@]} agent worktree(s)."
  for i in "${!paths[@]}"; do
    wt_path="${paths[$i]}"
    wt_branch="${branches[$i]}"

    # Safety guard: never touch main or master.
    if [[ "$wt_branch" == "main" || "$wt_branch" == "master" ]]; then
      log "  SKIP $wt_path (branch '$wt_branch' is protected)"
      skipped=$((skipped + 1))
      continue
    fi

    log "  Removing worktree: $wt_path (branch: ${wt_branch:-<none>})"

    if [ "$DRY_RUN" = true ]; then
      log "  [dry-run] would run: git worktree remove --force $wt_path"
    else
      # Fail open: if git remove fails, fall back to manual rm so the rest
      # of cleanup can continue.
      if git -C "$REPO_ROOT" worktree remove --force "$wt_path" 2>/dev/null; then
        log "  Worktree removed."
      else
        log "  git worktree remove failed — attempting manual removal."
        rm -rf "$wt_path" 2>/dev/null || true
      fi
      removed_worktrees=$((removed_worktrees + 1))
    fi

    if [ -n "$wt_branch" ]; then
      log "  Deleting branch: $wt_branch"
      if [ "$DRY_RUN" = true ]; then
        log "  [dry-run] would run: git branch -D $wt_branch"
      else
        # Fail open: branch may already be deleted or may not exist.
        if git -C "$REPO_ROOT" branch -D "$wt_branch" 2>/dev/null; then
          log "  Branch deleted."
          removed_branches=$((removed_branches + 1))
        else
          log "  Branch '$wt_branch' not found or already deleted — skipping."
        fi
      fi
    fi
  done
fi

# --- Phase 4: final prune to clear any dangling references ---
log "Running git worktree prune (final)..."
git -C "$REPO_ROOT" worktree prune --verbose 2>&1 | sed 's/^/  /'

# --- Summary ---
if [ "$DRY_RUN" = true ]; then
  log "Dry run complete. Would remove ${#paths[@]} worktree(s)."
else
  log "Done. Removed $removed_worktrees worktree(s), $removed_branches branch(es), skipped $skipped."
fi
