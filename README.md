# ralph-ban

A terminal kanban board built with [bubbletea](https://github.com/charmbracelet/bubbletea), backed by [beads-lite](https://github.com/kylesnowschwartz/beads-lite)'s SQLite database.

```
╭───────────────────────╮
│    Backlog            │     To Do                    Doing                    Review
│                       │
│ No items.             │    Make a MIT License       make a Readme          No items.
│                       │    P2 task · bl-5d8x        P2 task · bl-71yd
│                       │
│                       │    Make a VERSION fil…
│                       │    P2 task · bl-yweo
│                       │
╰───────────────────────╯
                             [  | *Backlog* | To Do | Doing | Review>]

n new · e edit · ⏎ move → · ⌫ move ← · space detail · ? more
```

Five columns (Backlog, To Do, Doing, Review, Done) with vim-style navigation. The board pans horizontally when the terminal is too narrow to fit all columns.

Cards are shared — the `bl` CLI, Claude Code hooks, and other TUI sessions all read and write the same SQLite database. The TUI polls every 2 seconds to stay in sync.

## Install

Requires Go 1.25+ and a [beads-lite](https://github.com/kylesnowschwartz/beads-lite) database.

```sh
go install github.com/kylesnowschwartz/ralph-ban@latest
```

Or build from source:

```sh
git clone git@github.com:kylesnowschwartz/ralph-ban.git
cd ralph-ban
go build .
```

Initialize a beads-lite database if you don't have one:

```sh
bl init
```

Then run the board:

```sh
ralph-ban
# or: go run .
```

## Keybindings

### Navigation

| Key | Action |
|-----|--------|
| `h` / `←` | Focus left column |
| `j` / `↓` | Move cursor down |
| `k` / `↑` | Move cursor up |
| `l` / `→` | Focus right column |

### Cards

| Key | Action |
|-----|--------|
| `n` | New card |
| `e` | Edit card |
| `d` | Delete (press twice to confirm) |
| `space` | Expand card detail view |
| `Enter` | Move card right |
| `Backspace` | Move card left |
| `u` | Undo last move |
| `+` / `-` | Change priority |
| `Ctrl+click` | Move card to clicked column |

### General

| Key | Action |
|-----|--------|
| `?` | Toggle full help |
| `Ctrl+z` | Suspend (resume with `fg`) |
| `Ctrl+c` | Quit |
| `Esc` | Close overlay / cancel |

## Agent Integration

ralph-ban doubles as a coordination surface for Claude Code agents. An orchestrator reads the board to plan work, spawns workers into isolated git worktrees, and routes completed cards through review — all gated behind human approval before merge.

See [CLAUDE.md](CLAUDE.md) for architecture details and the `agents/` directory for agent templates.

## Development

Uses a `go.work` workspace with `../beads-lite` for local development. Changes to beads-lite types are immediately available without publishing.

```sh
go build ./...          # build
go test ./...           # test
just test               # run tests via justfile
just dump-view          # render one frame to stdout (headless testing)
```

## License

MIT
