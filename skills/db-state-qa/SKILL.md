---
name: db-state-qa
description: This skill should be used when the user asks to "verify database state", "check what's in the database", "assert DB rows after the action", "validate the migration", or wants behavioural verification of database side effects. Snapshots database state before the action, exercises the system, snapshots after, and diffs structurally — ignoring volatile fields the spec does not assert. Used by primary-surface oracles (http-qa, cli-qa, library-qa) when a card asserts side-effect behaviour.
argument-hint: "[scope of the database side-effect to verify]"
---

# db-state-QA

Verify database side effects by snapshotting state, exercising the system, snapshotting again, and diffing structurally. This skill is *not* a primary surface in the Oracle's `kind:` taxonomy; it is a side-effect oracle invoked from `http-qa`, `cli-qa`, or `library-qa` when a card's spec asserts "after this action, the database shall ...".

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine which tables, schemas, or query patterns the change touches.

## Bundled Resources

- `references/engine-specific.md` — introspection queries per engine (SQLite `sqlite_master`, Postgres `pg_catalog` and `information_schema`, MySQL), the WAL gotcha, advisory locks. Load when the spec asserts schema or constraint state.
- `references/active-record.md` — Rails app-level state via `bin/rails runner`, including the autoload boundary and the difference between asserting on AR objects versus raw SQL. Load when the library under test is a Rails app.

## Why this skill exists

The reviewer reads code; the Oracle drives behaviour. For changes that mutate database state, "the code looks correct" is the precise failure mode the Oracle exists to backstop. A spec that says "shall persist a Widget row" is satisfied when *a row exists with the asserted shape*, not when the controller calls `Widget.create!`. The Oracle observes the result; it does not read the call site.

This is the inversion the research turned up. Test-mocking libraries — `database_cleaner`, `sqlmock`, transactional fixtures, savepoint-based isolators — exist to make *unit tests* fast and isolated. They are antithetical to what the Oracle does: the Oracle exercises *live* state, observes the side effect, and rolls nothing back. A db-state assertion using sqlmock observes the worker's SQL string, not the database — and the worker already convinced themselves the SQL was right. The Oracle's job is the third leg of the stool.

`samber/cc-skills-golang/skills/golang-database/references/testing.md` documents sqlmock-style verification for *unit tests*. Cite it as the anti-pattern this skill replaces in the Oracle's context.

## The workflow

The spec asserts behaviour of the form *"after action X, the database shall be in state Y."* The Oracle's exercise is therefore three phases: snapshot before, perform X, snapshot after, compare.

1. Identify the database connection target (file path for SQLite, host/port/db for Postgres/MySQL, environment for Rails).
2. **Snapshot before**: capture row counts, key rows, and schema state for the tables the spec names. Persist to `before.json`.
3. **Exercise the action**: invoke the primary surface (HTTP request, CLI invocation, library call). The action is whatever `http-qa` / `cli-qa` / `library-qa` is driving.
4. **Snapshot after**: capture the same fields. Persist to `after.json`.
5. **Diff structurally**: compare `before.json` to `after.json`, ignoring fields the spec does not assert (timestamps, autoincrement IDs).
6. Apply the spec's assertion to the diff.

```bash
TXN=.agent-history/oracle/$CARD_ID/$(date +%Y%m%dT%H%M%S)
mkdir -p "$TXN"

snapshot_state > "$TXN/before.json"
./oracle/perform-action.sh > "$TXN/action.log" 2>&1
ACTION_EXIT=$?
echo "$ACTION_EXIT" > "$TXN/action_exit.txt"
snapshot_state > "$TXN/after.json"

# Structural diff with volatile fields blanked
jq -S 'walk(if type == "object" then del(.created_at, .updated_at, .id) else . end)' \
   "$TXN/before.json" > "$TXN/before.normalised.json"
jq -S 'walk(if type == "object" then del(.created_at, .updated_at, .id) else . end)' \
   "$TXN/after.json"  > "$TXN/after.normalised.json"

diff -u "$TXN/before.normalised.json" "$TXN/after.normalised.json" > "$TXN/diff.txt" || true
```

The asserted change shows up in `diff.txt` as `+` lines for new rows and `-` lines for removed rows. Volatile fields are normalised away first so the diff is signal, not noise.

## Snapshotting state

The minimum a snapshot needs:

- Row count per relevant table.
- The full content of rows the spec names (lookup by primary key when known, by query when not).
- Schema state if the spec asserts schema (column existence, index presence, constraint definitions).

A snapshot does *not* dump the entire database — that produces a diff dominated by unrelated noise. Snapshot the tables the spec asserts on, plus tables the planner identified as adjacent.

For SQLite (the engine ralph-ban uses):

```bash
snapshot_state() {
  sqlite3 "$DB_PATH" <<'SQL'
.mode json
SELECT 'widgets' AS table_name, COUNT(*) AS row_count FROM widgets
UNION ALL
SELECT 'audit_log', COUNT(*) FROM audit_log;
SQL
  echo
  sqlite3 "$DB_PATH" "SELECT * FROM widgets WHERE name = 'test'" -json
}
```

For Postgres and MySQL the shell-level patterns are similar; see `references/engine-specific.md`.

For Rails, use `bin/rails runner` to print structured state in one pass; see `references/active-record.md`.

## Volatile-field normalisation

Database side effects always introduce volatile fields. The standard set:

| Field | Source | Why volatile |
|---|---|---|
| `id` | autoincrement / UUID | Different on every run; usually not asserted |
| `created_at` / `updated_at` | timestamp on insert | Wall clock; never byte-equal across runs |
| `*_token` / `*_secret` | random generation | Deliberately non-determinable |
| `lock_version` | optimistic locking | Increments on every save |
| `cached_*` | counter caches | Updates asynchronously |

The `jq` walk in the workflow above is the normalisation primitive. Project-specific volatiles extend the list. The spec specifies which fields are *content* (asserted) versus which are *bookkeeping* (ignored); when the spec is silent, the Oracle defaults to ignoring the standard set above and notes this in the verdict.

## What the spec actually asserts

The spec drives the diff. Three common shapes:

| Spec asserts | Diff predicate |
|---|---|
| "shall persist a Widget with name=X" | a `+` line for a row matching `{name: "X", ...}` exists; row count for `widgets` increased by 1 |
| "shall not modify the audit_log on read" | row count for `audit_log` unchanged; no `+` or `-` lines for that table |
| "shall update the cached_count on add" | `cached_count` field on the parent row shows `+old, -new` with `new = old + 1` |
| "shall add an index on widgets(slug)" | schema snapshot's index list includes `widgets_slug_idx` after; absent before |

A spec that does not name fields is too vague to verify; mark it `could-not-determine` in the verdict and surface the ambiguity to the planner.

## SQLite WAL gotcha

This is the operational knowledge the research surfaced as non-obvious. SQLite in WAL mode (`PRAGMA journal_mode=WAL`) keeps writes in a sidecar `-wal` file until checkpointed; readers can see stale state if they connect before the writer commits. ralph-ban's database is in WAL mode.

Symptoms:
- The Oracle snapshots `before`, exercises the action, snapshots `after`, and the diff shows nothing — even though the action clearly should have written a row.
- Re-running the snapshot a second after the action shows the row.

Mechanism: the Oracle's snapshot connection was opened before the writer committed; SQLite's MVCC-flavoured isolation pins the connection's view of the database. Closing and reopening the connection between snapshots is the fix. The `sqlite3` shell already does this when invoked fresh per snapshot; the failure mode appears in long-lived probe processes that hold a single connection.

Rule: **open a fresh connection per snapshot**. The cheapest is `sqlite3 "$DB_PATH" "..."` per snapshot — process boundary forces a fresh connection.

## Evidence capture

Save under `.agent-history/oracle/<card-id>/<timestamp>/`:

- `before.json` — pre-action snapshot
- `before.normalised.json` — same with volatiles redacted
- `action.log` — output of the primary-surface action (curl response, CLI stdout, etc.; usually a link to the primary oracle's transcript)
- `action_exit.txt` — exit code of the action
- `after.json` — post-action snapshot
- `after.normalised.json` — normalised
- `diff.txt` — `diff -u` of the two normalised snapshots
- `verdict.md` — APPROVE / REJECT / ESCALATE with the spec table filled in

The `diff.txt` is the verdict's evidence column. A satisfied spec usually corresponds to a few `+` and `-` lines that match the spec's predicate.

## Rules

- **Open a fresh connection per snapshot.** Particularly important under SQLite WAL; pin-to-connection isolation otherwise hides the action's effect.
- **Snapshot only the tables the spec names.** Whole-database dumps drown the asserted change in noise.
- **Normalise volatiles before diff.** `id`, `created_at`, `updated_at`, tokens, lock versions. Project-specific volatiles extend the set.
- **Distinguish action failure from spec failure.** `action_exit.txt` answers "did the action even run." A non-zero action exit makes the after-snapshot uninformative; report and escalate.
- **Do not run inside a transaction.** Transactional rollback is the failure mode the Oracle exists to backstop. The Oracle observes committed state.
- **Prefer engine-specific catalogue queries for schema.** `sqlite_master`, `pg_catalog`, `information_schema` per `references/engine-specific.md` — ORM-level introspection (e.g., `User.columns_hash`) reads the schema cache, which may be stale.
- **For Rails apps, use `bin/rails runner`.** `bundle exec ruby -e "require 'config/environment'; ..."` is the manual fallback; runner is the canonical surface.
- **Don't fix anything.** Report what's broken. This skill is QA, not implementation.

## Report Format

```
## DB-State QA Report

**Scope**: <which tables / schema state verified>
**Engine**: SQLite | Postgres | MySQL | ActiveRecord (Rails)
**Verdict**: APPROVE | REJECT | ESCALATE

### Action
- Primary oracle: http-qa / cli-qa / library-qa
- Action exit: <code>
- Action transcript: <linked path>

### Diff Summary
| Table | Before count | After count | Change matches spec? |
|---|---|---|---|
| widgets | 0 | 1 | yes |
| audit_log | 14 | 14 | yes (spec: shall not modify) |

### Specifications Verified
| Spec # | Predicate | Verified by | Verdict |
|--------|-----------|-------------|---------|
| 1 | (paste from bl show) | (diff hunk / row content) | satisfied / unsatisfied / could-not-determine |

### Findings
1. <description with evidence path>

### Transcript
Path: `.agent-history/oracle/<card-id>/<timestamp>/`
Contents: <brief listing>
```
