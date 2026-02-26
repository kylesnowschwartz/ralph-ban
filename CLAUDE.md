# ralph-ban

Go TUI kanban board backed by beads-lite's SQLite database.

## Design Constraints

Four invariants that shape every decision in this codebase:

- **The TUI is a view, not the source of truth.** The CLI (`bl`), agent hooks, and other TUI sessions all read and write the same SQLite database. ralph-ban renders and edits but never assumes exclusive access.
- **Hooks fail open.** If a hook crashes, the agent continues. A broken hook should never permanently trap an agent. The one exception: the stop-guard blocks on uncommitted changes regardless.
- **Human approval before merge (batch mode).** In batch mode, automation handles claim, implement, test, and review — the merge decision stays human. In autonomous mode, reviewed cards merge without asking.
- **Priority P0-P4, ascending.** Lower number = higher urgency. P0 sorts to the top of each column.

## Architecture

- `board.go` — Root tea.Model. Routes messages to overlays or columns based on a `boardView` enum. Cross-cutting messages (resize, refresh, suspend) are intercepted before overlay routing — without this, a resize arriving while the form is open would be silently dropped.
- `column.go` — Wraps bubbles/list per kanban column. The wrapper exists because bubbles/list ships with global quit bindings, built-in filtering, and help that ralph-ban doesn't want. Without it, those would need re-disabling on every column construction.
- `card.go` — Adapts beads-lite Issue to list.Item.
- `form.go` — Modal overlay for create/edit. Full-screen modal rather than inline editing because expanding one column while contracting others complicates layout. Priority and type use selectors (not text inputs) to eliminate invalid state entirely.
- `store.go` — SQLite persistence via 2-second tick polling. Polling rather than fsnotify because SQLite WAL mode creates `-wal` and `-shm` files that confuse file watchers on macOS, and polling keeps the goroutine lifecycle trivial. Each refresh replaces all column items (no diffing — SQLite reads at this scale are sub-millisecond).
- `keys.go` — Vim-style h/j/k/l with arrow key fallbacks. Both are bound so the board isn't hostile to non-vim users.
- `messages.go` — Decoupled message types following the Elm architecture. Columns emit messages; the board routes them. No component holds a reference to another. This is what makes isolated overlays safe — a form can emit `saveMsg` without knowing board internals.

Layout uses `panOffset` to slide a window of visible columns (`minColumnWidth=24`). Narrow terminals can't fit all 5, so the focused column is always kept in view. Offscreen columns stay in memory — no evict/reload on pan.

## Agent Roles

Three agent types coordinate work. The split enforces separation of concerns by role — each agent can only act within its lane, reducing the blast radius of errors.

- **Orchestrator** (`agents/orchestrator.md`) — Sees the whole board, never touches code. Uses opus for coordination judgment.
- **Worker** (`agents/worker.md`) — Implements one card in an isolated git worktree. Uses sonnet (sufficient for implementation). Worktrees allow multiple workers in parallel without working-tree conflicts.
- **Reviewer** (`agents/reviewer.md`) — Reviews one card in an isolated worktree. Reports approve/reject to the orchestrator. Can't move cards or merge — concurrent status transitions from multiple reviewers would create race conditions. Only the orchestrator serializes transitions.

### Workflow phases

```
batch:      ASSESS -> SPAWN -> MONITOR -> REVIEW -> HUMAN APPROVAL -> MERGE
autonomous: ASSESS -> SPAWN -> MONITOR -> REVIEW -> MERGE
```

### Status flow

```
Backlog -> To Do -> Doing -> Review -> Done
```

Cards move right. The orchestrator owns status transitions and card closure.

### Worker claiming

Workers own their full card lifecycle. The orchestrator dispatches without pre-claiming. On startup, each worker runs `bl claim <id> --agent worker` then `bl update <id> --status doing`. This is load-bearing: TeammateIdle and TaskCompleted hooks identify cards by `assigned_to` matching the worker's name. See `agents/worker.md` for the canonical execution protocol.

## Hooks

Six hooks inject board state and enforce workflow gates. All source `hooks/lib/board-state.sh` for shared infrastructure (per-invocation SQLite caching, hash-based change detection, `BL_ROOT` for worktree path resolution).

- **SessionStart** — Board snapshot + framework preamble into agent context. User sees only the board summary, not the orchestration preamble.
- **UserPromptSubmit** — Diffs board since last snapshot. Embeds dispatch nudges (unclaimed todos), review queue alerts (3+ in review), circuit breaker (cards bouncing review-doing 3+ times), and stall detection (cards stuck in doing 5+ cycles).
- **Stop** — Three layers: (1) uncommitted changes block all agents, (2) claimed active cards block the claiming agent, (3) any active board work blocks the orchestrator. A `stop_hook_active` flag prevents infinite recursion.
- **TeammateIdle** — Workers can't go idle while they own doing/todo cards. Exits non-zero to keep the worker working.
- **TaskCompleted** — Workers can't mark tasks complete while their cards are still in doing. Enforces the review step.
- **PreCompact** — Re-injects full board state + preamble before context compression. Without this, compressed context loses the structured board state and the agent loses awareness.

### Hook output channels

| Event | Agent context | User-visible |
|-------|--------------|--------------|
| SessionStart | `additionalContext` (preamble + board) | `systemMessage` (board summary) |
| UserPromptSubmit | `additionalContext` (checkpoint + diffs) | `systemMessage` (diffs only) |
| Stop | `systemMessage` (workflow guidance) | `reason` (short block reason) |
| PreCompact | Both channels get full state (compression destroys prior context) |

## Agent Frontmatter

Workers and reviewers have `maxTurns` and `permissionMode: bypassPermissions` set in their YAML frontmatter. Claude Code enforces these natively — no CLI flags needed.

## Development

go.work workspace with `../beads-lite`. go.work rather than `replace` directives because go.work is local-only — `go.mod` points to the published version, so the repo builds correctly without the sibling directory.

```
go build ./...    # build
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

### Dependencies

- [bubbletea](https://github.com/charmbracelet/bubbletea) — TUI framework
- [bubbles](https://github.com/charmbracelet/bubbles) — TUI components
- [lipgloss](https://github.com/charmbracelet/lipgloss) — TUI styling
- [beads-lite](https://github.com/kylesnowschwartz/beads-lite) — SQLite task tracker
