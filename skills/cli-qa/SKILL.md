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

A CLI spec that says "shall fail" is incomplete; the Oracle reads it as "shall exit non-zero," which leaves the *which* non-zero unaddressed.

The shell-level conventions (`126`, `127`, `128+N`) are POSIX-defined; `0` and any specific small non-zero code (`1`, `2`, etc.) are *program-defined* — different tools use them differently. `git` uses `1` for "merge conflict" and `128` for "fatal error"; `grep` uses `1` for "no match" (a non-error). The Oracle treats an exit code as a fact to record, not a category to interpret, unless the program documents its codes.

| Code | Defined by | What it tells the Oracle |
|---|---|---|
| 0 | program | exit was graceful — the program decided everything succeeded |
| 1, 2, 3, … | program | program-specific; check the program's docs/man page before interpreting |
| 126 | shell | command was found but not executable — a setup defect, not behaviour under test |
| 127 | shell | command was not found — the binary was not built; halt the QA |
| 128 + N | shell | the program was killed by signal N (e.g., 130 = 128 + SIGINT, 137 = 128 + SIGKILL) |

If the card's spec is "shall exit non-zero on bad input," ask the planner which code, or note in the verdict that the spec is too vague to verify against a specific code.

## Pipeline gotchas

`2>&1` collapses stderr into stdout — after this, "what did the program write to stderr" is unanswerable. Keep them apart in the Oracle's capture. For multi-stage pipelines, `set -euo pipefail` at the top of the script makes failures in any stage propagate; without `pipefail`, only the last stage's exit is consulted.

## Environment that warps output

CLIs check more than `isatty(1)` to decide what to print. The Oracle should fix the obvious env-var levers for reproducibility:

| Variable | Effect when unset / default | What to set for deterministic capture |
|---|---|---|
| `TERM` | colour, cursor codes vary | `TERM=dumb` for plainest output |
| `NO_COLOR` | many tools respect this | `NO_COLOR=1` to disable colour even on TTYs |
| `CI` | many tools change output for CI | `CI=` (unset) or `CI=1` per intent |
| `LC_ALL` / `LANG` | number / date format vary | `LC_ALL=C` |
| `TZ` | local-time output drifts | `TZ=UTC` |
| `PAGER` | tools like `git log` paginate | `PAGER=cat` to disable |
| `COLUMNS` | line-wrapping width | `COLUMNS=80` for stable wraps |

`env -i HOME=$HOME PATH=$PATH TERM=dumb LC_ALL=C TZ=UTC ./bin/mytool ...` is the heavy form when the spec asserts byte-exact output.

## TTY-vs-pipe divergence

Many CLIs check `isatty(1)` and change output: colours on for TTYs and off for pipes, progress bars only when interactive, paginated output through `less` only when stdout is a terminal. The Oracle's redirected captures are *pipes*, so the captured output is the non-TTY path.

When the spec asserts behaviour the program only emits on a TTY (e.g., "shall print a progress bar"), force a PTY with `script`. Note three caveats:

- `script` does *not* preserve separate stdout and stderr; under a PTY both flow through the single terminal channel. A `2>` redirect on `script` captures `script`'s own stderr, not the program's. Pick: TTY mode (one merged transcript) *or* separate-channel mode (no PTY) — you cannot have both from `script` alone. For separate channels under a PTY, use a tool like `socat` or run the program twice.
- The PTY transcript carries CR (`\r`), backspace, and other terminal control bytes the underlying program emits. Goldens against PTY output need an additional normalisation pass (`tr -d '\r'`, ANSI strip, control-byte filter).
- `script` flag spelling differs by platform:

```bash
if [[ "$OSTYPE" == darwin* ]]; then
  script -q /dev/null ./bin/mytool subcommand > "$TXN/transcript.txt"
else
  script -q -c './bin/mytool subcommand' /dev/null > "$TXN/transcript.txt"
fi
echo "$?" > "$TXN/exit.txt"
# transcript.txt contains merged stdout+stderr with terminal control bytes
```

Capture the non-TTY path *and* the PTY transcript when both matter. The non-TTY path is what scripts and CI see; the PTY path is what humans see. The spec usually specifies one or the other.

## Signal-handling assertions

When the spec asserts shutdown behaviour ("shall exit cleanly on SIGINT"), drive the signal explicitly:

```bash
./bin/mytool serve >"$TXN/stdout.txt" 2>"$TXN/stderr.txt" &
PID=$!

# Poll for readiness — sleep is flaky for slow-starting binaries
for i in $(seq 1 60); do
  if grep -q 'listening' "$TXN/stderr.txt" 2>/dev/null; then break; fi
  sleep 0.1
done

kill -INT "$PID"
wait "$PID"; CODE=$?
echo "$CODE" > "$TXN/exit.txt"
```

If the binary handles SIGINT and exits cleanly with its own non-zero code, `wait` returns that code. If the binary does *not* handle SIGINT and the kernel terminates it, `wait` returns `130` (= 128 + SIGINT). Don't claim "expect 130" without naming which case you're testing — the spec must specify "shall exit 0 on SIGINT" or "shall exit 130 (uncaught SIGINT)" for the verdict to be unambiguous. The asserted behaviour is usually some combination of: the process exited, the exit code matches the spec, stderr printed a shutdown message, no orphan child processes remain.

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
