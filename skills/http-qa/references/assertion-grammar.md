# Assertion Grammar for http-qa

The assertion vocabulary the Oracle uses against captured HTTP responses. Lifted in spirit from `Orange-OpenSource/hurl`'s predicate set; rewritten in `jq` and POSIX so no extra binary is required.

## Predicates over the body (`body.json`)

| Predicate | jq expression | Evidence written to `assertions.log` |
|---|---|---|
| Field exists | `jq -e '.path.to.field'` | `match` if exit 0 |
| Field equals literal | `jq -e '.id == "abc-123"'` | `match` if exit 0 |
| Field is non-empty string | `jq -e '.name | type == "string" and length > 0'` | |
| Field is integer | `jq -e '.count | type == "number" and . == floor'` | |
| Field is one of | `jq -e '.status | IN("queued","running","done")'` | |
| Array length equals N | `jq -e '.items \| length == 5'` | |
| Array length at least N | `jq -e '.items \| length >= 1'` | |
| Every element matches | `jq -e '.items \| all(.id != null)'` | |
| Some element matches | `jq -e '.items \| any(.id == "abc")'` | |
| Field matches regex | `jq -e '.email \| test("^[^@]+@[^@]+$")'` | |
| Field absent / null | `jq -e '.deleted_at == null'` | |

`-e` exits non-zero when the result is `false` or `null`, which is what makes these usable in shell. Without `-e`, `jq` exits 0 even when the predicate returned `false`, and the Oracle would silently approve a mismatch.

## Predicates over headers (`headers.txt`)

| Predicate | Shell |
|---|---|
| Header present | `grep -qi '^Content-Type:' headers.txt` |
| Header equals | `grep -qi '^Content-Type: application/json' headers.txt` |
| Header matches regex | `grep -qiE '^Cache-Control:.*max-age=[0-9]+' headers.txt` |
| Header absent | `! grep -qi '^Set-Cookie:' headers.txt` |

`grep -i` for case-insensitive header matching — HTTP headers are not case-sensitive, and curl preserves whatever the server emitted.

## Predicates over status and timing (`status_and_timing.txt`)

The file has two lines: status code, total time in seconds (decimal).

| Predicate | Shell |
|---|---|
| Status equals | `[ "$(sed -n 1p status_and_timing.txt)" = "200" ]` |
| Status in 2xx | `[[ "$(sed -n 1p status_and_timing.txt)" =~ ^2[0-9][0-9]$ ]]` |
| Time under N seconds | `awk 'NR==2 && $1 < 0.500 {ok=1} END {exit !ok}' status_and_timing.txt` |

Timing assertions are the lever for "the spec asserts the endpoint responds within 500 ms." Without this, latency regressions slip past the Oracle silently.

## Why this grammar and not jsonpath / jmespath / hurl

Three candidate dialects exist. The trade-off:

- **jsonpath** — well-known, but `jq` is ubiquitous in dev environments and the syntax overlap is enough that operators read jq paths as jsonpath and vice versa.
- **jmespath** — AWS-preferred; not universally installed.
- **hurl** — richer, but adds a binary requirement.

The skill's floor is `curl + jq + grep + awk` because all four are present on every developer machine the project's repo expects. Predicates that require richer dialects belong in a card spec that explicitly names the surface (`oracle_kind: http with hurl`); the default surface assumes nothing more than the POSIX floor.

## Composing predicates into a single pass

For a single endpoint with several specs, write the assertions to `assertions.log` in one loop, with one line per predicate, so the verdict.md can summarise without re-running:

```bash
ASSERT_LOG="$TXN/assertions.log"
: > "$ASSERT_LOG"

assert() {
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "match    $name" >>"$ASSERT_LOG"
  else
    echo "mismatch $name" >>"$ASSERT_LOG"
  fi
}

assert "status is 201"             test "$(sed -n 1p "$TXN/status_and_timing.txt")" = "201"
assert "Content-Type is JSON"      grep -qi '^Content-Type: application/json' "$TXN/headers.txt"
assert "body.id is non-empty str"  jq -e '.id | type == "string" and length > 0' "$TXN/body.json"
assert "body.status is queued"     jq -e '.status == "queued"' "$TXN/body.json"
```

`assertions.log` becomes the row-source for the spec table in the verdict; one line per asserted spec, three possible values (`match` / `mismatch` / `could-not-determine`).
