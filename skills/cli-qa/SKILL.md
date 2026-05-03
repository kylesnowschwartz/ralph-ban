---
name: cli-qa
description: This skill should be used when the user asks to "QA a CLI tool", "verify a command-line program", "check the binary's output", "validate exit codes", "compare against a golden file", or wants end-to-end behavioural verification of command-line tools. Drives a binary with representative inputs, captures stdout / stderr / exit code as separate evidence channels, asserts via golden-file comparison with volatile-field redaction.
argument-hint: "[scope of the CLI change to verify]"
---

# cli-QA

Drive the binary, capture stdout / stderr / exit separately, compare against the spec or a golden fixture.

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine which subcommands, flags, or output formats changed.

## Bundled Resources

- `references/golden-files.md` — golden-file redaction (timestamps, PIDs, paths, widths, ANSI) and regenerate-on-purpose idiom. Load when the spec asserts byte-equality against a fixture.

## Workflow

1. Read the changeset — `git diff`, `git log` — and identify the surface that changed: subcommand, flags, input parsing, output formatting, error reporting, exit-code semantics.
2. Build the binary in the worktree (`GOWORK=off go build ./...` for Go, project's build for others).
3. For each spec asserting CLI behaviour, drive the binary with a representative input.
4. Capture stdout, stderr, and exit code to *three separate files*. Never `2>&1` for the Oracle's transcript.
5. Compare the captures against the spec — equality, regex, golden-file with redaction.
6. Persist the transcript under `.agent-history/oracle/<card-id>/<timestamp>/`.

## Driving the binary

```bash
TXN=.agent-history/oracle/$CARD_ID/$(date +%Y%m%dT%H%M%S)
mkdir -p "$TXN"

# Three channels, three files; preserve exit code
./bin/mytool subcommand --flag value \
  > "$TXN/stdout.txt" \
  2> "$TXN/stderr.txt"
echo "$?" > "$TXN/exit.txt"
```

`echo $?` captures the exit before anything else clobbers it. Pipelines clobber `$?` unless `set -o pipefail`.

## Exit code conventions

`126`, `127`, `128+N` are POSIX-defined. `0`, `1`, `2` are *program-defined* and mean different things in different tools (`git` uses `1` for merge conflict; `grep` uses `1` for "no match"). Record the exit code as a fact; interpret only when the program documents its codes.

| Code | Defined by | What it tells the Oracle |
|---|---|---|
| 0 | program | exit was graceful — the program decided everything succeeded |
| 1, 2, 3, … | program | program-specific; check the program's docs/man page before interpreting |
| 126 | shell | command was found but not executable — a setup defect, not behaviour under test |
| 127 | shell | command was not found — the binary was not built; halt the QA |
| 128 + N | shell | the program was killed by signal N (e.g., 130 = 128 + SIGINT, 137 = 128 + SIGKILL) |

If the spec says "shall exit non-zero on bad input" without naming a code, mark `could-not-determine` and surface to the planner.

## Pipeline gotchas

`2>&1` collapses stderr into stdout — after that, "what went to stderr" is unanswerable. For multi-stage pipelines, `set -euo pipefail` makes any-stage failures propagate; without it, only the last stage's exit is consulted.

## Environment that warps output

CLIs check more than `isatty(1)`. Fix env-var levers for reproducibility:

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

Many CLIs check `isatty(1)` and change output: colours, progress bars, pagination. The Oracle's redirected captures are pipes, so they record the non-TTY path. To assert TTY-only behaviour, force a PTY with `script`:

- `script` merges stdout and stderr under the PTY — separate channels are not recoverable. Pick TTY mode *or* separate channels.
- PTY transcripts carry `\r`, backspace, and ANSI control bytes; goldens need normalisation (`tr -d '\r'`, ANSI strip).
- Flag spelling differs by platform:

```bash
if [[ "$OSTYPE" == darwin* ]]; then
  script -q /dev/null ./bin/mytool subcommand > "$TXN/transcript.txt"
else
  script -q -c './bin/mytool subcommand' /dev/null > "$TXN/transcript.txt"
fi
echo "$?" > "$TXN/exit.txt"
```

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

If the binary handles SIGINT and exits with its own code, `wait` returns that code. If it doesn't and the kernel kills it, `wait` returns `130` (= 128 + SIGINT). The spec must say which case ("shall exit 0 on SIGINT" or "shall exit 130 uncaught") for the verdict to be unambiguous.

## Golden-file comparison

For large or structurally complex output, compare against a fixture. Redaction (replacing volatiles with stable placeholders) is what makes golden files non-flaky. See `references/golden-files.md`.

```bash
# Quick recipe: redact obvious volatiles, then diff
sed -E '
  s/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.+-]+/[TIMESTAMP]/g;
  s/(pid )[0-9]+/\1[PID]/g;
  s/\/tmp\/[A-Za-z0-9._-]+/[TMPPATH]/g;
' "$TXN/stdout.txt" > "$TXN/stdout.redacted.txt"

diff -u testdata/expected.txt "$TXN/stdout.redacted.txt" > "$TXN/diff.txt" || true
```

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

## Side-effect assertions

When the spec asserts effects beyond stdout/stderr/exit, delegate:

- Database → `db-state-qa` (snapshot before/after, structural diff).
- Log content → `log-tail-qa` (bounded wait for pattern).
- File-system → `find` + `stat`.

Link the side-effect transcript from `verdict.md`.

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
