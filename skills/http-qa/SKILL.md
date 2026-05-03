---
name: http-qa
description: This skill should be used when the user asks to "QA an HTTP endpoint", "verify an API", "check the API behaves", "drive the API", "validate a route", or wants end-to-end behavioural verification of HTTP/JSON services. Drives a real running service with curl + jq, asserts on status / shape / headers / timing, captures the full request-response transcript as evidence, and distinguishes spec violations from flake.
argument-hint: "[scope of the API change to verify]"
---

# http-QA

Verify HTTP behaviour by driving the running service — boot it, request it, observe the full response, judge whether what was observed matches what the card's specs asserted. This skill drives behaviour; it does not write code.

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine which routes, handlers, or middleware changed.

## Workflow

1. Read the changeset — `git diff`, `git log` — and identify the surface that changed: route paths, request schemas, response shapes, headers, status semantics.
2. Detect the server start command — `package.json` scripts, `bin/rails server`, `Procfile`, `Justfile`, `make run`. Read the project's `README` and `CLAUDE.md` first.
3. Start the server in the background; trap teardown so it dies even on failure.
4. Poll the server's readiness *by HTTP request*, not by sleep — see the lifecycle block below.
5. Drive the endpoint with `curl`. Capture status, headers, and body to *separate files*. Capture timing.
6. Apply the assertion grammar (`references/assertion-grammar.md`) to compare observed output to the spec.
7. If a response is unexpected, classify it as defect or flake using the rubric in `references/flake-vs-defect.md`.
8. Persist the transcript under `.agent-history/oracle/<card-id>/<timestamp>/`.
9. Kill the server.

## Server Lifecycle

The single most common failure of HTTP QA is racing the boot. A test that worked once because the laptop was warm is no test at all.

```bash
SERVER_LOG="/tmp/qa-server-$$.log"
bin/rails server -p 3000 >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!
trap '[ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null' EXIT

# Poll readiness — request the actual surface, and watch for the process to die
ready=0
for i in $(seq 1 60); do
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "server process died before becoming ready (see $SERVER_LOG)" >&2
    exit 1
  fi
  if curl -sf -o /dev/null http://localhost:3000/up; then
    ready=1
    break
  fi
  sleep 0.5
done
[ "$ready" = 1 ] || { echo "server failed to become ready in 30s (see $SERVER_LOG)" >&2; exit 1; }
```

Two non-default disciplines worth naming. First: poll on the condition you actually care about, not a guess about how long boot takes — the condition is "the endpoint under test responds," not "30 seconds have elapsed" (this is the lesson `obra/superpowers-skills/skills/testing/condition-based-waiting` encodes for in-process tests, retargeted to HTTP). Second: watch the server process itself with `kill -0`. A process that crashes during boot never opens the port and never responds; without the death check, the loop polls until timeout against a corpse, then runs the test against nothing. This is the load-bearing piece of `anthropics/skills/skills/webapp-testing/scripts/with_server.py` — a poll loop alone is not equivalent to the upstream pattern.

Replace `bin/rails server -p 3000` and `/up` with the project's start command and a route the spec under test does *not* depend on (e.g., `/healthz`, `/up`, `/`). Polling the route under test masks readiness behind whatever the test will assert.

## Driving the Endpoint

Capture status, headers, and body separately. `curl -i` collapses them; the Oracle's transcript needs them apart.

```bash
TXN=.agent-history/oracle/$CARD_ID/$(date +%Y%m%dT%H%M%S)
mkdir -p "$TXN"

curl -sS -o "$TXN/body.json" \
     -D "$TXN/headers.txt" \
     -w '%{http_code}\n%{time_total}\n' \
     -X POST http://localhost:3000/api/widgets \
     -H 'Content-Type: application/json' \
     -d '{"name":"test"}' \
  > "$TXN/status_and_timing.txt" 2> "$TXN/curl_stderr.txt"

# Pretty-print the request for the transcript
cat > "$TXN/request.txt" <<'EOF'
POST /api/widgets HTTP/1.1
Content-Type: application/json

{"name":"test"}
EOF
```

`-w` writes status and total time to stdout; `-D` dumps headers; `-o` writes body. Three channels, three files.

## Boundary Walk

Lifted from `vercel/vercel-plugin/skills/verification`: name the boundaries the request crosses, and stop at the first broken one. A failure at boundary 1 makes assertions at boundary 3 meaningless.

| # | Boundary | Asserted by | Evidence file |
|---|---|---|---|
| 1 | Request reaches the server | `curl` exits 0, `status_and_timing.txt` has a code | `curl_stderr.txt` |
| 2 | Server responds with the expected status | `status_and_timing.txt` line 1 | `status_and_timing.txt` |
| 3 | Response carries the expected headers | `headers.txt` matches spec | `headers.txt` |
| 4 | Response body matches the expected shape | `jq` predicates over `body.json` | `body.json` + assertion log |
| 5 | Side effects (DB row, queued job, log line) match spec | per the skill for that surface (`db-state-qa`, `log-tail-qa`) | linked transcript |

If boundary 1 fails (curl error, connection refused), boundaries 2-5 are not assertable. Report and stop.

## Assertion Grammar

`jq` is universal; lean on it for body shape. The vocabulary worth committing to memory lives in `references/assertion-grammar.md` — JSONPath-flavoured predicates over `body.json`, header presence/equality, status equality, timing under N ms. This is the non-obvious selector syntax a model would otherwise hallucinate.

For richer assertion grammar, `Orange-OpenSource/hurl` ships a plain-text format with `jsonpath`, `xpath`, `header`, `duration`, and `regex` predicates as first-class. Cite it; do not require it.

## Distinguishing Spec Violation from Flake

This is the original judgment the Oracle contributes — no upstream skill encodes it.

| Observation | Spec asserts what? | Verdict |
|---|---|---|
| 2xx with expected body | matching success | satisfied |
| 2xx with unexpected body | success but shape wrong | REJECT (boundary 4) |
| 4xx | spec asserts that 4xx | satisfied |
| 4xx | spec asserts 2xx | REJECT (boundary 2) |
| 5xx, deterministic across two requests | spec does not assert 5xx | REJECT (defect) |
| 5xx, intermittent across N requests | spec does not assert idempotency | ESCALATE (flake suspected — environmental, may not be the worker's fault) |
| 5xx, deterministic | spec asserts 5xx (e.g., "shall return 503 when downstream is unavailable") | satisfied |
| Timeout | any | ESCALATE (cannot determine cause from a single trial) |

See `references/flake-vs-defect.md` for the longer rubric and reproduction protocol.

## Evidence Capture for the Oracle

Save artefacts under `.agent-history/oracle/<card-id>/<timestamp>/`:

- `request.txt` — the request as it would appear in an HTTP/1.1 transcript
- `body.json` — the response body as the server returned it (no jq processing)
- `headers.txt` — `-D` output, full header block
- `status_and_timing.txt` — `-w` output, two lines: status, time
- `curl_stderr.txt` — connection errors live here; absence is significant
- `assertions.log` — one line per asserted predicate, with `match` / `mismatch` / `could-not-determine`
- `verdict.md` — APPROVE / REJECT / ESCALATE with the boundary-walk table filled in

The transcript directory is the deliverable. Without it, the Oracle's APPROVE has no foundation.

## Rules

- **Poll readiness with the actual condition.** A sleep-based wait passes on a fast machine and fails on a slow one. The polling loop is two lines; write them.
- **Trap teardown.** Servers leak. `trap … EXIT` runs on success, failure, and interrupt.
- **Separate stdout from stderr from headers from body.** Collapsing them loses information; the Oracle's transcript needs all four.
- **Walk the boundaries in order.** Failure at boundary N makes boundaries > N unassertable. Report the first failure and stop.
- **Reproduce 5xx before judging it.** Two requests with the same input. Deterministic 5xx is a defect; intermittent is flake.
- **Cite hurl, do not require it.** `curl + jq` is the universal floor; `hurl` is a richer dialect when present.
## Report Format

```
## HTTP QA Report

**Scope**: <which endpoint(s) verified>
**Verdict**: APPROVE | REJECT | ESCALATE

### Server
- [PASS/FAIL] `<server start command>` — port, ready time

### Boundary Walk
| # | Boundary | Result | Evidence |
|---|---|---|---|
| 1 | request reaches server | PASS/FAIL | path |
| 2 | status code | PASS/FAIL | path |
| 3 | headers | PASS/FAIL | path |
| 4 | body shape | PASS/FAIL | path |
| 5 | side effects | PASS/FAIL/N/A | linked transcript |

### Specifications Verified
| Spec # | Predicate | Verified by | Verdict |
|--------|-----------|-------------|---------|
| 1 | (paste from bl show) | (jq expression / file) | satisfied / unsatisfied / could-not-determine |

### Findings
1. <description with reproduction command and evidence path>

### Transcript
Path: `.agent-history/oracle/<card-id>/<timestamp>/`
Contents: <brief listing>
```
