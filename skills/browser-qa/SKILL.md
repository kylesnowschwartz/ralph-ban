---
name: browser-qa
description: This skill should be used when the user asks to "QA browser changes", "verify a web UI", "test the browser flow", "check the page works", "validate the dev server", or wants end-to-end behavioral verification of web pages or web apps. Drives a real browser to navigate, snapshot, interact, and capture evidence — supports both agent-browser (preferred) and playwright-cli, picking whichever is installed.
argument-hint: "[scope of changes to verify]"
---

# browser-QA

Drive a real browser: start the dev server, navigate, snapshot, interact, observe, capture evidence.

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine what needs verification.

## Tool Detection

Three viable drivers; check in order.

```bash
if [ -n "${MCP_CHROME_LOADED:-}" ] || command -v claude-mcp-chrome >/dev/null 2>&1; then
  DRIVER=mcp-chrome   # use mcp__claude-in-chrome__* tools directly
elif command -v agent-browser >/dev/null 2>&1; then
  DRIVER=agent-browser
elif command -v playwright-cli >/dev/null 2>&1; then
  DRIVER=playwright-cli
else
  echo "ESCALATE: no browser driver available. Install agent-browser (preferred), playwright-cli, or load the chrome-browser MCP." >&2
  exit 1
fi
echo "Using driver: $DRIVER"
```

If MCP is loaded, `mcp__claude-in-chrome__*` tools are the native interface and the CLI examples below translate to tool calls.

**Driver-specific references (load on demand):**

- `agent-browser` (preferred CLI): `references/agent-browser/overview.md`.
- `playwright-cli` (fallback CLI): `references/playwright-cli/overview.md`.
- `chrome-browser` MCP: tool schemas load on demand via `ToolSearch`.

## Workflow

1. Read the changeset — `git diff`, `git log`, understand what UI surface changed.
2. Detect the dev-server start command — look for `package.json` scripts, `Procfile`, `bin/rails server`, `vite.config.*`, framework conventions. Read `README` and `CLAUDE.md` for project specifics.
3. Start the dev server in the background; trap teardown so it dies even on failure.
4. Wait for the server to be ready (poll the URL with `curl`, do not sleep-and-hope).
5. Drive the browser with `$DRIVER`: open the URL, take a snapshot, identify elements by ref, interact, re-snapshot after every navigation.
6. Verify the asserted behavior — accessibility-tree comparison, screenshot, console messages, network calls.
7. Capture evidence to `.agent-history/oracle/<card-id>/<timestamp>/`.
8. Kill the browser session and the dev server.
9. Report PASS / FAIL / PARTIAL with evidence.

## Server Lifecycle

```bash
SERVER_LOG="/tmp/qa-server-$$.log"
PORT=5173
npm run dev >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!
trap '[ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null; [ "$DRIVER" != mcp-chrome ] && "$DRIVER" close --all 2>/dev/null' EXIT

# Poll readiness *and* watch the process — exit if it dies before responding
ready=0
for i in $(seq 1 60); do
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    echo "dev server died before becoming ready (see $SERVER_LOG)" >&2
    exit 1
  fi
  if curl -sf "http://localhost:$PORT" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 0.5
done
[ "$ready" = 1 ] || { echo "dev server failed to become ready in 30s (see $SERVER_LOG)" >&2; exit 1; }
```

Three operational details:

- Poll the URL, don't sleep-and-hope.
- Watch the process with `kill -0` so a crashed boot fails fast, not against a corpse.
- Don't trust the configured port — Vite, Next.js, and others auto-bump (`5173 → 5174`, `3000 → 3001`) when occupied. Parse `$SERVER_LOG` for the bound port if the spec is port-sensitive.

Replace `npm run dev` and `5173` with the project's command and port (Rails: `bin/rails server -p 3000`; Next.js: 3000; Vite: 5173).

## Browser Workflow Pattern

Both drivers share this shape; references document exact syntax.

```
$DRIVER open <url>            # navigate
# wait for SPA hydration:
#   agent-browser: $DRIVER wait --load networkidle
#   playwright-cli: see references/playwright-cli/overview.md
$DRIVER snapshot              # accessibility tree + element refs (agent-browser @e1, playwright-cli e1)
$DRIVER click <ref>           # interact
$DRIVER snapshot              # RE-snapshot — refs invalidate
$DRIVER screenshot <path>     # evidence
$DRIVER close
```

Three subtleties:

- **Refs invalidate on any DOM change**, not only navigation. Modals, client rerenders, infinite scroll, optimistic-update revert all invalidate. Re-snapshot after any action that may mutate the DOM.
- **`networkidle` does not fire** for websocket / SSE / long-polling apps; the wait times out. Wait on a UI condition (element appears, class changes) instead.
- **Assert against the accessibility tree, not the screenshot.** A 1-pixel diff is not a defect; the a11y tree is text-based and stable.

## Evidence Capture for the Oracle

Save artefacts under `.agent-history/oracle/<card-id>/<timestamp>/`:

- `before.png` — screenshot before the asserted action
- `after.png` — screenshot after
- `snapshot-before.txt` — accessibility tree before
- `snapshot-after.txt` — accessibility tree after
- `console.log` — console messages captured during the flow
- `diff.txt` — `agent-browser diff snapshot` after the action; for `playwright-cli`, `diff` the captured before/after snapshots

## Rules

- **Detect the driver first.** Do not assume which CLI is installed. The detection block belongs at the top of the script, before any tool-specific commands.
- **Always start the server before driving.** A browser pointed at a non-listening port produces useless errors. Start, poll for ready, then drive.
- **Always trap teardown.** Servers and browser sessions leak processes. Use `trap … EXIT` so cleanup happens on success, failure, and interrupt.
- **Re-snapshot after every navigation.** Element refs invalidate. Stale refs produce confusing errors that look like bugs in the page.
- **Capture evidence as you go, not at the end.** A test that "looked right at the time" without saved screenshots and snapshots is not evidence. The transcript directory is the deliverable.
- **Name browser sessions when running concurrently.** `agent-browser --session qa-<timestamp>`; `playwright-cli -s=qa-<timestamp>`. Without a session name, parallel QAs collide.
## Report Format

```
## Browser QA Report

**Scope**: <what was verified>
**Driver**: agent-browser | playwright-cli | chrome-browser MCP
**Verdict**: PASS | FAIL | PARTIAL

### Server
- [PASS/FAIL] `<dev server command>` — <port, ready time>

### Navigation
- [PASS/FAIL] `<url>` — <observed page state>

### Interactions
- [PASS/FAIL] <action> — <observed response>
  - Evidence: `<path-to-screenshot-or-snapshot>`

### Console
- <any console errors observed during the flow>

### Issues
1. <description with reproduction steps and evidence path>

### Transcript
Path: `.agent-history/oracle/<card-id>/<timestamp>/`
Contents: <brief listing of what's in there>
```
