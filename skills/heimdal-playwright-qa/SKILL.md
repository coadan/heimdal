---
name: heimdal-playwright-qa
description: Use when an agent needs to explore, diagnose, or regression-test a web application from a Git worktree with Playwright as the only browser runtime.
---

# Heimdal Playwright QA

Use Heimdal for worktree-isolated browser QA. Playwright remains the only
browser runtime and owns actions, locators, assertions, traces, and reports.
Heimdal manages project discovery, sessions, app processes, and compact
evidence.

## Pick the shortest workflow

Use one focused repository test for known behavior:

```bash
heimdal doctor
heimdal run -- tests/browser/<flow>.spec.ts --grep "<behavior>"
```

Use one persistent session for exploration:

```bash
heimdal doctor
heimdal session start
heimdal session click e12
heimdal session fill e5 "hello"
heimdal session diagnose --json
heimdal session stop
```

Run from the target worktree. Add `--dir PATH` from elsewhere. Use `--name`
only for concurrent sessions or cross-directory lookup. Sessions are headless
by default; add `--headed` when a person needs to inspect the browser.

If `doctor` reports a missing component, install only that component:

```bash
heimdal install agent-cli
heimdal install agent-browser chromium
heimdal install chromium
```

The first two support sessions; the last supports repository tests.

## Keep interaction bounded

`session start` and state-changing actions return a fresh accessibility
snapshot. Do not immediately call `observe` again. Prefer current refs and
user-facing locators over CSS, XPath, or coordinates. Semantic snapshots omit
boxes by default; add `--boxes` only for coordinate or layout work.

Use snapshots for structure and interaction. Request a screenshot only for
Use `--verbose` only when compact output omits a needed fact. Forward uncommon
Playwright CLI options after `--`:

```bash
heimdal session observe -- --depth=4
heimdal session screenshot -- --full-page
```

## Diagnose from summaries first

For an interactive failure, use one diagnostic packet:

```bash
heimdal session diagnose --json
```

For a deterministic run, inspect the live or final report before opening raw
artifacts:

```bash
heimdal report --run latest --json
heimdal trace --run latest
```

Inspect raw artifacts only when the summary points to them. Never put secrets
in commands, screenshots, traces, metadata, or generated tests.

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

Commands are argument arrays, not shell strings. Templates include `${RUN_ID}`,
`${RUN_DIR}`, `${ROOT}`, `${BRANCH}`, `${PORT}`, and `${URL}`. Fixtures receive
the corresponding Heimdal run and artifact environment. Use named signals
instead of sleeps and publish only small, non-secret metadata:

```bash
heimdal signal send fixture.ready
heimdal signal wait fixture.ready --run latest --timeout 2m
heimdal metadata publish fixture.diagnostics --file ./metadata.json
heimdal metadata get fixture.diagnostics --run latest --json
```

## Invariants

- Read the target repository's `AGENTS.md` and owning QA docs first.
- Drive real controls and assert user-visible outcomes; do not manufacture
  state through private APIs, databases, hooks, or reducer calls.
- Prefer one focused flow; do not hide failures with retries or arbitrary
  waits.
- Preserve failure evidence and stop sessions when finished.
- Keep every run and session in its originating Git worktree.
