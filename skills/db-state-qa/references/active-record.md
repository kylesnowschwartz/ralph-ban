# Rails / ActiveRecord state assertion for db-state-qa

For Rails apps, the database is reachable two ways: through ActiveRecord (the ORM, with autoload, schema cache, validations, callbacks) and through raw SQL (`psql`, `mysql`, `sqlite3`). The choice matters more than it appears.

## When to use AR, when to use raw SQL

| Asserting | Surface | Why |
|---|---|---|
| "row exists with attributes X" | AR or raw SQL — both work | AR is more readable; raw SQL is engine-direct |
| "audit_log was not touched" | raw SQL | Avoids loading AR for tables not asserted on |
| "schema has column Y" | raw SQL | AR's schema cache may be stale; catalogue is authoritative |
| "validation prevents bad input" | AR | The validation lives in the AR model; raw SQL bypasses it |
| "callback fired and updated cache" | AR | The callback is AR-internal; raw SQL sees the *result* but not the *cause* |
| "ActsAsTaggable / Paper Trail / Audited gem behaved" | AR | Side-effect chains live in AR plumbing |
| "sql query is the expected shape" | raw SQL or `EXPLAIN` | AR's generated SQL varies with version |

The default for the Oracle: assert through AR when the spec is in AR's vocabulary (model methods, validations, callbacks); raw SQL when the spec is structural (rows, schema, indexes, constraints).

## bin/rails runner — the canonical surface

`bin/rails runner` boots the Rails environment, runs a script, and exits. Three forms:

**Inline string:**
```bash
bin/rails runner -e development \
  'puts({widgets: Widget.count, recent: Widget.order(created_at: :desc).limit(3).pluck(:id, :name)}.to_json)'
```

**Script file:**
```bash
bin/rails runner -e development ./.agent-history/oracle/CARD-ID/scratch/snapshot.rb
```

**Reading from stdin:**
```bash
bin/rails runner -e development - <<'RUBY'
puts({widgets: Widget.count}.to_json)
RUBY
```

The script-file form is preferred for snapshots longer than a few lines — keeps the snapshot logic in a file the verdict reader can inspect.

## A snapshot script for AR

```ruby
# .agent-history/oracle/CARD-ID/scratch/snapshot.rb
require "json"

snapshot = {
  widgets: {
    count: Widget.count,
    rows: Widget.where(name: "test").as_json(except: %i[created_at updated_at])
  },
  audit_log: {
    count: AuditLog.count
  },
  schema: {
    widgets_columns: Widget.columns.map { |c| { name: c.name, type: c.type.to_s } },
    widgets_indexes: ActiveRecord::Base.connection.indexes("widgets").map { |i|
      { name: i.name, columns: i.columns, unique: i.unique }
    }
  }
}

puts JSON.dump(snapshot)
```

Volatile fields (`created_at`, `updated_at`) are excluded at the AR layer via `as_json(except: ...)`. Identifiers (`id`) are kept here because the spec's "shall persist a Widget" usually wants to see *some* row appear, even if its specific `id` is not asserted; downstream `jq` normalisation can drop `id` if needed.

## The schema cache trap

ActiveRecord caches the schema on first connection. Methods like `Widget.columns`, `Widget.column_names`, and `Widget.attribute_names` read this cache. After a migration runs *during* the Oracle's exercise, the cache is stale — AR returns the pre-migration column list even though the database has the post-migration columns.

Symptoms:
- A snapshot taken via AR after a migration shows old column names.
- The same snapshot taken via raw SQL shows new column names.

Workarounds:
- `Widget.reset_column_information` clears the cache for that model.
- `ActiveRecord::Base.connection.schema_cache.clear!` clears all model caches.
- Run the snapshot in a fresh `bin/rails runner` invocation — process boundary discards the cache.

For schema assertions, the rule is to use raw SQL via `references/engine-specific.md`. AR's cache is a feature for app code; for the Oracle it is a hazard.

## The autoload boundary

Rails autoloads constants on first reference. A snapshot script that references `Widgets::Renderer` triggers a Zeitwerk load; failure to load (typo, namespace mismatch, missing file) raises `NameError`.

The Oracle reads error messages:
- `uninitialized constant Widgets::Renderer` — the snapshot script has a typo or the constant is genuinely absent. Probe defect; rewrite.
- `wrong number of arguments (given N, expected M)` — the constant exists but the API differs from what the snapshot expected. Library defect; finding.
- `NoMethodError: undefined method 'foo' for Widget:Class` — the method is missing. Library defect; finding.

The first kind is the snapshot's fault; the latter two are findings the Oracle records.

## Transactional fixtures — the inversion

Rails tests run inside transactions by default; each test is wrapped in `BEGIN ... ROLLBACK` so changes do not leak. This is *the* canonical anti-pattern for the Oracle.

The Oracle exercises live state. Wrapping the exercise in a transaction would roll back the action, and the after-snapshot would equal the before-snapshot — yielding a false APPROVE.

Rule: the Oracle's `bin/rails runner` snapshots run *outside* any transaction. If the Oracle is invoked from a test harness that wraps everything in a transaction, the wrapping must be disabled for Oracle exercises.

## Multi-database Rails

Rails 6+ supports multiple databases. A snapshot that queries `Widget.count` reaches whatever database the model is configured against. For a spec that asserts on a non-default database (a sharded read replica, a separate animals database), the snapshot script must connect explicitly:

```ruby
ActiveRecord::Base.connected_to(database: { reading: :animals_replica }) do
  puts({ animals: Animal.count }.to_json)
end
```

Without this, the snapshot reads the default database and silently misses the table the spec is about.

## Sequel as a fallback

`jeremyevans/sequel` is the cleanest portable schema-introspection API across SQLite/Postgres/MySQL — `DB.tables`, `DB.schema(:users)`, `DB.indexes(:users)`. For projects that already depend on Sequel, prefer it over hand-rolling per-engine catalogue queries. For Rails-only projects, AR plus raw catalogue queries is sufficient and avoids the dependency.

## Common mistakes

- **`Widget.connection.execute("SELECT ...")` for snapshots** — works, but allocates AR plumbing for nothing. Use `ActiveRecord::Base.connection.exec_query(sql).to_a` if AR is needed; otherwise raw `psql`/`sqlite3`/`mysql` directly.
- **Reading the schema cache for schema assertions** — stale after migrations. Use raw catalogue queries.
- **Snapshotting inside a transaction** — rolls back the action; verdict is false APPROVE.
- **Forgetting `-e production` when production is the asserted environment** — runner defaults to development.
- **Holding a connection across the action** — schema cache, query cache, and prepared statement cache all participate in stale-read failure modes. Fresh process per snapshot is the cheapest discipline.
