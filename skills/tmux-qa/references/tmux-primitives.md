# tmux Primitives — Driving Interactive Commands

Reference material for the `tmux-qa` skill. Loaded into context when deeper tmux background is needed than the SKILL.md body provides — for example, when verifying interactive tools (vim plugins, REPLs, full-screen TUIs) where the QA target is itself a tmux-driven program.

## Overview

Interactive CLI tools (vim, interactive git rebase, REPLs, etc.) cannot be controlled through standard bash because they require a real terminal. tmux provides detached sessions that can be controlled programmatically via `send-keys` and `capture-pane`.

## When to Reach for tmux

**Use tmux when:**
- Running vim, nano, or other text editors programmatically
- Controlling interactive REPLs (Python, Node, etc.)
- Handling interactive git commands (`git rebase -i`, `git add -p`)
- Working with full-screen terminal apps (htop, etc.)
- Commands that require terminal control codes or readline

**Don't use for:**
- Simple non-interactive commands (use the regular Bash tool)
- Commands that accept input via stdin redirection
- One-shot commands that don't need interaction

## Quick Reference

| Task           | Command                                       |
| -------------- | --------------------------------------------- |
| Start session  | `tmux new-session -d -s <name> <command>`     |
| Send input     | `tmux send-keys -t <name> 'text' Enter`       |
| Capture output | `tmux capture-pane -t <name> -p`              |
| Stop session   | `tmux kill-session -t <name>`                 |
| List sessions  | `tmux list-sessions`                          |

## Core Pattern

### Before (won't work)

```bash
# This hangs because vim expects an interactive terminal
bash -c "vim file.txt"
```

### After (works)

```bash
# Create detached tmux session
tmux new-session -d -s edit_session vim file.txt

# Send commands (Enter, Escape are tmux key names)
tmux send-keys -t edit_session 'i' 'Hello World' Escape ':wq' Enter

# Capture what's on screen
tmux capture-pane -t edit_session -p

# Clean up
tmux kill-session -t edit_session
```

## Implementation

### Basic Workflow

1. Create the detached session with the interactive command
2. Wait briefly for initialization (100-500ms depending on command)
3. Send input using `send-keys` (special keys like Enter, Escape are passed by name)
4. Capture output using `capture-pane -p` to see current screen state
5. Repeat steps 3-4 as needed
6. Terminate the session when done

### Special Keys

Common tmux key names:

- `Enter` — Return/newline
- `Escape` — ESC key
- `C-c` — Ctrl+C
- `C-x` — Ctrl+X
- `Up`, `Down`, `Left`, `Right` — Arrow keys
- `Space` — Space bar
- `BSpace` — Backspace

### Working Directory

Specify the working directory at session creation:

```bash
tmux new-session -d -s git_session -c /path/to/repo git rebase -i HEAD~3
```

### Helper Wrapper

A wrapper script is bundled with the `tmux-qa` skill at `${CLAUDE_PLUGIN_ROOT}/skills/tmux-qa/scripts/tmux-wrapper.sh`. It collapses the four-step `new-session` / `send-keys` / `capture-pane` / `kill-session` pattern into single commands:

```bash
# Resolve once for readability
WRAP="${CLAUDE_PLUGIN_ROOT}/skills/tmux-qa/scripts/tmux-wrapper.sh"

# Start session (auto-captures initial pane state after a 0.3s settle)
"$WRAP" start <session-name> <command> [args...]

# Send input (auto-captures pane state after a 0.2s settle)
"$WRAP" send <session-name> 'text' Enter

# Capture current pane state without sending anything
"$WRAP" capture <session-name>

# Stop the session
"$WRAP" stop <session-name>
```

The wrapper is optional — every invocation can be expressed in raw `tmux` commands as shown in the patterns above.

## Common Patterns

### Python REPL

```bash
tmux new-session -d -s python python3 -i
tmux send-keys -t python 'import math' Enter
tmux send-keys -t python 'print(math.pi)' Enter
tmux capture-pane -t python -p  # See output
tmux kill-session -t python
```

### Vim Editing

```bash
tmux new-session -d -s vim vim /tmp/file.txt
sleep 0.3  # Wait for vim to start
tmux send-keys -t vim 'i' 'New content' Escape ':wq' Enter
# File is now saved
```

### Interactive Git Rebase

```bash
tmux new-session -d -s rebase -c /repo/path git rebase -i HEAD~3
sleep 0.5
tmux capture-pane -t rebase -p  # See rebase editor
# Send commands to modify rebase instructions
tmux send-keys -t rebase 'Down' 'Home' 'squash' Escape
tmux send-keys -t rebase ':wq' Enter
```

## Common Mistakes

### Not Waiting After Session Start

**Problem:** Capturing immediately after `new-session` shows a blank screen.

**Fix:** Add a brief sleep (100-500ms) before first capture.

```bash
tmux new-session -d -s sess command
sleep 0.3  # Let command initialize
tmux capture-pane -t sess -p
```

### Forgetting the Enter Key

**Problem:** Commands typed but not executed.

**Fix:** Explicitly send Enter as a separate argument.

```bash
tmux send-keys -t sess 'print("hello")' Enter
```

### Using Wrong Key Names

**Problem:** `tmux send-keys -t sess '\n'` doesn't work.

**Fix:** Use tmux key names — `Enter`, not `\n`.

```bash
tmux send-keys -t sess 'text' Enter  # OK
tmux send-keys -t sess 'text\n'      # NO
```

### Not Cleaning Up Sessions

**Problem:** Orphaned tmux sessions accumulate.

**Fix:** Always kill the session when done. Prefer `trap … EXIT` so teardown runs even on error.

```bash
tmux kill-session -t session_name
# Or check for existing: tmux has-session -t name 2>/dev/null
```

## Real-World Impact

- Enables programmatic control of vim/nano for file editing
- Allows automation of interactive git workflows (rebase, add -p)
- Makes REPL-based testing and debugging possible
- Unblocks any tool that requires terminal interaction
- No custom PTY management needed — tmux handles it
