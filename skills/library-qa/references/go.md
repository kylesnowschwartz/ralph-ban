# Go scratch programs for library-qa

Go's compilation model makes scratch programs cheap: a single-file `main` package compiles and runs in one command, and the only ceremony is the import path.

## The minimal probe

```go
// .agent-history/oracle/CARD-ID/scratch/probe.go
package main

import (
    "encoding/json"
    "fmt"
    "os"

    "github.com/kylesnowschwartz/ralph-ban/internal/widget"
)

type result struct {
    OK     bool   `json:"ok"`
    Output string `json:"output,omitempty"`
    Error  string `json:"error,omitempty"`
}

func main() {
    out, err := widget.Render(widget.Input{Name: "test"})
    enc := json.NewEncoder(os.Stdout)

    if err != nil {
        _ = enc.Encode(result{OK: false, Error: err.Error()})
        os.Exit(1)
    }
    _ = enc.Encode(result{OK: true, Output: out})
}
```

Run from the worktree root:

```bash
GOWORK=off go run ./.agent-history/oracle/CARD-ID/scratch/
```

`GOWORK=off` is non-negotiable in worktrees — without it, the workspace file at the parent repo's root may resolve module paths to the wrong checkout, and the probe exercises code that is not on the worker's branch.

## Why a separate `main` package, not a `_test.go`

A `_test.go` file would require running `go test`, which means matching test signatures (`TestXxx(t *testing.T)`), interleaving with existing test output, and putting the probe in the worker's diff if not careful. A scratch `main.go` in `.agent-history/` is outside the module's test surface entirely; it compiles, runs, prints, and the file path makes its scratch-ness obvious to any reader.

## Module-internal types

If the library under test exposes only unexported symbols (lowercase types, `internal/` package layout), the scratch-only contract still holds — do *not* add a helper file to the worker's source tree. The probe should exercise the *exported* surface, because that is what callers see, which is what the spec is about. If the spec genuinely names an unexported symbol (rare, and a smell in itself), the verdict is `could-not-determine` and the planner should rewrite the spec to reference an exported observable. Adding source files to the worker's branch to make a probe possible would change what the reviewer is reviewing, which is exactly what the scratch-only rule prevents.

## Workspace gotchas

`go.work` at the parent repo's root references multiple modules by relative path. In a worktree, the relative paths point to directories that may not exist (the `../beads-lite` sibling is a typical case). The symptoms are:

- `go run` fails with `directory not found: ../beads-lite`
- `go run` succeeds but uses a stale version of the sibling

Both are `GOWORK=off` problems. Setting it disables the workspace and falls back to `go.mod`'s declared dependencies — which is what the worker's branch is supposed to build against anyway.

## Capturing structured output

Go's `encoding/json` handles the envelope cleanly. Use `json.NewEncoder(os.Stdout).Encode(...)` rather than `fmt.Printf("%+v", ...)` so the Oracle's parser does not need to invent a syntax for the printed value.

For probes that need to emit multiple events (NDJSON), encode each event in its own call:

```go
emit := func(event string, payload any) {
    _ = json.NewEncoder(os.Stdout).Encode(map[string]any{"event": event, "payload": payload})
}

emit("input", input)
result, err := widget.Render(input)
if err != nil {
    emit("error", err.Error())
    os.Exit(1)
}
emit("result", result)
```

`Encoder.Encode` writes a trailing newline; the Oracle reads NDJSON one line at a time.

## Exit codes for Go probes

`go run` exits `0` on success and `1` on any failure (compile error, runtime panic, `os.Exit(N)` with non-zero from the probe). It does *not* differentiate "library broke" from "probe is malformed" via the exit code — both surface as `1` plus a stderr message. The Oracle distinguishes them by *reading stderr*, not by the code:

- Stderr contains `cannot find package` / `undefined:` / `expected type` → probe defect (the scratch program does not compile against the library's API). Rewrite the probe.
- Stderr is the program's own panic or error envelope → library defect (the library compiled but misbehaved at runtime). Record as a finding.
- Stderr is empty and exit is `1` → the probe called `os.Exit(1)` deliberately; consult the structured output for what the envelope said.

If the probe wraps its own logic, it can map specific outcomes to specific codes via `os.Exit(N)`. That mapping is the probe's contract, not Go's; document it inline. `go run` itself only signals success-or-failure with `0`/`1`.

## Common mistakes

- **Probing through a workspace** — `GOWORK=off` first, every time, in worktrees.
- **Using `fmt.Printf` for output** — fragile parsing. Use `encoding/json`.
- **Adding the probe to the worker's diff** — `.agent-history/` is gitignored; the probe stays out of source.
- **Probing unexported types via reflection** — write a wrapper if the surface is unexported, or stop and ask the planner whether the spec is actually testing the exported surface.
