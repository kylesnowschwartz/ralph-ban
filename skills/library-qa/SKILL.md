---
name: library-qa
description: This skill should be used when the user asks to "QA a library", "verify the package", "check the API behaves", "exercise the module", or wants end-to-end behavioural verification of an importable library or package (Go, Ruby, TypeScript, etc.). Writes a one-shot consumer program in scratch space, runs it against the library under test, observes structured output, and judges whether the behaviour matches the card's spec.
argument-hint: "[scope of the library change to verify]"
---

# library-QA

Verify a library by writing a small consumer that imports it, calls the API, and prints structured output. The Oracle runs the consumer, parses the output, and judges whether the observed behaviour matches the spec.

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine which exported symbols, types, or behaviours changed.

## Bundled Resources

The language-specific gotchas live in three sibling references; load whichever matches the library under test:

- `references/go.md` — Go scratch programs: `GOWORK=off` for worktrees, `go run ./scratch/`, single-file consumer pattern, structured-output via `encoding/json`.
- `references/ruby.md` — Ruby probes: `bin/rails runner` vs `bundle exec ruby -e`, autoload boundaries, structural printing.
- `references/ts.md` — TypeScript probes: `tsx --no-cache`, ESM-vs-CJS hazard, tsconfig-paths, `import` paths under `noEmit`.

## Workflow

1. Read the changeset — `git diff`, `git log` — and identify the surface that changed: exported symbol(s), type signatures, observable behaviour.
2. For each spec asserting library behaviour, write a small consumer in `.agent-history/oracle/<card-id>/scratch/` (scratch space, never source). The consumer imports the library, calls the API, and prints structured output.
3. Run the consumer in the worktree using the language-appropriate runner. Capture stdout, stderr, exit.
4. Parse the structured output and compare to the spec.
5. Persist the transcript under `.agent-history/oracle/<card-id>/<timestamp>/`.

## Why scratch space, not source

The Oracle exercises the system the worker built. Writing a probe into `pkg/foo/foo_test.go` modifies the worker's branch — at best it confuses the reviewer, at worst it silently re-fixtures the tests. The scratch program lives under `.agent-history/oracle/<card-id>/scratch/` and exists only for the duration of this Oracle exercise. It does not commit, does not run in CI, and does not enter the diff.

## Lifecycle: boot the world before probing

For libraries that touch a database, file system, or network, the probe is not the whole exercise — it requires setup. The pattern is *boot the world, then probe it*, lifted from `anthropics/skills/webapp-testing/scripts/with_server.py`:

```bash
# 1. Boot whatever the library needs (DB, fixture file, embedded server)
./oracle/boot.sh > "$TXN/boot.log" 2>&1
trap './oracle/teardown.sh' EXIT

# 2. Run the probe
<runner> ./oracle/scratch/probe.<ext> > "$TXN/stdout.txt" 2> "$TXN/stderr.txt"
echo "$?" > "$TXN/exit.txt"

# 3. teardown.sh runs automatically via trap
```

A probe that hits `nil` because the database wasn't migrated tells the Oracle nothing about the library. Setup failure must be distinguished from spec failure; the boot script's exit code is the discriminator.

## Structured probe output

A probe that prints free text is fragile — the Oracle's parser becomes a regex of last resort. Print structured output instead. Two acceptable forms:

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

The discipline ("structured envelope keeps what the probe *observed* separable from what it *printed for humans*") is the lesson `anthropics/skills/mcp-builder/scripts/evaluation.py` encodes — the upstream uses XML-tagged blocks (`<summary>`, `<feedback>`, `<response>`); this skill prefers JSON because shell tools (`jq`) read it natively. The format is different; the separation discipline is the same. The Oracle's verdict reads the envelope, not the trace.

## Side-effect isolation

A probe that mutates shared state is anti-evidence. If the library's API has side effects (writes a row, sends a request, mutates a global), the probe must:

- Use a fixture or transactional scope when one exists.
- Run in a temp directory when the library writes files.
- Reset the global after the probe (or note in the verdict that the probe leaks).
- Document any leak under `## Unresolved` so the next Oracle exercise knows the starting state.

For database side effects specifically, consult `db-state-qa` for the assertion side; this skill covers the *probing* half.

## Evidence capture

Save artefacts under `.agent-history/oracle/<card-id>/<timestamp>/`:

- `scratch/probe.<ext>` — the consumer source (kept; useful for the verdict reader)
- `boot.log` — output of any setup script
- `stdout.txt` — probe stdout (the structured envelope is in here)
- `stderr.txt` — probe stderr
- `exit.txt` — single line: probe exit code
- `parsed.json` — the structured envelope after extraction (`jq` or equivalent)
- `verdict.md` — APPROVE / REJECT / ESCALATE with the spec table filled in

The probe source is part of the transcript. A future reader of the verdict needs to see what was tried.

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
