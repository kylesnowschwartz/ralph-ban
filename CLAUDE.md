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
- `config.go` — Reads `.ralph-ban/config.json` (WIP limits, project commands). Spec-gate configuration (`require_specs_for_review`) lives in `.beads-lite/config.json`, not `.ralph-ban/config.json`. The TUI reads it via `beadslite.LoadConfig()`. The `bl` CLI reads the same file. Both honor the setting; `--force` overrides it one-time.
- `embed.go` — `//go:embed` directive for `.claude-plugin/`, `_agents/`, `commands/`, and `hooks/` directories.
- `commands/` — Slash commands extracted into the plugin. `rb-planning` breaks down work into board cards with EARS specs, file scope, and dependency ordering.
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

Single agent + subagent dispatch. The lead agent reads the board and dispatches subagents for implementation, dual-gate verification, exploration, and planning. Workers run in isolated worktrees. Reviewers, oracles, Explore, and Plan agents are read-only with respect to source (no worktree needed; oracles do write transcripts to `.agent-history/oracle/`).

Verification is a dual gate: the **reviewer** reads the diff and judges code; the **oracle** drives the running system and judges behavior. They run in parallel with no knowledge of each other. Merge requires both to APPROVE.

- **Orchestrator** (`_agents/rb-orchestrator.md`) — Reads the board, dispatches agents, combines reviewer + oracle verdicts, merges. Never implements or reviews code directly. Uses opus.
- **Worker** (`_agents/rb-worker.md`) — Implements one card in an isolated git worktree. Uses opus. Workers rebase onto main before committing so the orchestrator can fast-forward merge.
- **Reviewer** (`_agents/rb-reviewer.md`) — Reviews one worker's changes per dispatch. Uses opus. Classifies risk (green/yellow/red), runs lint+tests, checks EARS specs against the code, returns APPROVE/REJECT/ESCALATE. Fresh context — structural independence from the worker's reasoning.
- **Oracle** (`_agents/rb-oracle.md`) — Verifies behavior of one worker's changes per dispatch, in parallel with the reviewer. Uses opus. Classifies surface (terminal/browser/cli/library/none), drives the running system, observes whether asserted behaviors actually occur, persists a transcript to `.agent-history/oracle/<card-id>/`, returns APPROVE/REJECT/ESCALATE. Anti-sycophancy is the central design constraint — finding defects is the agent's success condition.
- **Explore** (built-in `subagent_type: "Explore"`) — Read-only codebase research. Investigates unfamiliar code, traces call paths, scopes changes. Findings go into card descriptions or `.agent-history/`.
- **Plan** (built-in `subagent_type: "Plan"`) — Architecture and design planning. Chooses approaches, designs module boundaries, writes implementation plans. Output enriches card descriptions so workers have clear scope.

### Workflow phases

```
batch:      ASSESS -> DISPATCH -> REVIEW + ORACLE (parallel) -> HUMAN APPROVAL -> MERGE
autonomous: ASSESS -> DISPATCH -> REVIEW + ORACLE (parallel) -> MERGE
```

### Verdict combination

```
reviewer APPROVE + oracle APPROVE   -> merge
reviewer REJECT  + oracle APPROVE   -> status=doing, claim preserved, same worker iterates with reviewer feedback
reviewer *       + oracle REJECT    -> status=todo,  claim released,  next worker re-claims with oracle findings
either ESCALATE                     -> pause for human input
```

Oracle REJECT outranks reviewer APPROVE: behavioral failure routes the card back to `todo` with the claim released, regardless of what the reviewer said. Reviewer-only REJECT keeps the card in `doing` for the same worker to iterate.

### Status flow

```
Backlog -> To Do -> Doing -> Review -> Done
```

Cards move right. The orchestrator owns status transitions and card closure.

### Agent discovery paths

Agent definitions live in `_agents/` (git-tracked, source of truth). `ralph-ban init` extracts the embedded binary's copy into two locations that serve different Claude Code resolution mechanisms:

| Event | Flag/mechanism | Directory read | Purpose |
|---|---|---|---|
| `ralph-ban claude` starts session | `--agent rb-orchestrator` | `.claude/agents/` | `--agent` CLI flag only searches `.claude/agents/` and `~/.claude/agents/` |
| `ralph-ban claude` loads plugin | `--plugin-dir .ralph-ban/plugin/` | `.ralph-ban/plugin/.claude-plugin/` | Loads hooks, settings, and plugin agents without user-level install |
| Orchestrator dispatches worker | `Agent(subagent_type: "rb-worker")` | `.ralph-ban/plugin/agents/` | Agent tool resolves subagent types through plugin directories |
| Orchestrator dispatches reviewer | `Agent(subagent_type: "rb-reviewer")` | `.ralph-ban/plugin/agents/` | Reviewer runs tests, reads diffs, returns structured verdict |
| Orchestrator dispatches oracle | `Agent(subagent_type: "rb-oracle")` | `.ralph-ban/plugin/agents/` | Oracle drives the running system, observes behavior, persists transcript, returns structured verdict |
| Plugin hooks run | (automatic) | `.ralph-ban/plugin/hooks/` | Hook runner finds hooks via the loaded plugin |
| User runs `/rb-planning` | Plugin command discovery | `.ralph-ban/plugin/commands/` | Slash commands loaded from plugin directory |

Both `.claude/agents/` and `.ralph-ban/plugin/agents/` are gitignored and regenerated by `ralph-ban init` (or `ralph-ban update`). Edit `_agents/` and `commands/` only.

## Hooks

Two plugin-level hooks inject board state and enforce workflow gates. Both source `hooks/lib/board-state.sh` for shared infrastructure (per-invocation SQLite caching, hash-based change detection, `BL_ROOT` for worktree path resolution).

- **SessionStart** — Board snapshot into agent context. User sees the board summary. Matcher `startup|clear|compact` — the same hook re-injects state after `/clear` and after compaction, replacing the former PreCompact hook.
- **Stop** — Blocks on uncommitted changes and active board work (batch: doing only; autonomous: todo + doing). A `stop_hook_active` flag prevents infinite recursion. Stall cycle limit prevents permanent trapping.

Two additional hooks live in agent frontmatter, scoped to a single agent rather than the plugin:

- **PreToolUse on `Agent`** (`_agents/rb-orchestrator.md`) — `hooks/prevent-nested-worktree.sh` denies `Agent` tool calls when the orchestrator is already inside a worktree, preventing nested `.claude/worktrees/X/.claude/worktrees/Y` paths.
- **Stop** (`_agents/rb-worker.md`) — a prompt-style hook that verifies the worker's tree is clean, the card is in review, and specs are checked before allowing the worker to stop.

### Hook output channels

| Event | Agent context | User-visible |
|-------|--------------|--------------|
| SessionStart | `additionalContext` (board summary) | `systemMessage` (board summary) |
| Stop | `systemMessage` (workflow guidance) | `reason` (short block reason) |

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

### Worktree context

Git worktrees only contain tracked files. Gitignored directories are absent by default. A `post-checkout` git hook (installed by `ralph-ban init`) automatically symlinks configured directories from the main repo into new worktrees. Workers can read `.ralph-ban/config.json`, `.agent-history/`, and `.cloned-sources/` as if they were in the main repo.

The directory list is configured in `.ralph-ban/config.json` under `worktree_symlinks`. Defaults: `.agent-history`, `.cloned-sources`, `.ralph-ban`. `.beads-lite` is intentionally excluded — workers access the database through `bl`, which handles path resolution and locking. Set to `[]` to disable symlinks entirely. The hook reads config via `jq` at runtime; if `jq` is unavailable, defaults are used.

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
