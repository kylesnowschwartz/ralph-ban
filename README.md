# ralph-ban

A [ralph loop](https://ghuntley.com/ralph/) sets a target and works until it's done. ralph-ban makes that target a kanban board.

Terminal kanban built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), backed by [beads-lite](https://github.com/kylesnowschwartz/beads-lite)'s SQLite database. Five columns (Backlog, To Do, Doing, Review, Done) with vim-style navigation. The board pans horizontally when the terminal is too narrow. Cards sync across the `bl` CLI, Claude Code hooks, and other TUI sessions via 2-second polling.

<p align="center">
  <img src="demo.gif" alt="ralph-ban board demo" width="850" />
</p>

## Install

Requires Go 1.25+.

```bash
go install github.com/kylesnowschwartz/ralph-ban@latest
```

Or build from source:

```bash
git clone git@github.com:kylesnowschwartz/ralph-ban.git
cd ralph-ban
go build .
```

## Usage

Initialize a project, then open the board:

```bash
ralph-ban init --demo    # create project with a demo board (Conway's Game of Life)
ralph-ban                # open the board
```

### Claude Code orchestrator

ralph-ban ships a Claude Code plugin with hooks and agents. `ralph-ban init` extracts them into `.ralph-ban/plugin/` and `.claude/agents/`.

```bash
ralph-ban claude                    # batch mode (pauses for human merge approval)
ralph-ban claude --auto             # drain the board without intervention
ralph-ban claude --continue         # continue most recent session
ralph-ban claude --resume           # interactive session picker
```

### Keybindings

`?` toggles full help. `Ctrl+z` suspends the TUI (resume with `fg`).

**Navigation**

| Key | Action |
|-----|--------|
| `h` / `←` | Focus left column |
| `j` / `↓` | Move cursor down |
| `k` / `↑` | Move cursor up |
| `l` / `→` | Focus right column |
| `/` | Search cards |

**Cards**

| Key | Action |
|-----|--------|
| `n` | New card |
| `e` | Edit card |
| `d` | Delete (press twice to confirm) |
| `z` | Zoom card detail |
| `Enter` | Move card right |
| `Backspace` | Move card left |
| `u` | Undo last move |
| `+` / `-` | Change priority |

## Development

Requires [just](https://github.com/casey/just) for task running. Uses a `go.work` workspace with `../beads-lite` for local development.

```bash
just build     # build
just test      # run tests
just run       # build and launch TUI
just lint      # vet + staticcheck
just release   # tag, push, create GitHub release
```

## License

MIT
