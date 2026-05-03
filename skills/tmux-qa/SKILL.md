---
name: tmux-qa
description: This skill should be used when the user asks to "QA changes", "verify this works", "test the build", "check if this runs", "validate changes in tmux", or wants end-to-end verification of code changes by running builds, tests, and applications in tmux.
argument-hint: "[scope of changes to verify]"
---

# tmux-QA

Verify that code changes work by driving an isolated tmux session — build, run, observe, report. This skill does not write code; it builds, runs, observes, and reports.

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine what needs verification.

## Bundled Resources

- `references/tmux-primitives.md` — deeper tmux primer (key names, working-directory tricks, common patterns for vim/REPL/git rebase, common mistakes). Load when verifying interactive tools or when the inlined Primitives section below is insufficient.
- `scripts/tmux-wrapper.sh` — optional helper that collapses `new-session` / `send-keys` / `capture-pane` / `kill-session` into single commands. See the reference file for usage. Raw `tmux` commands work just as well.

## Workflow

1. Read the changeset — `git diff`, `git log`, understand what changed
2. Detect the project — look for `Makefile`, `go.mod`, `package.json`, `Cargo.toml`, etc. Read `README` and `CLAUDE.md` for build instructions
3. Create an isolated tmux session — never touch the user's existing sessions
4. Build and test — run the project's build and test commands in the isolated session
5. Run the thing — for servers, CLIs, and TUIs, actually run them and verify behavior
6. Verify specific changes — if the diff adds an endpoint, test it. If it fixes a bug, confirm the fix
7. Clean up — kill the test session
8. Report — structured PASS / FAIL / PARTIAL with evidence

## tmux Primitives

### Session lifecycle

Create an isolated session with a unique name and guarantee teardown:

```bash
TEST_SESSION="qa-$(date +%s)"

# Default geometry is fine for CLIs and servers
tmux new-session -d -s "$TEST_SESSION"

# For TUIs, pin dimensions so layout is deterministic
tmux new-session -d -s "$TEST_SESSION" -x 120 -y 40

# Guarantee teardown even if the script errors partway through
trap 'tmux kill-session -t "$TEST_SESSION" 2>/dev/null' EXIT
```

### Running commands

Send commands with a distinctive exit sentinel so assertions don't false-match on program output:

```bash
tmux send-keys -t "$TEST_SESSION" 'go build ./... 2>&1; echo "__QA_EXIT__:$?"' Enter

# Special keys for TUIs
tmux send-keys -t "$TEST_SESSION" C-c           # interrupt
tmux send-keys -t "$TEST_SESSION" Escape        # ESC
tmux send-keys -t "$TEST_SESSION" C-o           # ctrl+o
tmux send-keys -t "$TEST_SESSION" Up Down Tab   # arrows, tab, etc.
```

The sentinel reflects only the *foreground command list's* exit. It does *not* survive `cmd &` (background), `nohup`, daemonisers, or wrappers that fork-and-return — in those cases the sentinel fires when the wrapper returns, not when the asserted work finishes. For backgrounded work, wait on a different signal (a log line via `log-tail-qa`, a port becoming reachable, a DB row appearing) rather than the sentinel.

For multi-line input where each line should be typed (not pasted as a block — REPLs and editors often distinguish), send keys per line:

```bash
tmux send-keys -t "$TEST_SESSION" 'first line' Enter
tmux send-keys -t "$TEST_SESSION" 'second line' Enter
```

A single `send-keys` with embedded newlines may trigger bracketed-paste handling in some shells and editors, which is *not* the same as typing. When the spec asserts behaviour observable only when typed (autocomplete, debounced syntax-highlighting), key-by-key is the right primitive.

### Reading output (the assertion primitive)

```bash
# Current visible content
tmux capture-pane -t "$TEST_SESSION" -p

# With scrollback (long builds overflow the default view)
tmux capture-pane -t "$TEST_SESSION" -p -S -200

# Both alternate-screen and main-screen — required for vim, less, htop, full-screen TUIs
tmux capture-pane -t "$TEST_SESSION" -p -a
```

`capture-pane` includes the echoed command line itself. Assertions must be specific enough not to match the command — prefer patterns that appear only in the *output*, not the invocation.

**Alternate-screen mode trap.** Programs like `vim`, `less`, `htop`, and most full-screen TUIs switch to the *alternate screen* on start and back to the main screen on exit. Without `-a`, `capture-pane` reads the screen the program is currently on, which is usually what you want; but if the spec asserts behaviour visible only on the alt-screen and the program has already exited, the alt-screen content is gone. Capture *during* the asserted state, not after.

**`TERM` divergence.** The default `TERM` inside a tmux pane is `screen` or `tmux-256color`, not `xterm-256color`. Some TUIs check `TERM` and refuse colour or alt-screen modes against unrecognised values. If the spec asserts colour output and the capture shows none, check whether the program saw the wrong `TERM` and either set `default-terminal` in tmux or `TERM=xterm-256color` for the asserted command.

### Waiting for output

Poll `capture-pane`; do not sleep-and-hope:

```bash
for i in $(seq 1 30); do
  if tmux capture-pane -t "$TEST_SESSION" -p | grep -q "__QA_EXIT__:0"; then
    break
  fi
  sleep 0.5
done
```

### Multiple panes

Target panes explicitly once split, using `session:window.pane`:

```bash
# Split for parallel work (server + client pattern)
tmux split-window -h -t "$TEST_SESSION"
tmux send-keys -t "$TEST_SESSION:0.0" 'make run' Enter
# poll pane 0 for "listening on" or similar ready signal...
tmux send-keys -t "$TEST_SESSION:0.1" 'curl -sf localhost:8080/health' Enter
```

## Rules

- **Own session only.** Create `qa-<timestamp>`, never operate in existing sessions.
- **Always clean up.** Kill the session when done, even on failure. Prefer `trap … EXIT` over hoping execution reaches the teardown line.
- **Pin geometry for TUIs.** Pass `-x W -y H` to `new-session` when layout depends on terminal size; otherwise the default is fine.
- **Read before asserting.** Use `capture-pane`; do not assume.
- **Use a distinctive exit sentinel.** Append `; echo "__QA_EXIT__:$?"` so assertions don't collide with normal program output.
- **Poll, don't race.** Builds and servers take time. Check `capture-pane` in a loop.
- **Report evidence.** Show actual output for failures. "Build failed" is useless — show the error.
- **Stay in scope.** Verify what changed, unless asked for a full regression.
## Report Format

```
## QA Report

**Scope**: <what was verified>
**Verdict**: PASS | FAIL | PARTIAL

### Build
- [PASS/FAIL] `<command>` -- <notes>

### Tests
- [PASS/FAIL] `<command>` -- <N passed, M failed>
  - <failure details if any>

### Runtime Verification
- [PASS/FAIL] <what was checked> -- <evidence>

### Issues
1. <description with actual output>
```
