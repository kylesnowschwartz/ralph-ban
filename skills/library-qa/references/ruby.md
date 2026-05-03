# Ruby probes for library-qa

Ruby has two distinct probing surfaces: pure gems (loadable via `require`) and Rails-loaded code (loadable only through the framework's autoloader). The probe choice depends on which surface the library under test occupies.

## Pure gem probe — `bundle exec ruby -e`

For a gem with no Rails dependency, `bundle exec ruby -e` is the floor:

```bash
bundle exec ruby -e '
require "json"
require "widget"

result = Widget.render(name: "test")
puts JSON.dump({ok: true, output: result})
' > "$TXN/stdout.txt" 2> "$TXN/stderr.txt"
echo "$?" > "$TXN/exit.txt"
```

The `-e` form keeps the probe inline; for anything more than a few lines, write the probe to `.agent-history/oracle/CARD-ID/scratch/probe.rb` and run it:

```bash
bundle exec ruby ./.agent-history/oracle/CARD-ID/scratch/probe.rb
```

`bundle exec` resolves dependencies through the project's `Gemfile.lock`; without it, the probe loads whatever gems are globally installed, which may not match the worker's branch.

## Rails-loaded probe — `bin/rails runner`

For app code (models, jobs, mailers, anything that depends on Rails autoloading), `bundle exec ruby -e 'require "user"'` will fail or load the wrong file. The canonical surface is `bin/rails runner`:

```bash
bin/rails runner -e development '
puts({
  ok: true,
  user_count: User.count,
  active_count: User.where(active: true).count,
}.to_json)
' > "$TXN/stdout.txt" 2> "$TXN/stderr.txt"
echo "$?" > "$TXN/exit.txt"
```

`bin/rails runner` boots the Rails environment, runs the script in that context, and exits. The `-e development` flag chooses the environment explicitly — without it, runner uses `RAILS_ENV` or defaults to `development`.

For probes longer than a handful of lines, write to scratch and pass the path:

```bash
bin/rails runner -e development ./.agent-history/oracle/CARD-ID/scratch/probe.rb
```

## Rails environment side effects

`bin/rails runner` runs in a real Rails environment. That means:

- Database connections are made. The probe sees real data unless a fixture is loaded first.
- Initializers run. A probe that checks "the library does X" may instead observe "the library does X *plus whatever an initializer mutated*."
- Logging goes to `log/development.log` by default, mixed with whatever else is running in that environment.

For probes that must not touch real data, consult `db-state-qa` for the transactional pattern.

## Probing internal modules

Ruby's autoloader (Zeitwerk in Rails 6+) loads constants on first reference. Inside `bin/rails runner`, simply naming the constant triggers the load:

```ruby
# probe.rb
result = Widgets::Renderer.new(name: "test").call
puts({ok: !result.nil?, output: result&.to_s}.to_json)
```

If the constant cannot be loaded (typo, namespace mismatch), Zeitwerk raises a `NameError` at the line that references it. The Oracle reads the error message: `uninitialized constant Widgets::Renderer` is a probe defect (wrong name), `wrong number of arguments` is a library API mismatch.

## Structured output

Use `to_json` everywhere the Oracle parses output. `puts obj.inspect` produces a Ruby-syntax string, which is parseable but not in any standard tool's vocabulary. `puts obj.to_json` produces JSON.

For NDJSON probes:

```ruby
emit = ->(event, payload) {
  puts({event: event, payload: payload}.to_json)
}

emit.call("input", {name: "test"})
result = Widget.render(name: "test")
emit.call("result", {ok: true, output: result})
```

## Exit codes for Ruby probes

Ruby's default exit code is `0` on graceful exit, `1` on uncaught exception. `bin/rails runner` follows the same convention. To assert a specific exit code from the probe:

```ruby
begin
  result = Widget.render(...)
  puts({ok: true}.to_json)
rescue Widget::ValidationError => e
  puts({ok: false, kind: "validation", message: e.message}.to_json)
  exit 2
rescue => e
  puts({ok: false, kind: "unexpected", message: e.message}.to_json)
  exit 1
end
```

Exit `2` for "the library returned an expected error type" is distinct from exit `1` for "something unexpected blew up." The Oracle reads the code to disambiguate.

## Common mistakes

- **`ruby -e` instead of `bundle exec ruby -e`** — loads the wrong gems. Always `bundle exec`.
- **`ruby` instead of `bin/rails runner` for app code** — autoload won't fire. The probe fails with `NameError` and the Oracle wastes time debugging the probe.
- **`puts result` instead of `puts result.to_json`** — produces Ruby-`inspect` output. Hard to parse. Always JSON.
- **No environment isolation** — a probe in the development environment writes to the development database. Use a fixture, a transactional block, or a dedicated test database.
- **Putting the probe under `app/` or `lib/`** — the probe lives in scratch (`.agent-history/`), not in source.
