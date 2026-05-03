# TypeScript probes for library-qa

TypeScript probing has the most ceremony of the three languages — the runner has to compile, the module system has to resolve, the path mappings have to be honoured. The runner of choice is `tsx`; the alternative is `ts-node`.

## The minimal probe

```ts
// .agent-history/oracle/CARD-ID/scratch/probe.ts
import { render } from "../../../../src/widget";

interface Envelope {
  ok: boolean;
  output?: string;
  error?: string;
}

async function main(): Promise<void> {
  try {
    const out = await render({ name: "test" });
    const env: Envelope = { ok: true, output: out };
    process.stdout.write(JSON.stringify(env) + "\n");
  } catch (e) {
    const env: Envelope = { ok: false, error: (e as Error).message };
    process.stdout.write(JSON.stringify(env) + "\n");
    process.exit(1);
  }
}

void main();
```

Run from the worktree root:

```bash
npx tsx --no-cache ./.agent-history/oracle/CARD-ID/scratch/probe.ts \
  > "$TXN/stdout.txt" 2> "$TXN/stderr.txt"
echo "$?" > "$TXN/exit.txt"
```

`--no-cache` is hygiene, not correctness: `tsx` keeps a transpile cache that on hot re-runs of the same probe can serve a stale compilation. Passing it on every Oracle run is cheap insurance against the rare case where the cache outlasts the change. It is *not* a load-bearing flag — most probe runs would work without it.

## ESM vs CJS — the dual-package hazard

TypeScript libraries today ship one of three forms:

- **CJS only** — `"main": "dist/index.js"`, no `"exports"`, no `"type": "module"`.
- **ESM only** — `"type": "module"` with ESM-shaped `"exports"`.
- **Dual package** — separate `"import"` and `"require"` paths in `"exports"`.

A probe that imports the library may resolve to ESM in one environment and CJS in another. When this happens silently, two copies of the library are loaded — one ESM, one CJS — and module-level singletons (caches, registries, error classes) diverge. An `instanceof` check fails because the class came from the wrong copy.

Symptoms in the Oracle's transcript:

- `instanceof` returns false for an object the spec says should be an instance.
- Two registry entries appear when one is expected.
- Module-level mutable state (e.g., a counter) reads as zero after an operation that should have incremented it.

The fix is environmental: ensure `tsx` is resolving the same shape the worker's package.json declares. Check `package.json`'s `"type"` and `"exports"`; if the probe imports the path that ESM resolves to but the library was built for CJS, the import works but the runtime semantics are wrong.

## tsconfig path mappings

If the project uses path aliases (`"@/widget": "./src/widget"`), `tsx` resolves them through whichever `tsconfig.json` it discovers first — typically the nearest one walking up from the probe file, with `TSCONFIG_PATH` as an override. A probe in `.agent-history/oracle/CARD-ID/scratch/` may resolve a different tsconfig than `src/`, depending on where `tsconfig.json` files live in the tree, and that mismatch produces "module not found" failures even when the alias is otherwise correct.

Two workarounds:

- Use relative imports in the probe (`../../../../src/widget`) — boring but always works regardless of tsconfig discovery.
- Pin the tsconfig: `TSCONFIG_PATH=./tsconfig.json npx tsx --no-cache probe.ts`, or pass `--tsconfig ./tsconfig.json` if the installed `tsx` version supports it.

The relative-import form is simpler and survives discovery-order changes; prefer it.

## Async, top-level await, and `void main()`

Top-level await works in ESM but not in CJS. To stay portable across both, wrap the probe body in an `async main()` and call it as `void main()` — the `void` avoids the unhandled-promise warning.

For NDJSON probes:

```ts
const emit = (event: string, payload: unknown): void => {
  process.stdout.write(JSON.stringify({ event, payload }) + "\n");
};

async function main(): Promise<void> {
  emit("input", { name: "test" });
  const result = await render({ name: "test" });
  emit("result", { ok: true, output: result });
}

void main();
```

`process.stdout.write` over `console.log` because `console.log` has its own buffering and timestamping in some Node versions; `process.stdout.write` is the lower-level, more predictable channel.

## Exit codes for TypeScript probes

Node defaults to `0` on graceful exit. An uncaught rejection in modern Node (`--unhandled-rejections=throw`) exits `1`. Use `process.exit(N)` explicitly when the spec asserts a specific code:

```ts
catch (e) {
  if (e instanceof ValidationError) {
    emit("error", { kind: "validation", message: e.message });
    process.exit(2);
  }
  emit("error", { kind: "unexpected", message: (e as Error).message });
  process.exit(1);
}
```

Exit `2` for "library returned an expected error type" mirrors the convention used in the Ruby and Go references; consistency across languages keeps the Oracle's verdict-table interpretation simple.

## Bundler vs runtime

If the library under test is normally consumed through a bundler (Vite, Webpack, esbuild), the probe's `tsx` path is *different from production*. Bundlers do tree-shaking, dead-code elimination, and sometimes module replacement; `tsx` does none. A library that "works" under `tsx` may still be broken in production, and vice versa.

The Oracle should record which path was exercised, so the verdict reads honestly:

```
Probe runner: tsx --no-cache (no bundler)
```

If the spec asserts behaviour that depends on the bundler (e.g., "shall be tree-shakable"), `library-qa` is the wrong surface. Mark the spec `could-not-determine` and recommend a build-and-import probe instead.

## Other gotchas

- **`package.json#imports`** — subpath imports (`"#widget": "./src/widget.ts"`) are a separate resolution mechanism from `paths`; `tsx` honours them, but only when the probe's nearest `package.json` is the one that declares them.
- **CJS-only deps imported from an ESM probe** — many older packages export only CJS. ESM probes that `import` them go through Node's CJS interop, which does not always preserve named exports. Use `import pkg from 'thing'; const { fn } = pkg;` when the named-import form fails.
- **Native modules** — Node `.node` addons compiled for one Node version may fail to load under another; `tsx` runs against whichever Node is on PATH.

## Common mistakes

- **Stale tsx cache on hot re-runs** — `--no-cache` defends against it; not load-bearing but cheap.
- **Ignoring the dual-package hazard** — silent two-copy loads produce confusing `instanceof` failures.
- **Using `console.log` for the envelope** — Node-version-dependent buffering. Use `process.stdout.write` with explicit `"\n"`.
- **Top-level await in CJS** — fails to compile. `void main()` everywhere.
- **Putting the probe in `src/`** — pulls into the worker's diff and possibly into the build. `.agent-history/` is gitignored.
