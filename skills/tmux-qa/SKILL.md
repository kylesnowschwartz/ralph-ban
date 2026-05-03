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

### Reading output (the assertion primitive)

```bash
# Current visible content
tmux capture-pane -t "$TEST_SESSION" -p

# With scrollback (long builds overflow the default view)
tmux capture-pane -t "$TEST_SESSION" -p -S -200
```

`capture-pane` includes the echoed command line itself. Assertions must be specific enough not to match the command — prefer patterns that appear only in the *output*, not the invocation.

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
- **Don't fix anything.** Report what is broken. This skill is QA, not implementation.

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
