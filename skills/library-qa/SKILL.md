---
name: library-qa
description: This skill should be used when the user asks to "QA a library", "verify the package", "check the API behaves", "exercise the module", or wants end-to-end behavioural verification of an importable library or package (Go, Ruby, TypeScript, etc.). Writes a one-shot consumer program in scratch space, runs it against the library under test, observes structured output, and judges whether the behaviour matches the card's spec.
argument-hint: "[scope of the library change to verify]"
---

# library-QA

Write a small consumer that imports the library, calls the API, prints structured output. Run it, parse, judge.

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine which exported symbols, types, or behaviours changed.

## Bundled Resources

Load whichever matches the library under test:

- `references/go.md` — `GOWORK=off`, `go run ./scratch/`, JSON envelope via `encoding/json`.
- `references/ruby.md` — `bin/rails runner` vs `bundle exec ruby -e`, autoload boundaries.
- `references/ts.md` — `tsx --no-cache`, ESM-vs-CJS hazard, tsconfig path mapping.

## Workflow

1. Read the changeset — `git diff`, `git log` — and identify the surface that changed: exported symbol(s), type signatures, observable behaviour.
2. For each spec asserting library behaviour, write a small consumer in `.agent-history/oracle/<card-id>/scratch/` (scratch space, never source). The consumer imports the library, calls the API, and prints structured output.
3. Run the consumer in the worktree using the language-appropriate runner. Capture stdout, stderr, exit.
4. Parse the structured output and compare to the spec.
5. Persist the transcript under `.agent-history/oracle/<card-id>/<timestamp>/`.

## Lifecycle: boot the world before probing

Libraries that touch a database, file system, or network need setup before the probe runs:

```bash
# 1. Boot whatever the library needs (DB, fixture file, embedded server)
./oracle/boot.sh > "$TXN/boot.log" 2>&1
trap './oracle/teardown.sh' EXIT

# 2. Run the probe
<runner> ./oracle/scratch/probe.<ext> > "$TXN/stdout.txt" 2> "$TXN/stderr.txt"
echo "$?" > "$TXN/exit.txt"

# 3. teardown.sh runs automatically via trap
```

Setup failure is distinct from spec failure; the boot script's exit code is the discriminator. A probe that hits `nil` because the DB wasn't migrated tells the Oracle nothing about the library.

## Structured probe output

Free-text output is fragile — the parser becomes a regex of last resort. Two acceptable forms:

**One JSON object per line (newline-delimited JSON, NDJSON):**

```
{"event":"input","value":"abc"}
{"event":"result","ok":true,"id":42}
{"event":"observation","metric":"latency_ms","value":12}
```

**Single JSON object at the end:**

```
... free-form trace lines ...
{"summary":{"id":42,"ok":true,"errors":[]}}
```

Keep what the probe *observed* separable from what it printed for humans. The verdict reads the envelope, not the trace.

## Side-effect isolation

A probe that mutates shared state is anti-evidence. When the library's API has side effects:

- Use a fixture or transactional scope where one exists.
- Run in a temp directory when the library writes files.
- Reset the global after the probe, or note the leak in the verdict.

For database side effects, delegate the assertion side to `db-state-qa`.

## Evidence capture

Save under `.agent-history/oracle/<card-id>/<timestamp>/`:

- `scratch/probe.<ext>` — the consumer source
- `boot.log` — output of any setup script
- `stdout.txt` / `stderr.txt` — probe channels
- `exit.txt` — probe exit code
- `parsed.json` — structured envelope after `jq` extraction
- `verdict.md` — APPROVE / REJECT / ESCALATE

## Rules

- **Probe in scratch, not in source.** `.agent-history/oracle/<card-id>/scratch/`. The probe is not part of the worker's diff.
- **Boot the world before probing.** Setup failure is distinct from spec failure; record both with separate exit codes.
- **Print structured output.** NDJSON or a final summary object. Free text invites parser fragility.
- **Capture three channels.** Same discipline as `cli-qa` — stdout, stderr, exit, separate.
- **Honor language-specific runners.** `GOWORK=off` for Go in worktrees, `bin/rails runner` for Rails-loaded code, `tsx --no-cache` for TS. Each language's reference file names the gotcha.
- **Isolate side effects, or document the leak.** A probe that mutates shared state without restoration distorts the next exercise.
## Report Format

```
## Library QA Report

**Scope**: <which library / which exported surface>
**Language**: Go | Ruby | TypeScript | <other>
**Verdict**: APPROVE | REJECT | ESCALATE

### Probe
- Path: `.agent-history/oracle/<card-id>/scratch/probe.<ext>`
- Runner: `<command used>`
- Boot: PASS/FAIL — `<boot.sh exit>`

### Specifications Verified
| Spec # | Probe step | Observed | Verdict |
|--------|------------|----------|---------|
| 1 | (paste from bl show) | (parsed envelope field) | satisfied / unsatisfied / could-not-determine |

### Findings
1. <description with reproduction command and evidence path>

### Transcript
Path: `.agent-history/oracle/<card-id>/<timestamp>/`
Contents: <brief listing>
```
