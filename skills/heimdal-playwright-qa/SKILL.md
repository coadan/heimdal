---
name: heimdal-playwright-qa
description: Use when an agent needs to explore, diagnose, or regression-test a web application from a Git worktree with Playwright as the only browser runtime.
---

# Heimdal Playwright QA

Use Heimdal for worktree-isolated browser QA. Playwright owns browser actions,
locators, assertions, fixtures, traces, and reports. Heimdal selects the
project, isolates ports and artifacts, manages interactive app processes, and
returns compact evidence. Do not use another browser runtime.

## Pick the shortest workflow

For a known behavior, run one focused repository-owned test:

```bash
heimdal doctor
heimdal run -- tests/browser/<flow>.spec.ts --grep "<behavior>"
```

For unknown behavior or visual exploration, use the default worktree session:

```bash
heimdal doctor
heimdal session start --headed
heimdal session observe
heimdal session click e12
heimdal session fill e5 "hello"
heimdal session diagnose --json
heimdal session save --test tests/browser/<flow>.spec.ts
heimdal session stop
```

Run from the target worktree. Add `--dir PATH` only when invoking Heimdal from
elsewhere. Use `--name NAME` only for multiple sessions in one worktree or for
lookup from another directory; repeat that name on later commands.

If `doctor` reports a missing component, install only that component:

```bash
heimdal install agent-cli
heimdal install agent-browser chromium
heimdal install chromium
```

The first two support interactive sessions; the last installs the repository's
Playwright test browser.

## Keep interaction bounded

`observe` returns an accessibility snapshot with semantic refs and boxes.
State-changing actions already return a fresh post-action snapshot, so do not
observe again unless a read-only command or external change may have made refs
stale. Prefer current refs and user-facing locators over CSS, XPath, or
coordinates.

Use snapshots for structure and interaction. Request a screenshot only for
layout, canvas, charts, or visual evidence. Use coordinates only when no
semantic target exists, and record the viewport and screenshot dimensions.

Default session output is compact and snapshots stop at depth 5. Use
`--verbose` only when the compact result omits a fact needed for the next
decision. Forward uncommon Playwright CLI options after `--`:

```bash
heimdal session observe -- --depth=4
heimdal session screenshot -- --full-page
```

## Diagnose from summaries first

For an interactive failure, start with one bounded diagnostic packet:

```bash
heimdal session status --json
heimdal session diagnose --json
```

For a deterministic run, inspect the live or final report before opening raw
artifacts:

```bash
heimdal report --run latest --json
heimdal trace --run latest
```

Only inspect retained stdout, stderr, snapshots, screenshots, console/network
logs, or fixture logs when the summary points to them. Never put credentials in
commands, screenshots, traces, metadata, or generated tests.

`session save --test` creates a TypeScript draft, not a finished regression
test. Replace TODO locators and add an assertion for the user-visible outcome.

## Project and fixture contract

Connect to an existing app with `session start --url URL`. To let Heimdal start
and stop the app, define the smallest useful `.heimdal.json`:

```json
{
  "version": 1,
  "session": {
    "command": ["npm", "run", "fixture:dev"],
    "url": "http://127.0.0.1:${PORT}",
    "server_timeout_ms": 45000
  }
}
```

Commands are argument arrays, not shell strings. Supported templates include
`${RUN_ID}`, `${RUN_DIR}`, `${ROOT}`, `${BRANCH}`, `${PORT}`, and `${URL}`.

Fixtures receive `HEIMDAL_RUN_ID`, `HEIMDAL_RUN_DIR`,
`HEIMDAL_RUN_METADATA_DIR`, and `HEIMDAL_RUN_SIGNALS_DIR`. Publish only small,
non-secret JSON owned by one namespace:

```bash
heimdal metadata publish fixture.diagnostics --file ./metadata.json
heimdal metadata get fixture.diagnostics --run latest --json
```

Use idempotent named milestones instead of sleeps or ad hoc polling:

```bash
heimdal signal send fixture.ready
heimdal signal wait fixture.ready --run latest --timeout 2m
```

Inside a running fixture, metadata and signal commands use
`HEIMDAL_RUN_DIR`; an outside process selects `--run ID|latest`.

## Invariants

- Read the target repository's `AGENTS.md` and owning QA docs first.
- Drive real controls and assert user-visible outcomes; do not manufacture
  state through private APIs, databases, hooks, or reducer calls.
- Prefer one focused flow; do not hide failures with retries or arbitrary
  waits.
- Preserve evidence for failed startup or fixture behavior, classify the
  boundary correctly, and stop interactive sessions when finished.
- Keep every run and session in its originating Git worktree.
