package main

import "embed"

// pluginFS bundles the complete Claude Code plugin for extraction by `ralph-ban init`.
// The embedded trees — plugin manifest, hook scripts, agent definitions, and skills —
// form a self-contained plugin directory that `--plugin-dir` can load directly.
// The `all:` prefix is required for `.claude-plugin` because embed skips dot-prefixed
// directories by default.
//
// Agent source lives in `_agents/` (underscore prefix) to keep it out of Claude Code's
// agent discovery chain. extractPlugin remaps `_agents/` → `agents/` in the output
// so the plugin structure is correct.
//
//go:embed all:.claude-plugin _agents commands hooks skills
var pluginFS embed.FS

// gitHooksFS bundles git hooks (post-checkout, etc.) for installation by `ralph-ban init`.
// These are standard git hooks, separate from the Claude Code hooks in pluginFS.
// The post-checkout hook symlinks gitignored directories into new worktrees so agents
// have the same context as the main repo.
//
//go:embed githooks
var gitHooksFS embed.FS
