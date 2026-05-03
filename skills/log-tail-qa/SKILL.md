---
name: log-tail-qa
description: This skill should be used when the user asks to "verify a log line appears", "wait for the log message", "check the audit log", "assert async behaviour from the log", or wants behavioural verification of asynchronous side effects observable only through a log file or stream. Tails a log file with bounded waiting, asserts pattern presence with history-vs-tail semantics and occurrence counts, fails fast when the log stops growing. Used by primary-surface oracles (http-qa, cli-qa, library-qa) when a card asserts log-emission behaviour.
argument-hint: "[scope of the log-emitted behaviour to verify]"
---

# log-tail-QA

Tail a log under bounded waiting, assert pattern presence, record surrounding context. Side-effect oracle invoked from `http-qa`, `cli-qa`, or `library-qa` when a spec asserts "after this action, a log line shall appear matching ...".

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine which log-emitting paths the change touches.

## Three behaviours bash idioms typically miss

**History inclusion.** The asserted line may already exist when watching starts, or appear fresh. `tail -n +1 -F` includes existing content; `tail -n 0 -F` skips it.

**Occurrence count.** "Shall log 3 times" is satisfied by 3 matches, not 1. `grep -m1` exits at the first match and cannot count higher; use an awk accumulator.

**No-progress fail-fast.** If the file stops growing and the pattern still does not match, waiting longer is futile. Stat the size between polls (`stat -c %s` Linux, `stat -f %z` macOS); fail when size is unchanged across N polls.

## Workflow

1. Identify the log source. Three common shapes:
   - **File** — the system writes to a file; tail it directly (`tail -n +1 -F /var/log/foo.log`).
   - **Process stdout/stderr** — the Oracle started the process; tee it (`command 2>&1 | tee "$TXN/process.log" &`).
   - **Service journal or container** — the system writes to systemd-journal or a Docker container; substitute the file-tail step with `journalctl -f -u service` or `docker logs -f --tail=0 <container>`. The `journalctl` and `docker logs` commands stream like `tail -F` but get their own gotchas (`journalctl --since=now -f`, `docker logs --tail=N`); the bounded-wait helpers below work the same way over their stdout.
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

# Include history; -F re-opens on rotation; -m1 exits at first match.
# Use PIPESTATUS so we read tail|timeout's exit, not grep's.
set +o pipefail
"$TIMEOUT_BIN" "$TIMEOUT" tail -n +1 -F "$LOG" | grep -m1 -E "$PATTERN"
TAIL_RC=${PIPESTATUS[0]}
GREP_RC=${PIPESTATUS[1]}

if [ "$GREP_RC" = 0 ]; then
  echo "match"
elif [ "$TAIL_RC" = 124 ]; then
  echo "timeout"
  exit 1
else
  echo "error tail_rc=$TAIL_RC grep_rc=$GREP_RC"
  exit 1
fi
```

`$?` after a pipeline returns grep's exit, not timeout's — `PIPESTATUS[0]` gives tail/timeout, `[1]` gives grep. `124` is the standard timed-out sentinel for both `timeout` and `gtimeout`.

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

Counts matching *lines*, not occurrences within a line. If the spec asserts substring count where occurrences may share a line, replace awk with `grep -oE "$pattern" | wc -l` against a captured snapshot.

### No-progress fail-fast

```bash
wait_with_progress_check() {
  local log=$1 pattern=$2 timeout=${3:-30} stall_polls=${4:-5}
  local interval=0.5
  local elapsed=0 last_size=-2 same_size=0   # -2 distinguishes "not yet seen" from "stat failed"

  while [ "$(awk "BEGIN{print ($elapsed < $timeout)}")" = 1 ]; do
    if grep -qE "$pattern" "$log" 2>/dev/null; then
      echo "match"
      return 0
    fi

    local size
    if [[ "$OSTYPE" == darwin* ]]; then
      size=$(stat -f %z "$log" 2>/dev/null || echo "missing")
    else
      size=$(stat -c %s "$log" 2>/dev/null || echo "missing")
    fi

    # Don't trip the stall counter on a missing-file poll; the file may not exist yet
    if [ "$size" = "missing" ]; then
      sleep "$interval"
      elapsed=$(awk "BEGIN{print $elapsed + $interval}")
      continue
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

Return codes: `0` matched, `1` timed out, `2` file stopped growing (action crashed, or log rotated to an unfollowed path). `stall_polls=5` (≈2.5s) tolerates one or two repeat-size polls during normal JIT flushing; lower it for stricter behaviour.

## tail flag survey

| Flag | Means | When |
|---|---|---|
| `-f` | follow appends; does *not* re-open on rename/truncate | only if no rotation can happen |
| `-F` | follow appends *and* re-open on rotation | default for the Oracle |
| `-n 0` | skip existing; follow new appends | action writes after watcher attaches |
| `-n +1` | include from line 1; follow new appends | action may have already written |
| `--retry` (GNU) | keep trying when file does not exist yet | action creates the file |

## Buffering

If the line never appears, the program may be full-buffering its stdout on a pipe. Force line-buffering at the writer:

| Tool | Behaviour |
|---|---|
| `stdbuf -o0 -e0 cmd` | unbuffered (GNU coreutils; not macOS default) |
| `stdbuf -oL -eL cmd` | line-buffered (usually what you want) |
| `unbuffer cmd` (`expect`) | runs under a PTY; program sees TTY and chooses line buffering |
| `script -q /dev/null cmd` | same effect via `script` |

`stdbuf` overrides the stdio default; programs that explicitly call `setvbuf` ignore it.

## Multi-line and structured logs

`grep` matches per line; patterns spanning lines do not match.

For JSON, normalise with `jq -c` and check its exit (a casual `2>/dev/null` hides partial-conversion failures):

```bash
if ! jq -c . "$LOG" > "$TXN/log.ndjson" 2> "$TXN/jq_errors.txt"; then
  echo "WARN: log contains non-JSON lines; partial conversion in log.ndjson" >&2
fi
grep -E '"event":"widget_created"' "$TXN/log.ndjson"
```

For mixed JSON/text, `jq -c -R 'fromjson? // empty'` filters to parseable entries.

For stack traces, `grep -A N -B N` captures surrounding frames; or use an `awk` state machine.

## Evidence capture

Save under `.agent-history/oracle/<card-id>/<timestamp>/`:

- `log.path.txt` — single line: the absolute path of the log being watched
- `pattern.txt` — single line: the asserted regex pattern
- `match.txt` — the matched lines (may be empty if timeout or no-progress)
- `tail-context.txt` — last 200 lines at the moment of verdict, regardless of match (shows what *else* the system was doing)
- `exit.txt` — `0` / `1` / `2` / other (from the helpers above)
- `verdict.md` — APPROVE / REJECT / ESCALATE with the spec table filled in

## Rules

- **Always set a timeout.** `timeout` on Linux, `gtimeout` on macOS via `brew install coreutils`. Without it, the wait is unbounded.
- **Default to `tail -F`, not `-f`.** Rotation happens during long-running waits; `-F` re-opens, `-f` silently stops following.
- **Choose history mode deliberately.** `-n +1 -F` includes existing lines; `-n 0 -F` skips them. The wrong choice hides bugs in either direction.
- **Use awk for occurrence count.** `grep -m1` only matches once. For "shall log N times," accumulate in awk and exit at the threshold.
- **Fail fast on no-progress.** Stat the file size between polls; if it has not grown across N polls and the pattern still does not match, the system has stopped writing. Three return codes: matched, timed out, no-progress.
- **Defeat buffering at the writer.** `stdbuf -o0`, `unbuffer`, or `script -q`. If the line never flushes, no amount of waiting helps.
- **Normalise structured logs before grep.** `jq -c` for JSON; `awk` state machines for multi-line plaintext.
- **Capture surrounding context.** `tail-context.txt` is part of the transcript even when the assertion succeeds; it is the difference between "the line appeared" and "the line appeared in a sensible context."
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
