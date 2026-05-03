# Flake vs Defect: judgment encoding for http-qa

The Oracle's distinctive job is judgment: deciding whether an unexpected response is a defect (REJECT) or environmental noise (ESCALATE). This document is the rubric.

## The reproduction protocol

When an unexpected response is observed, do not record a verdict on the first observation. Reproduce.

```bash
# Capture three trials of the same request
for i in 1 2 3; do
  curl -sS -o "$TXN/body.$i.json" \
       -D "$TXN/headers.$i.txt" \
       -w '%{http_code}\n%{time_total}\n' \
       <same flags as the original request> \
    > "$TXN/status_and_timing.$i.txt"
done

# Compare statuses across trials
for i in 1 2 3; do echo "trial $i: $(sed -n 1p "$TXN/status_and_timing.$i.txt")"; done
```

Three trials is the minimum to distinguish "deterministic" from "intermittent." A single observation cannot tell the two apart.

## The rubric

| Pattern across 3 trials | Spec asserts | Verdict | Why |
|---|---|---|---|
| 2xx, 2xx, 2xx, all bodies match | matching success | satisfied | Spec exercised, behaviour matches. |
| 2xx, 2xx, 2xx, bodies differ between trials | spec asserts deterministic body | REJECT | Non-determinism is itself a defect when the spec says "shall return X". |
| 2xx, 2xx, 2xx, bodies differ | spec does not assert determinism | satisfied with note | Record the variation in `verdict.md`; do not block. |
| 4xx, 4xx, 4xx, same code | spec asserts that 4xx | satisfied | |
| 4xx, 4xx, 4xx, same code | spec asserts 2xx | REJECT | Deterministic wrong status is a defect. |
| 5xx, 5xx, 5xx, same code | spec asserts 5xx | satisfied | |
| 5xx, 5xx, 5xx, same code | spec does not assert 5xx | REJECT | Deterministic 5xx without a spec is a defect. |
| 5xx, 2xx, 5xx (or any mix) | spec does not assert idempotency | ESCALATE | Intermittent 5xx is environmental flake until proven otherwise — possibly upstream, possibly load-related, possibly the worker. The Oracle cannot determine cause from request-level observation. Hand to a human. |
| 5xx, 2xx, 5xx | spec asserts "shall be idempotent" or "shall succeed under load" | REJECT | The spec asserts the very property the observations falsify. |
| timeout, ?, ? | any | ESCALATE | One timeout poisons the trial set; reproduce on a quieter system, or escalate. |
| 2xx, 2xx, 2xx but timing > spec'd budget | spec asserts "shall respond within N ms" | REJECT | A timing spec is still a spec. |
| 2xx, 2xx, 2xx but timing > 2× spec'd budget on one trial | spec asserts "shall respond within N ms" | ESCALATE | A single slow trial may be GC, may be context switch, may be the worker. Reproduce; escalate if it persists. |

## What "deterministic" actually means here

Three identical trials in a row is not proof of determinism in the abstract — it is the threshold the Oracle uses for *this verdict*. A defect that manifests once per thousand requests will not be caught by three trials and is correctly outside the Oracle's scope. Such defects are caught by load testing or production monitoring; the Oracle's job is to catch defects observable in the small.

If the card's specs assert behaviour that requires statistical confidence (e.g., "shall succeed at least 99.9% of the time"), the card needs a different surface and a different harness. Mark the spec as `could-not-determine` and flag in `## Unresolved`.

## The honest case for ESCALATE

There is institutional pressure to convert ESCALATE verdicts into REJECT or APPROVE — REJECT looks decisive, APPROVE moves work forward. ESCALATE looks like indecision. Resist this. ESCALATE is the verdict that says "I observed something the rubric cannot classify, and a human should look."

Specifically, the Oracle's protocol forbids APPROVE in the absence of evidence. An intermittent 5xx is not evidence of correctness; it is evidence of *something*, but not necessarily evidence of a defect in the change under test. The honest verdict is ESCALATE.

## Anti-rationalization

The reviewer ought to find these reasonings unconvincing if encountered in the Oracle's verdict:

| Rationalization | What's wrong with it |
|---|---|
| "The 5xx happened only once, so it's flake — APPROVE." | Without a reproduction protocol, "only once" is "I tried only once." |
| "The spec doesn't say 5xx is forbidden, so it's fine." | Specs assert positively; a 5xx not asserted by spec is a defect by exclusion. |
| "I couldn't reproduce the slow trial, so timing is fine." | The card's timing spec is the Oracle's contract; the inability to reproduce the breach does not absolve it. |
| "The worker said this is a known issue." | The Oracle does not read the worker's reasoning; "known" is not "satisfied." |
| "It worked in the worker's environment." | The Oracle exercises the system the orchestrator built, not the worker's laptop. |

If the Oracle's verdict rests on any of these, the verdict is ESCALATE, not APPROVE.
