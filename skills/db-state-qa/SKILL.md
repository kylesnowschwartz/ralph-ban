---
name: db-state-qa
description: This skill should be used when the user asks to "verify database state", "check what's in the database", "assert DB rows after the action", "validate the migration", or wants behavioural verification of database side effects. Snapshots database state before the action, exercises the system, snapshots after, and diffs structurally — ignoring volatile fields the spec does not assert. Used by primary-surface oracles (http-qa, cli-qa, library-qa) when a card asserts side-effect behaviour.
argument-hint: "[scope of the database side-effect to verify]"
---

# db-state-QA

Snapshot DB state before the action, exercise the system, snapshot after, diff structurally. Side-effect oracle invoked from `http-qa`, `cli-qa`, or `library-qa` when a spec asserts "after this action, the database shall ...".

The Oracle observes *live, committed* state. Unlike unit-test harnesses that wrap in transactions and roll back, the Oracle rolls nothing back.

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine which tables, schemas, or query patterns the change touches.

## Bundled Resources

- `references/engine-specific.md` — introspection queries per engine (SQLite, Postgres, MySQL), WAL gotcha, advisory locks.
- `references/active-record.md` — Rails app-level state via `bin/rails runner`, autoload boundary, AR vs raw SQL.

## Workflow

1. Identify the database connection target (file path for SQLite, host/port/db for Postgres/MySQL, environment for Rails).
2. **Snapshot before**: capture row counts, key rows, and schema state for the tables the spec names. Use `ORDER BY` on every multi-row query so the JSON output is deterministic. Persist to `before.json`.
3. **Exercise the action**: invoke the primary surface (HTTP request, CLI invocation, library call). The action is whatever `http-qa` / `cli-qa` / `library-qa` is driving.
4. **Wait for quiescence** if the action triggers async work. `after_commit` callbacks, queued jobs, and replicated writes all complete *after* the action's primary response. Snapshotting immediately can race them. See "Quiescence" below.
5. **Snapshot after**: capture the same fields, in the same order. Persist to `after.json`.
6. **Diff structurally**: compare `before.json` to `after.json`, ignoring fields the spec does not assert (timestamps, autoincrement IDs).
7. Apply the spec's assertion to the diff.

```bash
TXN=.agent-history/oracle/$CARD_ID/$(date +%Y%m%dT%H%M%S)
mkdir -p "$TXN"

snapshot_state > "$TXN/before.json"
./oracle/perform-action.sh > "$TXN/action.log" 2>&1
ACTION_EXIT=$?
echo "$ACTION_EXIT" > "$TXN/action_exit.txt"

# Wait for the asserted side effect to settle before snapshotting.
# For sync writes, no wait. For async, see "Quiescence" below.
./oracle/wait-for-quiescence.sh

snapshot_state > "$TXN/after.json"

# Structural diff with volatile fields blanked.
# `walk` is a builtin in jq 1.5+ (Linux/macOS default jq is usually 1.6).
jq -S 'walk(if type == "object" then del(.created_at, .updated_at, .id) else . end)' \
   "$TXN/before.json" > "$TXN/before.normalised.json"
jq -S 'walk(if type == "object" then del(.created_at, .updated_at, .id) else . end)' \
   "$TXN/after.json"  > "$TXN/after.normalised.json"

diff -u "$TXN/before.normalised.json" "$TXN/after.normalised.json" > "$TXN/diff.txt" || true
```

The asserted change appears in `diff.txt` as `+` / `-` lines.

## Determinism: order matters

Two snapshots of the same rows in different orders produce a noisy diff. Every multi-row query needs `ORDER BY` on a stable key — `Widget.order(:id)` for AR, `ORDER BY id ASC` for raw SQL.

## Quiescence: when to snapshot after async work

If the side effect is asynchronous, the immediate after-snapshot may run before it lands. Common sources:

- **`after_commit` callbacks** — fire after transaction commits, after the controller returns.
- **Queued jobs** (Sidekiq, Active Job, Resque) — action enqueues; worker runs later.
- **Replicated writes** — primary commits, replica catches up after lag.
- **Counter caches** — sometimes synchronous, sometimes deferred.

For job-driven effects, drain the queue (`Sidekiq::Worker.drain`, `bin/rails runner 'YourJob.drain'`, or wait on a completion log line via `log-tail-qa`). For `after_commit` and counter caches, the effect is settled by the time the response reaches the client. For replication, snapshot the primary.

If the spec is silent on async-vs-sync and the action is observably async, verdict is `could-not-determine`.

## Snapshotting state

A snapshot captures: row count per relevant table; content of rows the spec names; schema state if the spec asserts schema. Snapshot only the tables the spec names — whole-DB dumps drown the asserted change in noise.

For SQLite:

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

| Field | Source | Why volatile |
|---|---|---|
| `id` | autoincrement / UUID | Different per run |
| `created_at` / `updated_at` | timestamp on insert | Wall clock |
| `*_token` / `*_secret` | random generation | Non-determinable |
| `lock_version` | optimistic locking | Increments on save |
| `cached_*` | counter caches | Updates asynchronously |

Project-specific volatiles extend the list. When the spec is silent, default to ignoring the standard set and note this in the verdict.

## What the spec asserts

| Spec asserts | Diff predicate |
|---|---|
| "shall persist a Widget with name=X" | `+` row matching `{name: "X", ...}`; widgets count +1 |
| "shall not modify audit_log on read" | audit_log count unchanged; no `+` / `-` for that table |
| "shall update cached_count on add" | `cached_count` shows `+old, -new` with `new = old + 1` |
| "shall add an index on widgets(slug)" | schema index list includes `widgets_slug_idx` after; absent before |

A spec that does not name fields is too vague; mark `could-not-determine` and surface the ambiguity to the planner.

## SQLite WAL gotcha

WAL mode keeps writes in a `-wal` sidecar until checkpointed. Long-lived connections opened before a writer commits see stale state due to SQLite's MVCC isolation — the diff shows nothing even when the action wrote a row.

Fix: open a fresh connection per snapshot. `sqlite3 "$DB_PATH" "..."` per snapshot crosses the process boundary and forces a fresh connection.

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

## Rules

- **Open a fresh connection per snapshot.** Particularly important under SQLite WAL; pin-to-connection isolation otherwise hides the action's effect.
- **Snapshot only the tables the spec names.** Whole-database dumps drown the asserted change in noise.
- **Normalise volatiles before diff.** `id`, `created_at`, `updated_at`, tokens, lock versions. Project-specific volatiles extend the set.
- **Distinguish action failure from spec failure.** `action_exit.txt` answers "did the action even run." A non-zero action exit makes the after-snapshot uninformative; report and escalate.
- **Do not run inside a transaction.** Transactional rollback is the failure mode the Oracle exists to backstop. The Oracle observes committed state.
- **Prefer engine-specific catalogue queries for schema.** `sqlite_master`, `pg_catalog`, `information_schema` per `references/engine-specific.md` — ORM-level introspection (e.g., `User.columns_hash`) reads the schema cache, which may be stale.
- **For Rails apps, use `bin/rails runner`.** `bundle exec ruby -e "require 'config/environment'; ..."` is the manual fallback; runner is the canonical surface.
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
