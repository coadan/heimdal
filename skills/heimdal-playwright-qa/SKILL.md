---
name: heimdal-playwright-qa
description: Use when an agent needs to explore, diagnose, or regression-test a web application from a Git worktree with Playwright as the only browser runtime.
---

# Heimdal Playwright QA

Use Heimdal as the worktree-aware control plane around the official Playwright
CLI. Playwright owns the browser, locators, actionability, assertions, test
fixtures, and test reports. Heimdal owns project startup, isolated ports,
session artifacts, and compact agent-facing evidence.

## Choose the workflow

Use an interactive session when the behavior is unknown, a visual regression
needs investigation, or an agent must explore before writing a test:

```bash
heimdal doctor --json
heimdal session start --root <worktree> --name <short-worktree-name> --headed
heimdal session observe
```

Use a deterministic test run when the relevant Playwright spec and behavior are
already known:

```bash
heimdal run -- tests/browser/<flow>.spec.ts --grep "<behavior>"
```

If `doctor` reports that `playwright-cli` is unavailable, install the official
agent CLI into the project:

```bash
heimdal install agent-cli
```

If the Playwright agent browser is missing, install it once with
`heimdal install agent-browser chromium`. Install the repository's test browser
with `heimdal install chromium` when deterministic tests need it.

## Interactive loop

The intended loop is `observe → act → observe → diagnose → codify`:

```bash
heimdal session observe
heimdal session click e12
heimdal session fill e5 "hello"
heimdal session press Enter
heimdal session screenshot
heimdal session diagnose --json
heimdal session save --test tests/browser/<new-flow>.spec.ts
heimdal session stop
```

`observe` returns an accessibility snapshot with element refs and bounding
boxes. State-changing actions automatically return the post-action snapshot
with boxes. Read-only commands return their useful result. Heimdal keeps the
default response compact by using Playwright raw output and snapshot depth 5;
use `--verbose` on any session command when the full Playwright CLI response is
needed. All stdout/stderr, snapshots, and screenshots are still preserved in
the session artifact directory. Use explicit `observe` after read-only
commands when you need fresh refs.

Use refs from the latest observation (`e12`, `e5`, and so on). Re-observe after
navigation or a DOM mutation because refs are invalidated when the page changes.
Prefer refs or user-facing Playwright locators over CSS/XPath. Use screenshots
for layout, canvas, charts, and visual evidence; use snapshots for structure and
interaction. Forward Playwright CLI-specific options after `--`, for example:

```bash
heimdal session observe -- --depth=4
heimdal session screenshot -- --full-page
heimdal session click e12 --verbose
```

Coordinate actions are a fallback for canvas/custom widgets. Record the
viewport and screenshot dimensions when reasoning from coordinates, and never
use coordinates when a semantic ref is available.

## Diagnosis and evidence

For an interactive failure, inspect the machine-readable session result and
preserved evidence. `status` and all other session commands can resolve a
named session from the root recorded at `start`, even when the agent's current
directory has changed:

```bash
heimdal session status --json
heimdal session diagnose --json
```

The session directory contains the session state, generated Playwright CLI
config, action transcript, snapshots, screenshots, console/network output, and
fixture logs. Keep it until the failure is understood. Do not put credentials
in command arguments, screenshots, traces, or generated test files.

For a failing deterministic `heimdal run`, use its report and trace commands.
For a failing Playwright test, use the Playwright CLI debugger when available:

```bash
heimdal report --run latest --json
heimdal trace --run latest
npx playwright test <spec> --debug=cli
playwright-cli attach <session-name>
playwright-cli snapshot --boxes
playwright-cli console error
playwright-cli requests
playwright-cli step-over
```

## Saving an exploration

`heimdal session save` writes a Markdown transcript. With `--test`, it also
writes a Playwright TypeScript draft and records semantic locators when the
Playwright CLI can generate them. Review the draft, replace any TODO locator,
and add assertions that prove the user-facing outcome. A recorded interaction
is not a complete test until it has an explicit assertion.

## Project contract

Projects with an already-running app only need a URL:

```bash
heimdal session start --url http://127.0.0.1:3000
```

Fixture-backed projects can add `.heimdal.json`:

```json
{
  "version": 1,
  "session": {
    "command": ["npm", "run", "fixture:dev"],
    "url": "http://127.0.0.1:${PORT}",
    "run_id_env": "APP_QA_RUN_ID",
    "port_env": "APP_QA_PORT",
    "env": { "APP_QA_DB": "${RUN_ID}" },
    "browser": "chromium",
    "server_timeout_ms": 45000
  }
}
```

Commands are argument arrays, not shell strings. Session values may use
`${RUN_ID}`, `${RUN_DIR}`, `${OUTPUT_DIR}`, `${REPORT_DIR}`, `${ROOT}`,
`${BRANCH}`, `${PORT}`, `${SESSION}`, and `${URL}`.
Set `session.runner` to an argument array when the project cannot resolve its
local `playwright-cli` executable automatically.

## QA rules

- Read the repository's `AGENTS.md` and the docs that own the flow first.
- Drive real user-facing controls and assert rendered/subscribed outcomes.
- Do not call reducers, APIs, databases, private hooks, or ad hoc scripts to
  manufacture a state the user could not reach.
- Prefer one focused flow over the full suite; do not hide failures with
  retries or arbitrary waits.
- Run from the current Git worktree. Keep each session and test artifact set
  isolated from other worktrees.
- Use Playwright as the only browser runtime. Do not substitute the Codex
  in-app browser, MCP browser control, Selenium, or another automation layer.
- If the fixture cannot start, classify it as an environment/fixture failure
  and preserve the command and logs.
