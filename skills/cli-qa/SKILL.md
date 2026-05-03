---
name: cli-qa
description: This skill should be used when the user asks to "QA a CLI tool", "verify a command-line program", "check the binary's output", "validate exit codes", "compare against a golden file", or wants end-to-end behavioural verification of command-line tools. Drives a binary with representative inputs, captures stdout / stderr / exit code as separate evidence channels, asserts via golden-file comparison with volatile-field redaction.
argument-hint: "[scope of the CLI change to verify]"
---

# cli-QA

Verify a command-line tool by driving the binary, capturing its three output channels separately, and comparing the captures against the asserted spec or a golden fixture. This skill drives behaviour; it does not write code.

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine which subcommands, flags, or output formats changed.

## Bundled Resources

- `references/golden-files.md` — golden-file discipline: redaction of volatile fields (timestamps, PIDs, paths, widths, ANSI), the regenerate-on-purpose env-var idiom, when to compare structurally vs textually. Load when the spec asserts byte-equality against a fixture.

## Workflow

1. Read the changeset — `git diff`, `git log` — and identify the surface that changed: subcommand, flags, input parsing, output formatting, error reporting, exit-code semantics.
2. Build the binary in the worktree (`GOWORK=off go build ./...` for Go, project's build for others).
3. For each spec asserting CLI behaviour, drive the binary with a representative input.
4. Capture stdout, stderr, and exit code to *three separate files*. Never `2>&1` for the Oracle's transcript.
5. Compare the captures against the spec — equality, regex, golden-file with redaction.
6. Persist the transcript under `.agent-history/oracle/<card-id>/<timestamp>/`.

## Driving the binary

The single most common QA mistake on CLIs is collapsing channels. The Oracle needs all three.

```bash
TXN=.agent-history/oracle/$CARD_ID/$(date +%Y%m%dT%H%M%S)
mkdir -p "$TXN"

# Three channels, three files; preserve exit code
./bin/mytool subcommand --flag value \
  > "$TXN/stdout.txt" \
  2> "$TXN/stderr.txt"
echo "$?" > "$TXN/exit.txt"
```

`echo $?` immediately after the command captures the exit code before any subsequent command can clobber it. A pipeline like `./bin/mytool | tee` swallows the exit code unless `set -o pipefail` is enabled — the Oracle's capture must not pipe.

## Exit code conventions

POSIX gives the exit code precise meaning. A CLI spec that says "shall fail" is incomplete; the Oracle reads it as "shall exit non-zero," which leaves the *which* non-zero unaddressed.

| Code | Meaning | When the spec is satisfied |
|---|---|---|
| 0 | success | when the spec asserts success |
| 1 | generic error | when the spec asserts a non-specific failure |
| 2 | misuse / usage error | when the spec asserts "shall reject invalid flags" |
| 126 | found but not executable | a setup defect, not behaviour under test |
| 127 | command not found | the binary was not built; halt the QA |
| 128 + N | killed by signal N (e.g., 130 = 128+SIGINT) | when the spec asserts signal-handling behaviour |

If the card's spec is "shall exit non-zero on bad input," ask: which non-zero? `1` for "we tried and failed" reads differently than `2` for "we wouldn't try because the request was malformed." The Oracle should record the observed code and note when the spec is too vague to verify against a specific code.

## Pipeline gotchas

`set -e` does not cause a script to fail when a command in a pipeline fails — only the *last* exit code is consulted unless `set -o pipefail` is set. For Oracle scripts that drive multi-stage pipelines, `set -euo pipefail` at the top is the discipline.

`2>&1` collapses stderr into stdout. After this, "what did the program write to stderr" is unanswerable. The Oracle's capture must keep them apart; use `2>&1` only when piping to a tool that genuinely needs the merged stream (and even then, capture the originals first).

## TTY-vs-pipe divergence

Many CLIs check `isatty(1)` and change output: colours on for TTYs and off for pipes, progress bars only when interactive, paginated output through `less` only when stdout is a terminal. The Oracle's redirected captures are *pipes*, so the captured output is the non-TTY path.

When the spec asserts behaviour the program only emits on a TTY (e.g., "shall print a progress bar"), force a PTY:

```bash
# Linux and macOS — flag spelling differs
if [[ "$OSTYPE" == darwin* ]]; then
  script -q /dev/null ./bin/mytool subcommand > "$TXN/stdout-tty.txt" 2> "$TXN/stderr.txt"
else
  script -q -c './bin/mytool subcommand' /dev/null > "$TXN/stdout-tty.txt" 2> "$TXN/stderr.txt"
fi
echo "$?" > "$TXN/exit.txt"
```

Capture the non-TTY path *and* the TTY path when both matter. The non-TTY path is what scripts and CI see; the TTY path is what humans see. The spec usually specifies one or the other.

## Signal-handling assertions

When the spec asserts shutdown behaviour ("shall exit cleanly on SIGINT"), drive the signal explicitly:

```bash
./bin/mytool serve >"$TXN/stdout.txt" 2>"$TXN/stderr.txt" &
PID=$!
sleep 0.5                              # let it bind / reach steady state
kill -INT "$PID"
wait "$PID"; CODE=$?                   # captures 130 = 128 + SIGINT
echo "$CODE" > "$TXN/exit.txt"
```

The asserted behaviour is usually some combination of: the process exited, the exit code is `130`, stderr printed a specific shutdown message, no orphan child processes remain. All four are checkable; the spec usually picks one.

## Golden-file comparison

For commands whose output is large or structurally complex, compare against a fixture rather than asserting line-by-line. The discipline that makes golden files non-flaky is *redaction* — replacing volatile fields with stable placeholders before diff. See `references/golden-files.md` for the full pattern.

```bash
# Quick recipe: redact obvious volatiles, then diff
sed -E '
  s/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.+-]+/[TIMESTAMP]/g;
  s/(pid )[0-9]+/\1[PID]/g;
  s/\/tmp\/[A-Za-z0-9._-]+/[TMPPATH]/g;
' "$TXN/stdout.txt" > "$TXN/stdout.redacted.txt"

diff -u testdata/expected.txt "$TXN/stdout.redacted.txt" > "$TXN/diff.txt" || true
```

Redact, then diff. A diff over un-redacted output finds nothing but the volatile fields, every time.

## Evidence capture

Save artefacts under `.agent-history/oracle/<card-id>/<timestamp>/`:

- `command.txt` — the exact command line invoked, one line
- `stdin.txt` — input fed to the program, if any
- `stdout.txt` — raw stdout
- `stdout.redacted.txt` — stdout after volatile-field redaction (when comparing to a golden)
- `stderr.txt` — raw stderr
- `exit.txt` — single line: the exit code as a number
- `diff.txt` — output of `diff -u golden observed`, when applicable
- `verdict.md` — APPROVE / REJECT / ESCALATE with the spec table filled in

The transcript directory is the deliverable. The reviewer reads code; the Oracle reads these files.

## Side-effect assertions

When the spec asserts effects beyond stdout/stderr/exit — a database row written, a log line emitted, a file created — the cli-qa skill covers the *invocation* half. Delegate the assertion half:

- Database state → `db-state-qa` skill (snapshot before, snapshot after, structural diff with volatile-field normalisation).
- Log content → `log-tail-qa` skill (bounded wait for pattern with history/occurrence semantics).
- File-system state → check structurally with `find` + `stat`; no dedicated skill yet.

Link the side-effect transcript from this skill's `verdict.md`.

## Rules

- **Three channels, three files.** Never `2>&1` in the capture. Stderr carries information the spec may assert.
- **Capture exit code immediately.** `echo $?` on the next line. Pipelines and chained commands clobber it.
- **Build the binary the Oracle exercises.** `GOWORK=off` for Go in worktrees. A stale binary makes the verdict meaningless.
- **Force a PTY when the spec asserts TTY-mode output.** `script -q` on macOS, `script -q -c` on Linux. Capture both paths when both matter.
- **Reproduce signal behaviour explicitly.** Send the signal; wait; capture the code. Implicit shutdowns (SIGTERM at end of pipeline) are different from asserted SIGINT handling.
- **Redact before diff.** Timestamps, PIDs, temp paths, terminal widths, ANSI escapes. The redaction list belongs in `references/golden-files.md`.
- **Regenerate goldens deliberately.** A golden updated to match observed output is no longer a spec — it is a record. The Oracle does not regenerate; it reports mismatch and lets the worker decide.
- **Don't fix anything.** Report what's broken. This skill is QA, not implementation.

## Report Format

```
## CLI QA Report

**Scope**: <which subcommands / flags verified>
**Verdict**: APPROVE | REJECT | ESCALATE

### Build
- [PASS/FAIL] `<build command>` — <notes>

### Channels
| Trial | stdout | stderr | exit |
|---|---|---|---|
| 1 | <one-line summary or "see file"> | <same> | <code> |

### Specifications Verified
| Spec # | Assertion | Verified by | Verdict |
|--------|-----------|-------------|---------|
| 1 | (paste from bl show) | (file path / diff) | satisfied / unsatisfied / could-not-determine |

### Findings
1. <description with reproduction command and evidence path>

### Transcript
Path: `.agent-history/oracle/<card-id>/<timestamp>/`
Contents: <brief listing>
```
