package main

import "embed"

// pluginFS bundles the complete Claude Code plugin for extraction by `ralph-ban init`.
// The three embedded trees — plugin manifest, hook scripts, and agent definitions —
// form a self-contained plugin directory that `--plugin-dir` can load directly.
// The `all:` prefix is required for `.claude-plugin` because embed skips dot-prefixed
// directories by default.
//
//go:embed all:.claude-plugin agents hooks
var pluginFS embed.FS
