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
A terminal runner error takes precedence over caught assertion probes; inspect
`failure_source`, `classification`, and `caught_probe_count` before treating an
errored trace action as causal.
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

Discover lifecycle state before guessing at session names or inspecting files:

```bash
heimdal sessions list --json
heimdal sessions list --status stale --json
heimdal sessions prune --dry-run --json
```

The inventory probes the owning Playwright workspace. Treat `stale` as a dead
browser or owned app, `unknown` as an unavailable runtime probe, and `broken` as
an invalid global index. Pruning removes stale indexes but preserves evidence;
`gc` integrates the same cleanup.

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
fall back to a full snapshot. Pure reordering and moves between meaningful
regions count as changes. Do not immediately call `observe` again. Prefer
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

For design decisions, use `session measure --viewport 360x800 --json` before
writing ad hoc eval scripts or separately resizing and measuring. Omit
`--viewport` to measure the current viewport. The bounded packet reports
viewport/document geometry, overflow, clipping, touch-target warnings, controls
and early leaf content, plus semantic, grid/flex, and padded/scroll regions with
tracks or direction, padding, gap, and overflow. One packet per viewport should
usually support the layout decision. Use `session measure TARGET --json` only when a remaining
decision needs that target's rectangle and key computed styles; TARGET and
`--viewport` are intentionally separate. Then request a screenshot only for
visual qualities the measurement cannot represent.

For canvas or spatial controls, measure once and use element-relative pointer
coordinates instead of viewport arithmetic or raw mouse sequences:

```bash
heimdal session click --within e42 --at 62%,35%
heimdal session pointer drag --within e42 --from 20%,50% --to 80%,50%
```

These actions remain Playwright bounding-box operations and graduate into
viewport-resilient test code.

For asynchronous UI, issue one semantic wait instead of repeatedly observing
or sleeping. A role is the page's accessibility role (`button`, `link`,
`textbox`, or similar). Wait by role and accessible name, visible text, or any
semantic change; each successful wait returns the resulting delta:

```bash
heimdal session wait --role button --name "Continue" --state enabled --timeout 30s
heimdal session wait --text "The world answers"
heimdal session wait --change
heimdal session wait --change --settle 300ms
```

Change waits compare against the retained Playwright snapshot first, so they
also catch state that completed between agent commands. Use `--settle` for
model-backed or multi-stage UI when the result should remain semantically quiet
before continuing. All phases consume one timeout budget.

Record the user-visible outcome with `session expect` during exploration so it
graduates into a Playwright assertion:

```bash
heimdal session expect --role button --name "Continue" --state enabled
heimdal session expect --text "Saved" --state visible
heimdal session expect --url "http://127.0.0.1:4173/done"
heimdal session expect --target e12 --value "ready"
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
heimdal session timeline --failures --limit 20 --json
```

Timeline and report output is a bounded phase/failure/recent-change view by
default. Page long histories with `--from`, `--to`, `--limit`, `--category`, or
`--failures`; follow `next_from`. Use `--json=full` only when every retained
entry is necessary. A successful zero-error console check is not an issue.
Treat report `suggestions` as workflow coaching, not failures: replace repeated
snapshot/find polling with a semantic wait, checkpoint long phases, and batch
safe consecutive interactions when the retained timeline supports it.
Checkpoints label recoverable session state and appear in reports; they do not
make arbitrary repository test fixtures resumable.

Once a short verification flow is known, batch its actions, semantic assertions,
and bounded named JSON evidence. The payoff is one agent command/response round
for the whole flow, without losing per-step failure attribution:

```bash
heimdal session batch --json -- \
  click e8 --then \
  expect --role button --name "Use light theme" --state visible --then \
  evidence theme.after-click "() => ({ theme: document.documentElement.dataset.theme, stored: localStorage.getItem('theme') })" --then \
  reload --then \
  expect --role button --name "Use light theme" --state visible --then \
  evidence theme.after-reload "() => ({ theme: document.documentElement.dataset.theme, stored: localStorage.getItem('theme') })"
```

This six-step example verifies that a theme toggle changes the visible control
and that both the DOM state and persisted preference survive a reload. On the
atomic path it needs two Playwright invocations—one `run-code` plus one final
ref-refresh snapshot—instead of six separate command/response loops. Replace
`e8`, the accessible name, and the evidence expression with values from the
current app.

The batch stops at the first failed step and returns final fresh refs. Safe
batches with unambiguous retained refs run as one Playwright code block plus one
final ref-refresh snapshot while preserving per-step deltas, assertions,
failure attribution, and test-generation locators. Named evidence appears in
the response's `evidence` object and must return JSON. Check `execution` and
`playwright_invocations`; unsupported or ambiguous batches fall back to the
stepwise path. Keep `wait --change` outside an atomic batch so its retained-
snapshot race check remains active. Action JSON is compact by default; use
`--json=full` only when repeated session metadata is needed.

Use `--file browser-steps.json` for a longer or reusable batch. When no action
surrounds a measurement, capture it directly with `heimdal session evidence
NAME EXPRESSION --json`. Expressions run through Playwright page evaluation,
must return bounded JSON, and must not contain secrets.

For a bounded multi-user flow, use one group instead of independently managing
several app fixtures:

```bash
heimdal session group start --actors host,guest
heimdal session click --actor guest e12
heimdal session group timeline --json
heimdal session group stop
```

Actors have isolated Playwright browser state but share the first actor's app
process and URL. Ordinary session commands accept `--actor`; add `--group` only
when the actor name is ambiguous across active groups. Stop the group once so
non-owning browsers close before the shared app owner.

## Diagnose from summaries first

For an interactive failure, use one diagnostic packet:

```bash
heimdal session diagnose --json
heimdal session diagnose --screenshot --stop --json
```

The compact packet groups repeated console and request failures by signature
and returns a semantic delta when the page has not changed. Add `--screenshot`
only when visual evidence matters. Use `--stop` only for the final inspection
of a non-group session; it captures evidence before closing the browser and
owned app, saving separate screenshot and lifecycle commands. Stop multi-actor
sessions with `session group stop`.

For a deterministic run, inspect the live or final report before opening raw
artifacts:

```bash
heimdal report --run latest --json
```

The report includes bounded trace diagnosis when available. It correlates a
terminal test error with a matching trace error or the last relevant action and
classifies earlier continued-past assertions as caught probes. Use `heimdal
trace inspect --run latest --around-failure` only for a separate trace packet or
a direct trace path. Inspect raw artifacts only when these summaries point to
them. Never put secrets in commands, screenshots, traces, metadata, or generated
tests.

Use the indexed history before scanning `.heimdal` directly:

```bash
heimdal runs list --status failed --since 2d --json
heimdal runs show latest-failed --json
heimdal runs compare <old-run> <new-run> --json
heimdal runs pin <run-id>
```

Repeated failures are grouped by semantic fingerprint; comparison separately
reports exact-message equality. Inventory provenance includes selectors, Git
commit/dirty identity, and configured fixture-variable names plus set/unset
state, never their values. Use `latest-failed` with report or trace when
diagnosing the newest failure. Pin only evidence that must outlive normal
retention, then unpin it with `runs pin <run-id> --remove`.

Use `heimdal gc --dry-run` before manual artifact cleanup. Retention preserves
pins, active runs, recent non-duplicate runs, and the configured number of
distinct failure fingerprints within its byte budget. By default it compacts
older copies of a repeated semantic failure while retaining the newest full
evidence; exact within-run copies are hard-linked. Pruned runs remain as compact
indexed history, so use `runs list` rather than scanning or deleting `.heimdal`
paths.

Use `session save --test PATH --ready` to audit the generated TypeScript draft.
It fails readiness when assertions are missing or coordinate, stale-ref,
evaluation, run-code, or unsupported actions still need repair. The draft is
still written. A passing readiness audit means the recorded actions are
portable; run the repository-owned test before treating it as regression
evidence.

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
