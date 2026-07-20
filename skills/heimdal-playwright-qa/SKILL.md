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

Treat doctor status `issues` as a failed preflight. Configured project checks
represent required runtimes or build prerequisites and run without a shell.

Treat only `passed` as passing evidence. Heimdal reports `skipped` with a
nonzero exit when Playwright discovers tests but executes none. Run and report
JSON expose structured test counts, a primary failure fingerprint, deduplicated
warnings, artifact sizes, and a bounded Playwright failure-context excerpt; use
those fields before reading log tails. Failed reports also fold in the retained
trace's failing action, locator, nearby actions, and DOM excerpt when available.
A long JSON run emits progress on stderr, and `report --run ID --json` can be
polled for structured live progress.

Use one persistent session for exploration:

```bash
heimdal doctor
heimdal session start
heimdal session click e12
heimdal session fill e5 "hello"
heimdal session wait --role button --name "Continue" --state enabled --timeout 30s
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

`session start` returns a semantic accessibility snapshot. State-changing
actions return a semantic delta when it is smaller, with fresh refs for changed
controls. Reloads and same-page navigation return fresh refs for every current
control while omitting unchanged static content; materially different pages
fall back to a full snapshot. Do not immediately call `observe` again. Prefer
current refs and user-facing locators over CSS, XPath, or coordinates. Add
`--full` when the complete tree is needed and `--boxes` only for coordinate or
layout work.

Use snapshots for structure and interaction. Request a screenshot only for
visual appearance or layout. Use `--verbose` only when compact output omits a
needed fact. Forward uncommon Playwright CLI options after `--`:

```bash
heimdal session observe -- --depth=4
heimdal session screenshot -- --full-page
```

For asynchronous UI, issue one semantic wait instead of repeatedly observing
or sleeping. A role is the page's accessibility role (`button`, `link`,
`textbox`, or similar). Wait by role and accessible name, visible text, or any
semantic change; each successful wait returns the resulting delta:

```bash
heimdal session wait --role button --name "Continue" --state enabled --timeout 30s
heimdal session wait --text "The world answers"
heimdal session wait --change
```

On `wait`, `--name` is the accessible name; use `--session NAME` to select a
named browser. Canonical targeted forms include `press TARGET KEY`, `type TARGET
TEXT`, `fill TARGET TEXT --submit`, `click TARGET --force`, and `mouse click X
Y`. Follow Heimdal's structured correction when a command shape is invalid.

Checkpoint meaningful states in long explorations and use the synthesized
timeline before reading individual action logs:

```bash
heimdal session checkpoint "entered checkout"
heimdal session timeline --json
heimdal session report --json
```

Checkpoints label recoverable session state and appear in reports; they do not
make arbitrary repository test fixtures resumable.

Put known consecutive actions in one bounded batch to reduce agent round trips:

```json
{"version":1,"steps":[{"command":"fill","args":["e5","hello"]},{"command":"click","args":["e8"]}]}
```

```bash
heimdal session batch --file browser-steps.json --json
```

The batch stops at the first failed step and returns the final snapshot or
delta. Action JSON is compact by default; use `--json=full` only when repeated
session metadata is needed.

## Diagnose from summaries first

For an interactive failure, use one diagnostic packet:

```bash
heimdal session diagnose --json
```

For a deterministic run, inspect the live or final report before opening raw
artifacts:

```bash
heimdal report --run latest --json
```

The report includes bounded trace diagnosis when available. Use `heimdal trace
inspect --run latest --around-failure` only for a separate trace packet or a
direct trace path. Inspect raw artifacts only when these summaries point to
them. Never put secrets in commands, screenshots, traces, metadata, or
generated tests.

Use the indexed history before scanning `.heimdal` directly:

```bash
heimdal runs list --status failed --since 2d --json
heimdal runs show latest-failed --json
heimdal runs compare <old-run> <new-run> --json
heimdal runs pin <run-id>
```

Repeated failures are grouped by fingerprint. Use `latest-failed` with report
or trace when diagnosing the newest failure. Pin only evidence that must outlive
normal retention, then unpin it with `runs pin <run-id> --remove`.

Use `heimdal gc --dry-run` before manual artifact cleanup. Retention preserves
pins, active runs, recent runs, and the configured number of distinct failure
fingerprints within its byte budget. Pruned runs remain as compact indexed
history, so use `runs list` rather than scanning or deleting `.heimdal` paths.

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

For test-produced measurements or decision evidence, emit bounded named JSON
as `HEIMDAL_EVIDENCE <name> <json>` or attach `application/json` through
Playwright. Read the structured `evidence` object from the run/report instead
of scraping stdout. Never publish secrets as evidence.

## Invariants

- Read the target repository's `AGENTS.md` and owning QA docs first.
- Drive real controls and assert user-visible outcomes; do not manufacture
  state through private APIs, databases, hooks, or reducer calls.
- Prefer one focused flow; do not hide failures with retries or arbitrary
  waits.
- Preserve failure evidence and stop sessions when finished.
- Keep every run and session in its originating Git worktree.
