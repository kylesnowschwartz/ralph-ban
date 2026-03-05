# ralph-ban

Go TUI kanban board backed by beads-lite's SQLite database.

## Design Constraints

Four invariants that shape every decision in this codebase:

- **The TUI is a view, not the source of truth.** The CLI (`bl`), agent hooks, and other TUI sessions all read and write the same SQLite database. ralph-ban renders and edits but never assumes exclusive access.
- **Hooks fail open.** If a hook crashes, the agent continues. A broken hook should never permanently trap an agent. The one exception: the stop-guard blocks on uncommitted changes regardless.
- **Human approval before merge (batch mode).** In batch mode, automation handles claim, implement, test, and review — the merge decision stays human. In autonomous mode, reviewed cards merge without asking.
- **Priority P0-P4, ascending.** Lower number = higher urgency. P0 sorts to the top of each column.

## Architecture

- `main.go` — CLI entry point. Routes subcommands (`init`, `claude`, `snapshot`, `version`, `update`) and falls through to the TUI. `var Version` is set via ldflags at build time.
- `board.go` — Root tea.Model. Routes messages to overlays or columns based on a `boardView` enum. Cross-cutting messages (resize, refresh, suspend) are intercepted before overlay routing — without this, a resize arriving while the form is open would be silently dropped.
- `column.go` — Wraps bubbles/list per kanban column. The wrapper exists because bubbles/list ships with global quit bindings, built-in filtering, and help that ralph-ban doesn't want. Without it, those would need re-disabling on every column construction.
- `card.go` — Adapts beads-lite Issue to list.Item.
- `form.go` — Modal overlay for create/edit. Full-screen modal rather than inline editing because expanding one column while contracting others complicates layout. Priority and type use selectors (not text inputs) to eliminate invalid state entirely.
- `store.go` — SQLite persistence via 2-second tick polling. Polling rather than fsnotify because SQLite WAL mode creates `-wal` and `-shm` files that confuse file watchers on macOS, and polling keeps the goroutine lifecycle trivial. Each refresh replaces all column items (no diffing — SQLite reads at this scale are sub-millisecond).
- `keys.go` — Vim-style h/j/k/l with arrow key fallbacks. Both are bound so the board isn't hostile to non-vim users.
- `messages.go` — Decoupled message types following the Elm architecture. Columns emit messages; the board routes them. No component holds a reference to another. This is what makes isolated overlays safe — a form can emit `saveMsg` without knowing board internals.
- `init.go` — `ralph-ban init` bootstraps a project: creates `.ralph-ban/config.json`, `.beads-lite/beads.db`, extracts the embedded plugin, and installs agents for `--agent` discovery.
- `claude.go` — `ralph-ban claude` launches a Claude Code orchestrator session with the right flags (`--agent`, `--plugin`, `--settings`).
- `update.go` — `ralph-ban update` downloads latest releases of both ralph-ban and bl from GitHub, replaces the binaries, and refreshes the embedded plugin.
- `config.go` — Reads `.ralph-ban/config.json` (WIP limits, project commands). `require_specs_for_review` (default true): blocks cards from entering Review unless all specifications are checked. Cards with no specs are unaffected. The `bl` CLI enforces the same gate unless `--force` is passed.
- `embed.go` — `//go:embed` directive for `.claude-plugin/`, `_agents/`, and `hooks/` directories.
- `snapshot.go` — `ralph-ban snapshot` exports board state as JSON or ASCII.
- `dump.go` — `--dump` flag renders one TUI frame as JSON for testing.
- `filter.go` — Card filtering overlay.
- `deplink.go` — Dependency link overlay for connecting cards.
- `resolution.go` — Close-card overlay with resolution choice (done/wontfix/duplicate).
- `transforms.go` — Pure functions for card sorting, grouping, and layout math.
- `theme.go` — Lipgloss styles and color constants.
- `icons.go` — Unicode icons for priority and card type badges.

Layout uses `panOffset` to slide a window of visible columns (`minColumnWidth=24`). Narrow terminals can't fit all 5, so the focused column is always kept in view. Offscreen columns stay in memory — no evict/reload on pan.

## Agent Model

Single agent + subagent dispatch. The lead agent reads the board and dispatches subagents for implementation, exploration, and planning. Workers run in isolated worktrees. Explore and Plan agents are read-only (no worktree needed).

- **Orchestrator** (`agents/orchestrator.md`) — Reads the board, dispatches agents, reviews diffs, merges. Never implements code directly. Uses opus.
- **Worker** (`agents/worker.md`) — Implements one card in an isolated git worktree. Uses sonnet. Workers rebase onto main before committing so the orchestrator can fast-forward merge.
- **Explore** (built-in `subagent_type: "Explore"`) — Read-only codebase research. Investigates unfamiliar code, traces call paths, scopes changes. Findings go into card descriptions or `.agent-history/`.
- **Plan** (built-in `subagent_type: "Plan"`) — Architecture and design planning. Chooses approaches, designs module boundaries, writes implementation plans. Output enriches card descriptions so workers have clear scope.

### Workflow phases

```
batch:      ASSESS -> DISPATCH -> REVIEW -> HUMAN APPROVAL -> MERGE
autonomous: ASSESS -> DISPATCH -> REVIEW -> MERGE
```

### Status flow

```
Backlog -> To Do -> Doing -> Review -> Done
```

Cards move right. The orchestrator owns status transitions and card closure.

## Hooks

Four hooks inject board state and enforce workflow gates. All source `hooks/lib/board-state.sh` for shared infrastructure (per-invocation SQLite caching, hash-based change detection, `BL_ROOT` for worktree path resolution).

- **SessionStart** — Board snapshot into agent context. User sees the board summary.
- **UserPromptSubmit** — Diffs board since last snapshot. Embeds dispatch nudges (unclaimed todos), review queue alerts (3+ in review), circuit breaker (cards bouncing review-doing 3+ times), and stall detection (cards stuck in doing 5+ cycles).
- **Stop** — Blocks on uncommitted changes and active board work (batch: doing only; autonomous: todo + doing). A `stop_hook_active` flag prevents infinite recursion. Stall cycle limit prevents permanent trapping.
- **PreCompact** — Re-injects board state summary before context compression. Without this, compressed context loses board awareness.

### Hook output channels

| Event | Agent context | User-visible |
|-------|--------------|--------------|
| SessionStart | `additionalContext` (board summary) | `systemMessage` (board summary) |
| UserPromptSubmit | `additionalContext` (full: diffs, nudges, breaker, stalls) | `systemMessage` (actionable only: breaker, stalls, rate limit) |
| Stop | `systemMessage` (workflow guidance) | `reason` (short block reason) |
| PreCompact | Both channels get full state (compression destroys prior context) |

## Agent Frontmatter

Workers have `permissionMode: bypassPermissions` set in their YAML frontmatter. Claude Code enforces this natively — no CLI flags needed.

## Development

go.work workspace with `../beads-lite`. go.work rather than `replace` directives because go.work is local-only — `go.mod` points to the published version, so the repo builds correctly without the sibling directory.

```
just build        # build with version from VERSION file
go run .          # run TUI (requires bl init first)
```

### Worktree builds

Agents running in isolated worktrees don't have access to go.work. Prefix all Go commands with `GOWORK=off`:

```
GOWORK=off go build ./...
GOWORK=off go test ./... -count=1
GOWORK=off go vet ./...
```

This uses go.mod's published dependency versions instead of the local workspace.

SQLite via ncruces/go-sqlite3 (wazero WebAssembly runtime) — no CGo toolchain dependency.

### Releasing

Version lives in `VERSION` (semver, no `v` prefix). The Justfile has `bump` and `release` recipes:

```
just bump patch    # 0.5.0 -> 0.5.1, stages VERSION
just release       # commit, tag, push, create GitHub release
```

goreleaser (`.goreleaser.yaml`) runs via GitHub Actions (`.github/workflows/release.yml`) on tag push. It builds darwin/linux amd64/arm64 binaries with version embedded via `-X main.Version`.

Users update via `ralph-ban update`, which downloads latest releases of both ralph-ban and bl from GitHub.

### Dependencies

- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [bubbles](https://github.com/charmbracelet/bubbles) — TUI components
- [lipgloss](https://github.com/charmbracelet/lipgloss) — TUI styling
- [beads-lite](https://github.com/kylesnowschwartz/beads-lite) — SQLite task tracker
