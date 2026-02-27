#!/usr/bin/env bash
# PreToolUse hook: block Task tool calls when already inside a worktree.
#
# Orchestrators running inside a worktree would create deeply nested paths
# (.claude/worktrees/X/.claude/worktrees/Y) that break GOWORK resolution
# and waste turns unwinding the wrong tree. Detecting the worktree path in
# $PWD is cheap and reliable — the common dir always resolves through it.
#
# Fails open on any error so a broken hook never silently blocks tool calls.

trap 'exit 0' ERR
set -uo pipefail

cwd="${PWD:-$(pwd)}"

# Worktrees are always created under .claude/worktrees/ in this project.
# If the current directory contains that segment, we're inside one.
if [[ "$cwd" == *".claude/worktrees/"* ]]; then
  # Provide the git common dir path so the agent knows where to cd to.
  repo_root=$(git rev-parse --git-common-dir 2>/dev/null | xargs -I{} dirname {} 2>/dev/null || echo "")

  if [ -n "$repo_root" ]; then
    reason="Cannot spawn worktree from inside a worktree. Run: cd ${repo_root}"
  else
    reason="Cannot spawn worktree from inside a worktree (nested path detected in: ${cwd})"
  fi

  jq -n --arg reason "$reason" '{
    hookSpecificOutput: {
      permissionDecision: "deny"
    },
    systemMessage: $reason
  }'
  exit 0
fi

# Not inside a worktree — allow.
exit 0
