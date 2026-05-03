---
name: browser-qa
description: This skill should be used when the user asks to "QA browser changes", "verify a web UI", "test the browser flow", "check the page works", "validate the dev server", or wants end-to-end behavioral verification of web pages or web apps. Drives a real browser to navigate, snapshot, interact, and capture evidence — supports both agent-browser (preferred) and playwright-cli, picking whichever is installed.
argument-hint: "[scope of changes to verify]"
---

# browser-QA

Verify that web changes work by driving a real browser — start the dev server, navigate, snapshot, interact, observe, capture evidence. This skill does not write code; it builds, runs, observes, and reports.

**Scope**: $ARGUMENTS

If no scope was provided, read the recent changeset to determine what needs verification.

## Tool Detection

Pick the browser driver before anything else. Both tools share the same conceptual model — open, snapshot for element refs, interact by ref, re-snapshot, capture — but the command grammars differ.

```bash
if command -v agent-browser >/dev/null 2>&1; then
  DRIVER=agent-browser
elif command -v playwright-cli >/dev/null 2>&1; then
  DRIVER=playwright-cli
else
  echo "ESCALATE: install agent-browser (preferred) or playwright-cli before browser QA."
  exit 1
fi
echo "Using driver: $DRIVER"
```

**Driver-specific reference (load on demand):**

- `agent-browser` (preferred — has `diff snapshot`, content-boundaries security, annotated screenshots, auth vault): `references/agent-browser/overview.md` plus the rest of `references/agent-browser/` for deep detail.
- `playwright-cli` (fallback): `references/playwright-cli/overview.md` plus the rest of `references/playwright-cli/`.
- `chrome-browser` MCP toolset (`mcp__claude-in-chrome__*`): if the MCP is loaded, those tools work directly — this skill is for CLI-driven browser QA, but the MCP is an equally valid path that does not require either CLI binary.

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

Web apps don't drive themselves. Most browser QA fails by skipping the readiness check, not by missing a click.

```bash
SERVER_LOG="/tmp/qa-server-$$.log"
npm run dev >"$SERVER_LOG" 2>&1 &
SERVER_PID=$!
trap '[ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null; "$DRIVER" close --all 2>/dev/null' EXIT

# Wait for ready — poll the URL, do not assume
for i in $(seq 1 60); do
  if curl -sf http://localhost:5173 >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done
```

Replace `npm run dev` and `5173` with project-specific commands and ports. For Rails: `bin/rails server -p 3000`. For Next.js: `npm run dev` (default port 3000). For Vite: `npm run dev` (default port 5173).

## Browser Workflow Pattern

Both drivers follow the same logical shape. The reference files document exact syntax for each.

```
$DRIVER open <url>            # navigate
# wait for SPA hydration:
#   agent-browser: $DRIVER wait --load networkidle
#   playwright-cli: see references/playwright-cli/overview.md
$DRIVER snapshot              # capture accessibility tree, get element refs (agent-browser uses @e1, playwright-cli uses e1)
# inspect snapshot output, identify the ref you need
$DRIVER click <ref>           # interact
$DRIVER snapshot              # RE-snapshot — refs invalidate after every navigation
$DRIVER screenshot <path>     # evidence
$DRIVER close
```

The single most common bug is using a stale ref after navigation. Re-snapshot after every action that may change the DOM.

## Evidence Capture for the Oracle

Save artefacts under `.agent-history/oracle/<card-id>/<timestamp>/`:

- `before.png` — screenshot before the asserted action
- `after.png` — screenshot after
- `snapshot-before.txt` — accessibility tree before
- `snapshot-after.txt` — accessibility tree after
- `console.log` — console messages captured during the flow
- `diff.txt` — for `agent-browser`, run `agent-browser diff snapshot` after the action; for `playwright-cli`, capture before/after snapshots and run `diff` against them

The transcript directory is the Oracle's proof-of-work. An `APPROVE` verdict without a transcript is the failure mode the Oracle exists to prevent.

## Rules

- **Detect the driver first.** Do not assume which CLI is installed. The detection block belongs at the top of the script, before any tool-specific commands.
- **Always start the server before driving.** A browser pointed at a non-listening port produces useless errors. Start, poll for ready, then drive.
- **Always trap teardown.** Servers and browser sessions leak processes. Use `trap … EXIT` so cleanup happens on success, failure, and interrupt.
- **Re-snapshot after every navigation.** Element refs invalidate. Stale refs produce confusing errors that look like bugs in the page.
- **Capture evidence as you go, not at the end.** A test that "looked right at the time" without saved screenshots and snapshots is not evidence. The transcript directory is the deliverable.
- **Name browser sessions when running concurrently.** `agent-browser --session qa-<timestamp>`; `playwright-cli -s=qa-<timestamp>`. Without a session name, parallel QAs collide.
- **Don't fix anything.** Report what's broken. This skill is QA, not implementation.

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
