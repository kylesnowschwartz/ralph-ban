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

If the library under test exposes only unexported types (`internal/` package layout, lowercase types), the probe must live in a package that has visibility — which usually means writing a small exported wrapper *in the worktree* under `internal/widget/oracle_helper.go`, calling it from the scratch program, and committing the helper as part of the card's evidence. This is the exception, not the rule; prefer to exercise the exported surface.

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

- `0` — probe ran, library returned the expected shape.
- `1` — library returned an error or unexpected shape; the envelope's `error` field carries detail.
- `2` — probe itself is malformed (compile error, missing import). The Oracle should recognise this as a *probe defect*, not a library defect, and rewrite the probe.
- `127` — `GOWORK=off go run` could not resolve the package; usually a path or workspace issue.

The Oracle's verdict distinguishes between "library is broken" (exit 1 with library-side error) and "probe is broken" (exit 2 with compile error). Conflating them leads to false REJECTs.

## Common mistakes

- **Probing through a workspace** — `GOWORK=off` first, every time, in worktrees.
- **Using `fmt.Printf` for output** — fragile parsing. Use `encoding/json`.
- **Adding the probe to the worker's diff** — `.agent-history/` is gitignored; the probe stays out of source.
- **Probing unexported types via reflection** — write a wrapper if the surface is unexported, or stop and ask the planner whether the spec is actually testing the exported surface.
