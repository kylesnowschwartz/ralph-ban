# Golden-File Discipline for cli-qa

A *golden file* is a fixture stored in the repo that records the expected output of a command. The discipline that makes golden files non-flaky is **redaction** — replacing fields whose values vary between runs with stable placeholders before comparison.

## The problem golden files solve

For a command whose output is large or structurally rich, line-by-line assertions are tedious to write and brittle to maintain. A golden file inverts the relationship: write the assertion *once*, by inspection of correct output, and the test is "the output still matches this fixture." Adding a new field to the output requires regenerating the fixture; that regeneration is itself a deliberate decision recorded in the diff.

## The problem redaction solves

Most CLI output contains fields that are correct but variable: timestamps, process IDs, temporary paths, terminal widths, ANSI escapes for colour, hostnames, ephemeral ports, hash digests, addresses-of-objects. A naive byte-equality diff fails on every run not because the program is broken but because today's run wasn't yesterday's.

Redaction replaces each variable field with a fixed placeholder *before* diff. The placeholder names what was redacted, so the diff remains readable.

## The standard redaction set

```bash
redact() {
  sed -E '
    # ISO 8601 timestamps with optional fractional seconds and timezone
    s/[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9:.+-]+/[TIMESTAMP]/g;

    # Unix-epoch-style timestamps, 10 or 13 digits in plausible range
    s/\b1[0-9]{9}\b/[EPOCH]/g;
    s/\b1[0-9]{12}\b/[EPOCH_MS]/g;

    # PIDs as introduced by "pid N" or "process N"
    s/(pid |process )[0-9]+/\1[PID]/g;

    # Temp paths
    s/\/tmp\/[A-Za-z0-9._-]+/[TMPPATH]/g;
    s/\/var\/folders\/[^ ]+/[TMPPATH]/g;

    # Hex-flavoured object addresses (Go fmt.%p, Python id() in repr)
    s/0x[0-9a-fA-F]{6,}/[ADDR]/g;

    # SHA-style digests (8+ hex chars surrounded by word boundaries)
    s/\b[0-9a-f]{40}\b/[SHA1]/g;
    s/\b[0-9a-f]{64}\b/[SHA256]/g;

    # ANSI colour and cursor escapes
    s/\x1b\[[0-9;]*[a-zA-Z]//g;

    # Bound-port allocations
    s/(localhost:|127\.0\.0\.1:|\[::1\]:)[0-9]+/\1[PORT]/g;

    # Durations like "took 12.345ms" or "(0.42s)"
    s/[0-9]+\.[0-9]+(ms|s|µs|ns)/[DURATION]/g;
  ' "$1"
}

redact "$TXN/stdout.txt" > "$TXN/stdout.redacted.txt"
diff -u testdata/expected.txt "$TXN/stdout.redacted.txt"
```

The list above is the set worth committing to memory. Project-specific volatiles (request IDs, trace IDs, machine names) extend it.

## Inspired by snapbox

`assert-rs/snapbox` (Apache-2.0, Rust) ships built-in placeholders for the runtime's own contributions — `[EXE]` for the executable extension on Windows (`.exe` or empty), `[..]` for "any text" — and lets the user define additional named placeholders for project-specific volatiles. The skill borrows the *naming convention* (`[TIMESTAMP]`, `[PID]`, etc., as locally-defined names that preserve diff readability) rather than the snapbox built-in set, which is smaller and Rust-flavoured.

The discipline that survives the borrowing: a *named* placeholder beats an opaque blank. A missing `[PID]` in the observed output tells you the field was present in the golden but absent in the run, which is a substantive change. A blanked-out diff loses that distinction.

The placeholders are textual sentinels written into the golden as the redaction output, then matched textually against the redacted observed. A diff over a redacted observed against a redacted golden is what produces the verdict.

## When NOT to use golden files

Golden files are appropriate when the output is large, structurally rich, and changes infrequently. They are inappropriate when:

- The output is one or two lines — write a direct assertion instead.
- The output is structurally simple but varies by input — a property-based test or table-driven test reads better.
- The output is truly volatile in *content*, not just in *fields* — for example, a command that returns a randomly-shuffled list. Use a structural assertion (length, set membership) instead.
- The spec asserts behaviour the golden cannot capture — e.g., "shall exit within 100ms." Timing belongs in a non-golden assertion.

## Regenerate-on-purpose UX

When a golden file genuinely needs to be updated (the program's output legitimately changed), the regenerate ergonomics matter. Two patterns are common:

**Env-var idiom (lifted from `mitsuhiko/insta` and `assert-rs/snapbox`):**

```bash
# Normal run — verifies against existing golden
./oracle-script.sh

# Regenerate run — overwrites the golden with current output
RALPH_BAN_UPDATE_GOLDENS=1 ./oracle-script.sh
```

The script reads the env var:

```bash
if [ -n "${RALPH_BAN_UPDATE_GOLDENS:-}" ]; then
  cp "$TXN/stdout.redacted.txt" testdata/expected.txt
  echo "regenerated: testdata/expected.txt"
else
  diff -u testdata/expected.txt "$TXN/stdout.redacted.txt"
fi
```

**Review tool idiom (lifted from `cargo insta review`):**

A separate tool walks every golden mismatch and prompts the operator: accept, reject, edit. This is overkill for a single Oracle exercise; mention it as the upgrade path when goldens proliferate.

## The rule for the Oracle

The Oracle does not regenerate goldens. The Oracle exists to detect mismatches between specified and observed behaviour; regenerating a golden hides exactly the mismatch the Oracle would have surfaced. Mismatch is a finding; record it in the verdict, attach the diff to the transcript, and let the worker (and reviewer) decide whether the golden needs updating.

A regenerated golden is an answer to "what does the program do *now*"; the spec is "what *should* the program do." These are not the same question. The Oracle answers the spec question.

## Subtleties worth knowing

- **Trailing whitespace** — `diff -u` preserves it; some editors strip it on save. Normalise with `sed -E 's/[[:space:]]+$//'` before diff if golden files travel through such editors.
- **CRLF vs LF** — capture under bash on macOS or Linux is LF; goldens authored on Windows may carry CRLF. Normalise with `tr -d '\r'`.
- **Locale-dependent output** — programs that format numbers (1,000 vs 1.000), dates (en_US vs en_GB), or currency vary by `LC_ALL`. Set `LC_ALL=C` for deterministic captures.
- **Timezone-dependent output** — programs that print local time vary by `TZ`. Set `TZ=UTC` for deterministic captures, and redact the timezone-suffix anyway in case the program calls `localtime()`.
