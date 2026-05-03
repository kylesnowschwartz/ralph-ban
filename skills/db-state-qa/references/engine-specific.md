# Engine-Specific Introspection for db-state-qa

The catalogue queries that read schema, indexes, and constraints differ per engine. This reference holds the canonical queries plus engine-specific gotchas (WAL, advisory locks, role privileges).

## SQLite

ralph-ban uses SQLite via `ncruces/go-sqlite3` (wazero). The catalogue lives in `sqlite_master` and a few `PRAGMA` calls.

### Schema introspection

```sql
-- All user tables (excludes sqlite-internal)
SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%';

-- Columns of a specific table
PRAGMA table_info(widgets);
-- returns: cid | name | type | notnull | dflt_value | pk

-- Indexes on a specific table
PRAGMA index_list(widgets);
-- returns: seq | name | unique | origin | partial

-- Index details (which columns)
PRAGMA index_info(widgets_slug_idx);
-- returns: seqno | cid | name

-- Foreign keys of a table
PRAGMA foreign_key_list(widgets);
```

Wrapping them as JSON for the snapshot:

```bash
sqlite3 "$DB_PATH" <<'SQL' | jq -s '{tables: ., schema_version: env.SCHEMA_REV}'
.mode json
SELECT name AS table_name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%';
SQL
```

### The WAL gotcha (worth a second mention)

`PRAGMA journal_mode=WAL` keeps writes in `<db>-wal` until checkpoint. A reader connection opened before the writer commits sees the pre-commit state; the post-commit state appears only when the reader's connection is closed and reopened.

Practical impact for the Oracle: open a fresh `sqlite3` process per snapshot. Long-lived probe processes that snapshot through a held connection will report stale state.

```bash
# Right: fresh process per snapshot
before=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM widgets")
./action
after=$(sqlite3 "$DB_PATH" "SELECT COUNT(*) FROM widgets")

# Wrong: held connection, stale read after commit
sqlite3 "$DB_PATH" <<SQL
SELECT COUNT(*) FROM widgets;            -- 0 (before)
.shell ./action
SELECT COUNT(*) FROM widgets;            -- still 0 (the same connection's view is pinned)
SQL
```

### Locking

SQLite serialises writes; concurrent writers block. If the Oracle's snapshot races a writer, it may see `database is locked`. Set a busy-timeout:

```sql
PRAGMA busy_timeout = 5000;
```

5 seconds is enough for any normal contention; if the Oracle still times out, the system is genuinely deadlocked and that is itself a finding.

## Postgres

### Schema introspection

```sql
-- Tables in the current schema
SELECT tablename FROM pg_tables WHERE schemaname = 'public';

-- Columns
SELECT column_name, data_type, is_nullable, column_default
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = 'widgets'
ORDER BY ordinal_position;

-- Indexes
SELECT indexname, indexdef
FROM pg_indexes
WHERE schemaname = 'public' AND tablename = 'widgets';

-- Constraints
SELECT con.conname, con.contype, pg_get_constraintdef(con.oid)
FROM pg_constraint con
JOIN pg_class rel ON rel.oid = con.conrelid
WHERE rel.relname = 'widgets';
```

`information_schema` is the SQL-standard view; `pg_catalog` (`pg_indexes`, `pg_constraint`) is Postgres-specific and exposes detail the standard view hides (e.g., partial-index `WHERE` clauses, expression indexes). For schema assertions specific enough to matter, prefer `pg_catalog`.

### Wrapping for snapshot

`psql` with `-A -t -F$'\t'` produces tab-separated, no headers, no row counts — easy for `jq` to ingest after a small transform:

```bash
psql -h "$PGHOST" -U "$PGUSER" -d "$PGDATABASE" -A -t -F$'\t' -c \
  "SELECT row_to_json(t) FROM (SELECT * FROM widgets WHERE name='test') t" \
  | jq -s '.' > "$TXN/widgets.before.json"
```

`row_to_json` does the structuring inside Postgres; the shell just collects.

### Advisory locks

Postgres has session-scoped and transaction-scoped advisory locks (`pg_advisory_lock`, `pg_advisory_xact_lock`). When the system under test uses them (background job scheduling, leader election), the Oracle's exercise can deadlock against the running workers if the snapshot acquires the same lock key.

Symptom: the snapshot query hangs indefinitely. Cause: the snapshot is waiting on a lock the running system holds.

Rule: do not take advisory locks during snapshot. Read uncommitted is acceptable for state assertion when the spec specifies; in that case use `SET TRANSACTION ISOLATION LEVEL READ UNCOMMITTED` — but Postgres treats this as `READ COMMITTED` regardless, so the safer move is to ensure the running system has quiesced before snapshot.

### Role privileges

A snapshot user with insufficient privileges sees a partial schema and silently misses tables. The symptom is a snapshot that reports zero rows for a table that is in fact populated. Verify the snapshot role has `SELECT` on every table the spec names.

```sql
SELECT table_name, privilege_type
FROM information_schema.role_table_grants
WHERE grantee = current_user;
```

If a table the spec names is missing from this list, the role is the bottleneck, not the data.

## MySQL

### Schema introspection

```sql
-- Tables
SELECT table_name
FROM information_schema.tables
WHERE table_schema = DATABASE();

-- Columns
SELECT column_name, data_type, is_nullable, column_default
FROM information_schema.columns
WHERE table_schema = DATABASE() AND table_name = 'widgets'
ORDER BY ordinal_position;

-- Indexes
SHOW INDEX FROM widgets;

-- Constraints
SELECT constraint_name, constraint_type
FROM information_schema.table_constraints
WHERE table_schema = DATABASE() AND table_name = 'widgets';
```

MySQL's `information_schema` is roughly compatible with Postgres's, but `SHOW` commands are MySQL-specific and often more readable for ad-hoc inspection. Use `information_schema` in scripts; `SHOW` is fine in interactive exploration.

### Gap locks and snapshot isolation

InnoDB at the default `REPEATABLE READ` isolation pins the snapshot to the transaction start. A long-running snapshot transaction can see "wrong" data — the post-action state — if the snapshot transaction started after the action committed but before the snapshot ran.

Rule: take snapshots in short, fresh transactions. `SET SESSION TRANSACTION READ ONLY; START TRANSACTION; SELECT ...; COMMIT` per snapshot is enough.

## When the spec is engine-agnostic

For specs that assert "shall persist a Widget" without naming the engine, the Oracle uses whichever engine the system under test ships with. The spec's row-shape assertion translates to a `SELECT * FROM widgets WHERE ...` regardless of engine; only the connection and snapshot mechanics differ. Pick the engine, write the snapshot in its dialect, and note in the verdict which engine was exercised.
