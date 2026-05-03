---
name: log-tail-qa
description: This skill should be used when the user asks to "verify a log line appears", "wait for the log message", "check the audit log", "assert async behaviour from the log", or wants behavioural verification of asynchronous side effects observable only through a log file or stream. Tails a log file with bounded waiting, asserts pattern presence with history-vs-tail semantics and occurrence counts, fails fast when the log stops growing. Used by primary-surface oracles (http-qa, cli-qa, library-qa) when a card asserts log-emission behaviour.
argument-hint: "[scope of the log-emitted behaviour to verify]"
---

# log-tail-QA

Verify behaviour observable only in a log file by tailing the log under bounded waiting, asserting pattern presence, and recording the surrounding context as evidence. This skill is *not* a primary surface in the Oracle's `kind:` taxonomy; it is a side-effect oracle invoked from `http-qa`, `cli-qa`, or `library-qa` when a card's spec asserts "after this action, a log line shall appear matching ...".

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine which log-emitting paths the change touches.

## Why this skill exists

Some behaviours are observable only through a log: an async job acknowledging receipt, a background worker reporting progress, an audit trail recording a privileged operation. The action under test runs synchronously; the log line appears later, sometimes much later, sometimes only on a different process's stdout that is being multiplexed into a file.

The naive bash idiom — `tail -f file | grep PATTERN` — is wrong in three ways. It can hang forever (no timeout). It races log rotation (`-f` does not re-open). It does not say whether the pattern's absence means "not yet" or "never." This skill encodes the discipline that the testcontainers projects have already learned and ported into shell.

The corpus lifted here is `testcontainers/testcontainers-go/wait/log.go` and `testcontainers/testcontainers-java/.../LogMessageWaitStrategy.java` (both MIT). They encode three behaviours that bash idioms typically miss; this skill ports them.

## The three lifted behaviours

**1. History inclusion as an explicit knob.** The asserted line may already exist when watching starts (the action wrote it before the watcher attached) or it may be appended fresh. Treating these the same hides bugs. Testcontainers names this `withSince(0)`; in bash, `tail -n +1 -F` includes existing content, `tail -n 0 -F` skips it.

**2. Occurrence count.** A spec asserting "shall log 3 times" is satisfied by 3 matches, not 1. Testcontainers names this `withTimes(N)`; `grep -m1` exits at the first match and cannot count higher. The bash form is an awk accumulator that exits when the count threshold is reached.

**3. No-progress fail-fast.** If the log file stops growing entirely and the pattern has not matched, waiting longer is futile. Testcontainers tracks `lastLen` and fails the wait when both conditions hold: file size unchanged across N polls, and the latest check still mismatches. The bash equivalent is `stat -c %s` (Linux) / `stat -f %z` (macOS) between polls.

## Workflow

1. Identify the log file. If the system under test logs to stdout/stderr from a process the Oracle started, tee it to a temp file: `command 2>&1 | tee "$TXN/process.log" &`.
2. Decide history mode: `+1` (include) or `0` (skip) based on whether the action runs before or after the watcher attaches.
3. Set a timeout based on how long the asserted behaviour realistically takes (default 30s; longer for batch jobs).
4. Run the bounded-wait helper.
5. Persist the matched lines and the surrounding tail to the transcript.

## Bash primitives

### Bounded wait for a single match

```bash
LOG="$1"
PATTERN="$2"
TIMEOUT="${3:-30}"

# macOS lacks `timeout`; coreutils ships `gtimeout`
TIMEOUT_BIN="timeout"
command -v gtimeout >/dev/null && TIMEOUT_BIN="gtimeout"

# Include history; -F re-opens on rotation; -m1 exits at first match
"$TIMEOUT_BIN" "$TIMEOUT" tail -n +1 -F "$LOG" | grep -m1 -E "$PATTERN"
RC=$?
case $RC in
  0)   echo "match" ;;
  124) echo "timeout"; exit 1 ;;
  *)   echo "error rc=$RC"; exit 1 ;;
esac
```

The `124` exit code from `timeout` is the load-bearing detail — it is the standard "timed out" sentinel and distinguishes a timeout from a grep failure.

### Bounded wait for N occurrences

```bash
wait_for_n() {
  local log=$1 pattern=$2 want=$3 timeout=${4:-30}
  local timeout_bin=timeout
  command -v gtimeout >/dev/null && timeout_bin=gtimeout

  "$timeout_bin" "$timeout" \
    awk -v p="$pattern" -v want="$want" '
      $0 ~ p { n++; print; if (n >= want) exit 0 }
    ' < <(tail -n +1 -F "$log")
}
```

`awk` accumulates matches until the count threshold; `exit 0` from awk closes the pipeline cleanly. Exit code `124` from `timeout` means the threshold was not reached in time.

### No-progress fail-fast

```bash
wait_with_progress_check() {
  local log=$1 pattern=$2 timeout=${3:-30} stall_polls=${4:-5}
  local interval=0.5
  local elapsed=0 last_size=-1 same_size=0

  while [ "$(awk "BEGIN{print ($elapsed < $timeout)}")" = 1 ]; do
    if grep -qE "$pattern" "$log" 2>/dev/null; then
      echo "match"
      return 0
    fi

    local size
    if [[ "$OSTYPE" == darwin* ]]; then
      size=$(stat -f %z "$log" 2>/dev/null || echo -1)
    else
      size=$(stat -c %s "$log" 2>/dev/null || echo -1)
    fi

    if [ "$size" = "$last_size" ]; then
      same_size=$((same_size + 1))
      if [ "$same_size" -ge "$stall_polls" ]; then
        echo "no-progress: file size $size unchanged across $stall_polls polls"
        return 2
      fi
    else
      same_size=0
      last_size=$size
    fi

    sleep "$interval"
    elapsed=$(awk "BEGIN{print $elapsed + $interval}")
  done

  echo "timeout"
  return 1
}
```

Three return codes: `0` matched, `1` time budget exhausted, `2` file stopped growing. The Oracle's verdict reads `2` as "the system stopped writing logs entirely" — usually the action crashed, sometimes the log rotated to a path the watcher does not follow.

## tail flag survey

The flags matter; getting them wrong silently produces wrong answers.

| Flag | Means | When |
|---|---|---|
| `-f` | follow appends; *does not* re-open on rename/truncate | only if you control the writer and rotation will not happen |
| `-F` | follow appends *and* re-open on rotation | the default for the Oracle |
| `-n 0` | skip existing content; only follow new appends | when the action writes after the watcher attaches |
| `-n +1` | include from line 1; follow new appends | when the action may have already written |
| `--retry` (GNU) | keep trying when the file does not yet exist | when the file is created by the action itself |

For files written by long-running services, `-F` is the right default. For files written by a short-lived process the Oracle started, `-n +1 -F` covers both "it wrote before tail attached" and "it wrote during tail."

## Buffering

Many programs buffer stdout when piped; the line the spec asserts may be sitting in a 4 KB buffer that never flushes if the program exits cleanly without further writes.

| Tool | Forces line-buffered output |
|---|---|
| `stdbuf -o0 -e0 cmd` | unbuffer stdout and stderr (GNU coreutils) |
| `unbuffer cmd` | full PTY emulation (from `expect`) |
| `script -q /dev/null cmd` | PTY via the script utility |

The Oracle's bounded-wait works with whatever the program produces; if the assertion is failing because the program buffered, the test is observing buffering, not behaviour. Wrap the writer in one of the above when the spec depends on lines emitted *during* execution rather than only at exit.

## Multi-line and structured log entries

JSON-structured logs and stack traces span multiple physical lines. `grep` matches per line; a pattern that spans lines does not match.

For JSON: normalise to one entry per line with `jq -c` first, then `grep` over the normalised file:

```bash
jq -c . "$LOG" > "$TXN/log.ndjson" 2>/dev/null
grep -E '"event":"widget_created"' "$TXN/log.ndjson"
```

For stack traces (multi-line plaintext): grep with `-A N` (after-context) and `-B N` (before-context) to capture the surrounding frame; or use `awk` with a multi-line state machine.

The asserted *content* drives the technique: a structured log with a stable schema is best asserted via `jq` predicates; an unstructured log is best asserted with `grep -E` and context flags.

## Evidence capture

Save under `.agent-history/oracle/<card-id>/<timestamp>/`:

- `log.path.txt` — single line: the absolute path of the log being watched
- `pattern.txt` — single line: the asserted regex pattern
- `match.txt` — the matched lines (may be empty if timeout or no-progress)
- `tail-context.txt` — last 200 lines of the log at the moment of verdict, regardless of match
- `exit.txt` — single line: 0 / 1 / 2 / other (from the bash helpers above)
- `verdict.md` — APPROVE / REJECT / ESCALATE with the spec table filled in

The `tail-context.txt` is non-obvious. Even when the assertion succeeds, the surrounding 200 lines tell the verdict reader what *else* the system was doing — useful when the assertion is a one-line confirmation and the interesting story is in the 30 lines around it.

## Rules

- **Always set a timeout.** `timeout` on Linux, `gtimeout` on macOS via `brew install coreutils`. Without it, the wait is unbounded.
- **Default to `tail -F`, not `-f`.** Rotation happens during long-running waits; `-F` re-opens, `-f` silently stops following.
- **Choose history mode deliberately.** `-n +1 -F` includes existing lines; `-n 0 -F` skips them. The wrong choice hides bugs in either direction.
- **Use awk for occurrence count.** `grep -m1` only matches once. For "shall log N times," accumulate in awk and exit at the threshold.
- **Fail fast on no-progress.** Stat the file size between polls; if it has not grown across N polls and the pattern still does not match, the system has stopped writing. Three return codes: matched, timed out, no-progress.
- **Defeat buffering at the writer.** `stdbuf -o0`, `unbuffer`, or `script -q`. If the line never flushes, no amount of waiting helps.
- **Normalise structured logs before grep.** `jq -c` for JSON; `awk` state machines for multi-line plaintext.
- **Capture surrounding context.** `tail-context.txt` is part of the transcript even when the assertion succeeds; it is the difference between "the line appeared" and "the line appeared in a sensible context."
- **Don't fix anything.** Report what's broken. This skill is QA, not implementation.

## Report Format

```
## Log-Tail QA Report

**Scope**: <what log-emitted behaviour was verified>
**Log path**: <absolute path>
**Verdict**: APPROVE | REJECT | ESCALATE

### Wait
- Pattern: `<regex>`
- Timeout: <seconds>
- History mode: include / skip
- Occurrence threshold: <N>
- Result: matched / timeout / no-progress

### Specifications Verified
| Spec # | Predicate | Verified by | Verdict |
|--------|-----------|-------------|---------|
| 1 | (paste from bl show) | (matched line / awk count) | satisfied / unsatisfied / could-not-determine |

### Findings
1. <description with reproduction command and evidence path>

### Transcript
Path: `.agent-history/oracle/<card-id>/<timestamp>/`
Contents: <brief listing>
```
