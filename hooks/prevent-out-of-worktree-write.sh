#!/usr/bin/env bash
# PreToolUse hook: deny Edit/Write/MultiEdit/NotebookEdit calls whose
# target path resolves outside the current worktree.
#
# Claude Code's `isolation: worktree` sets CWD to the worktree, but
# absolute paths in tool inputs are not redirected. An LLM constructing
# absolute paths to the main repo root will silently land its writes in
# main — past every other isolation safeguard. This hook is the lexical
# containment gate that closes that gap.
#
# Scope: only fires when CWD is inside `.claude/worktrees/`. Outside a
# worktree the hook is a no-op so it cannot interfere with the
# orchestrator's main-repo work.
#
# Fails open on any error so a broken hook never permanently blocks
# legitimate edits.

trap 'exit 0' ERR
set -uo pipefail

input=$(cat)
cwd=$(echo "$input" | jq -r '.cwd // empty')
tool_name=$(echo "$input" | jq -r '.tool_name // empty')

# NotebookEdit uses notebook_path; Edit/Write/MultiEdit use file_path.
case "$tool_name" in
NotebookEdit) target=$(echo "$input" | jq -r '.tool_input.notebook_path // empty') ;;
*) target=$(echo "$input" | jq -r '.tool_input.file_path // empty') ;;
esac

# Nothing to validate — schema errors are not this hook's job.
[ -z "$target" ] && exit 0
[ -z "$cwd" ] && exit 0

# Only enforce containment when the actor is inside a worktree.
case "$cwd" in
*"/.claude/worktrees/"*) ;;
*) exit 0 ;;
esac

# Compute the worktree root from CWD: everything up to and including
# `.claude/worktrees/<id>`. Subdirectory CWDs (e.g. inside server/) still
# resolve to the same root.
prefix="${cwd%%/.claude/worktrees/*}/.claude/worktrees/"
remainder="${cwd#"$prefix"}"
worktree_id="${remainder%%/*}"
worktree_root="${prefix}${worktree_id}"

# Resolve target to an absolute lexical path. No realpath / readlink —
# symlinks like `.agent-history` (worktree -> main) are intentional and
# should pass through transparently.
case "$target" in
/*) abs="$target" ;;
*) abs="$cwd/$target" ;;
esac

deny() {
  local reason="$1"
  jq -n \
    --arg reason "$reason" \
    --arg target "$abs" \
    --arg root "$worktree_root" \
    '{
      hookSpecificOutput: {
        hookEventName: "PreToolUse",
        permissionDecision: "deny",
        permissionDecisionReason: $reason
      },
      systemMessage: ("Path containment: blocked write to " + $target + " (outside worktree " + $root + ")")
    }'
  exit 0
}

# Reject `..` traversal. Lexical normalization in bash is brittle and a
# correctly-written worker has no reason to use `..` in paths anyway.
case "$abs" in
*"/../"* | *"/..")
  deny "file_path contains '..'; use a path rooted at the worktree (${worktree_root})"
  ;;
esac

# Lexical containment: target must be the worktree root or a descendant.
case "$abs" in
"$worktree_root" | "$worktree_root"/*)
  exit 0
  ;;
*)
  deny "file_path ${abs} is outside worktree root ${worktree_root}; use a path inside the worktree (relative paths preferred)"
  ;;
esac
