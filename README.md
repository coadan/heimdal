<p align="center">
  <img src="assets/heimdal-logo.png" alt="Pixel-art Heimdal guardian helmet" width="192">
</p>

# Heimdal

Heimdal gives coding agents one project-aware CLI for Playwright browser QA.
It runs repository-owned Playwright tests, opens persistent sessions through the
official `playwright-cli`, isolates concurrent worktrees, and keeps the evidence
needed to diagnose failures.

Playwright still owns the browser, locators, assertions, traces, and HTML
reports. Heimdal does not add another browser protocol or test framework.
It wraps the runtime to remove repeated agent work: project and session lookup,
app lifecycle, compact semantic evidence, and durable artifacts all use one
consistent command surface.

Worktree-isolated browser testing is a central reason Heimdal exists. Parallel
agents should be able to test different branches without sharing a dev-server
port, artifact directory, project configuration, or named-session state.
Heimdal resolves the active worktree first, then scopes each run and session to
that project while allocating a port when the project requests one.

For an interactive session, Heimdal can also start the project's configured app
command, wait for its URL to respond, retain its logs, and stop that process
with the session. This is what Heimdal means by managing the app process; normal
Playwright test fixtures remain owned by the project's Playwright configuration.

## Install

Heimdal requires Go 1.26 or later and a project that Playwright supports.

```bash
go install github.com/coadan/heimdal/cmd/heimdal@latest
heimdal --version
heimdal skill install
```

For interactive sessions, install the official Playwright agent CLI and its
browser. Install the repository-owned browser separately when deterministic
tests need it:

```bash
heimdal install agent-cli
heimdal install agent-browser chromium
heimdal install chromium
```

## Check a project

Run Heimdal from the target Git worktree. It discovers the project from the
current directory by default. Use `--dir PATH` to start discovery elsewhere.

```bash
heimdal doctor
heimdal doctor --dir /path/to/worktree --json
```

`--dir` is a discovery path, not necessarily the project root: Heimdal resolves
the containing Git project and its `.heimdal.json`. The old `--root` spelling is
accepted for compatibility but omitted from current examples.

Projects can declare required, non-mutating preflight commands under
`doctor.checks`. A failed or timed-out check makes doctor return `issues`
instead of claiming the project is ready.

## Run a deterministic test

Pass Playwright arguments after `--`:

```bash
heimdal run -- tests/browser/example.spec.ts --grep "opens the menu"
```

Run JSON includes parsed test counts, invocation selectors, deduplicated
warnings, artifact sizes, and a fingerprinted primary failure when available.
If Playwright discovers tests but executes none, Heimdal returns status
`skipped` with a nonzero exit instead of treating the run as passing.
`heimdal report --json` omits raw log tails and long file inventories by
default, but keeps a bounded failure-context excerpt when Playwright produced
one. For a failed run with a retained trace, it also folds in the failing
Playwright action and locator, nearby actions, and relevant DOM excerpt; use
`--json=full` when the retained raw detail is required inline.

Each run gets an isolated artifact directory under `.heimdal/` by default.
The final JSON result, stdout, stderr, Playwright output, report, screenshots,
videos, and traces remain there. While a test is still running, its live status
is available through the same report command. JSON runs also emit a compact
progress heartbeat to stderr while reserving stdout for the final JSON result:

```bash
heimdal report --run latest --json
heimdal trace --run latest --json
```

Inspect retained history without filesystem searches:

```bash
heimdal runs list --status failed --since 2d
heimdal runs show latest-failed --json
heimdal runs compare older-run newer-run --json
heimdal runs pin important-run
```

The inventory includes test selectors, status, timing, size, interrupted state,
and repeated failure fingerprints. `latest-failed` also works with `report` and
`trace`; pinned runs are protected from retention.

Trace JSON resolves the retained trace without opening a viewer and returns the
failing action, nearby actions and locators, bounded DOM snapshot excerpts,
run timing, and artifact indexes. Use `heimdal trace --run latest` without
`--json` when a person needs Playwright's interactive viewer; `heimdal trace
--help` documents both modes. Start with `report --json`; use `trace --json`
only when inspecting a direct trace path or requesting trace data separately.

Artifact retention is enabled by default: runs older than 14 days are eligible
for removal, retained run artifacts are bounded to 5 GiB, and the newest full
run for up to 20 distinct failure fingerprints remains protected. Pinned,
active, and unrecognized directories are never removed. Pruned runs keep small
history records, so `runs list` can still group repeated failures without
retaining every trace and video. Inspect any cleanup first:

```bash
heimdal gc --dry-run
heimdal gc --older-than 14d --keep-failures 20
heimdal gc --max-bytes 5GB --dry-run
```

Automatic cleanup runs at most once per day. `heimdal doctor --json` reports
artifact bytes, the configured budget, reclaimable bytes, and interrupted runs;
it warns when usage exceeds the budget.

Use `--run-id ID` when another process needs a stable run name. Run IDs contain
lowercase letters, numbers, and hyphens.

## Explore interactively

A named session keeps a browser available across commands:

```bash
heimdal session start --dir /path/to/worktree --name qa
heimdal session click e12 --name qa
heimdal session diagnose --name qa --json
heimdal session save --name qa --test tests/browser/exploration.spec.ts
heimdal session stop --name qa
```

Wait for user-visible state instead of polling snapshots or sleeping:

```bash
heimdal session wait --role button --name "Continue" --state enabled --timeout 30s
heimdal session wait --text "The world answers"
heimdal session wait --change
heimdal session wait --change --settle 300ms
```

`--role` is the accessibility role exposed by the page, such as `button`,
`link`, `textbox`, or `dialog`. Waits run through Playwright locators and return
the resulting semantic delta. A change wait first compares the live page with
Heimdal's retained Playwright snapshot, so a change that completed between
agent commands is returned immediately instead of timing out. Use `--settle`
for model-backed or multi-stage interfaces that should remain semantically
quiet before the agent continues; all wait phases share one timeout budget.
For a named browser session, pass `--session NAME` because `wait --name` means
the accessible name paired with `--role`.

Record user-visible outcomes as Playwright-backed assertions while exploring:

```bash
heimdal session expect --role button --name "Continue" --state enabled
heimdal session expect --text "Saved" --state visible
heimdal session expect --url "http://127.0.0.1:4173/done"
heimdal session expect --target e12 --value "ready"
```

`expect` uses accessibility roles, exact visible text, the current URL, or an
input value and records a portable assertion for `session save --test`.

Heimdal also keeps common interaction shapes stable across Playwright CLI
versions: targeted `press` and `type`, `fill --submit`, `click --force`, and
`mouse click X Y` are canonical forms. Invalid shapes return a bounded
correction instead of embedding the upstream help page.

Mark important states and inspect a long exploration without reconstructing
hundreds of action files:

```bash
heimdal session checkpoint "entered checkout"
heimdal session timeline --json
heimdal session report --json
```

The timeline groups navigation, interaction, waits, evidence, console checks,
failures, and checkpoints, and maps recorded actions to generated-test line
numbers. Checkpoints are durable labels, not promises that an arbitrary
Playwright test fixture can resume from that browser state.

For layout decisions, request one bounded measurement packet instead of
iterating through screenshots and ad hoc evaluation scripts:

```bash
heimdal session measure --json
heimdal session measure e12 --json
```

The page packet reports viewport and document geometry, content and control
counts, horizontal overflow, clipped-content samples, and controls below 44 px.
Targeted measurement adds the element rectangle and key computed styles. It is
read-only and runs through Playwright's evaluation command.

For canvas or spatial controls, keep coordinates relative to a measured
element instead of calculating viewport pixels:

```bash
heimdal session click --within e42 --at 62%,35%
heimdal session pointer drag --within e42 --from 20%,50% --to 80%,50%
```

Heimdal resolves the retained semantic ref once, then Playwright reads its
bounding box and performs the pointer action. Saved tests retain the same
bounding-box calculation, so the interaction survives viewport and layout
changes better than an absolute `mouse click X Y` sequence.

Sessions are headless by default, which suits unattended agents. Add
`--headed` to `session start` when you want a visible, inspectable browser:

```bash
heimdal session start --dir /path/to/worktree --name qa-visible --headed
```

Headed and headless sessions have the same persistence and evidence behavior.
Snapshots are semantic and omit coordinates by default. Add `--boxes` only
when layout or coordinate-based interaction requires bounding boxes. A
state-changing action returns only its semantic delta when that is smaller than
the full state, while retaining the complete snapshot as an artifact. Reloads
and other navigation actions also use a delta when the page remains
substantially the same, but always include fresh refs for its current controls;
a materially different page returns a full snapshot. Add `--full` when the
complete semantic tree is needed.

Known consecutive actions can share one agent round trip through a bounded JSON
batch:

```json
{
  "version": 1,
  "steps": [
    { "command": "fill", "args": ["e5", "hello"] },
    { "command": "click", "args": ["e8"] }
  ]
}
```

```bash
heimdal session batch --file browser-steps.json --name qa --json
```

Batch execution stops at the first failed step. Ordinary action JSON omits
repeated session metadata; use `--json=full` when that metadata is required.
When every step has an unambiguous retained semantic locator and a stable
action shape, Heimdal compiles the batch into one Playwright `run-code`
invocation, captures a bounded semantic delta after each logical step, and uses
one final Playwright snapshot to return fresh refs. The response reports
`execution: "atomic"` and `playwright_invocations: 2`. Arbitrary commands,
ambiguous refs, expanded/boxed evidence, and change waits use the original
stepwise path for correctness and report `execution: "sequential"`.

When graduating an exploration, ask Heimdal to reject an incomplete draft:

```bash
heimdal session save --test tests/browser/exploration.spec.ts --ready
```

The graduation report counts recorded assertions and portable actions, and
flags absolute coordinates, refs without retained locators, raw evaluation or
code, unsupported actions, and missing outcome assertions. The test draft is
still written when the readiness check fails so the reported issues can be
fixed directly.

The directory supplied to `session start` is recorded, so later commands can
find a uniquely named session even when run from another directory. Pass
`--dir PATH` again if the same session name exists in more than one worktree.

If `.heimdal.json` defines `session.command`, `session start` launches that app
process before opening the browser and waits for `session.url` to respond.
`session stop` closes the Playwright session and that app process. Use
`--no-server` to connect to an app that is already running.

## Configure a project

`heimdal init` creates `.heimdal.json`. A project that starts its app for
interactive QA can use this shape:

```json
{
  "version": 1,
  "playwright": {
    "config": "playwright.config.ts",
    "run_id_env": "HEIMDAL_RUN_ID",
    "port_env": "PORT",
    "provenance_env": ["BROWSER_FIXTURE_ENABLED"]
  },
  "session": {
    "command": ["npm", "run", "dev", "--", "--port", "${PORT}"],
    "url": "http://127.0.0.1:${PORT}",
    "port_env": "PORT",
    "server_timeout_ms": 45000
  },
  "doctor": {
    "checks": [
      {"name": "typecheck-runtime", "command": ["npm", "run", "typecheck", "--", "--version"], "timeout_ms": 10000}
    ]
  },
  "artifacts": {
    "directory": ".heimdal",
    "retention": {
      "enabled": true,
      "max_age_days": 14,
      "keep_failures": 20,
      "max_bytes": 5368709120
    }
  }
}
```

Heimdal allocates a free port when a run or session needs one. `${PORT}` and
other configured environment templates are expanded for the app command.
`provenance_env` records only each listed variable's name and set/unset state
in run evidence; values are never persisted. Set `artifacts.retention.enabled`
to `false` to disable automatic cleanup; manual `heimdal gc` remains available.
Doctor checks execute argument arrays directly from the project root; they are
never shell strings.

Tests can publish bounded named JSON evidence without log parsing by emitting
one line per value:

```text
HEIMDAL_EVIDENCE design.metrics {"iterations":2,"latency_ms":42}
```

`run` and `report` expose these values under `evidence`. Heimdal also recognizes
named `application/json` Playwright attachments when the reporter lists their
artifact path. Names use letters, numbers, dots, dashes, or underscores;
payloads are limited to 64 KiB.

## Coordinate with a running test

The test process and its fixtures receive:

```text
HEIMDAL_RUN_ID
HEIMDAL_RUN_DIR
HEIMDAL_RUN_METADATA_DIR
HEIMDAL_RUN_SIGNALS_DIR
```

Metadata lets a fixture publish a small, non-secret JSON fact, such as the URL
or database identity needed by a diagnostic command. Payloads are limited to
64 KiB and come from a file or stdin, never a command-line value:

```bash
heimdal metadata publish app.diagnostics --file ./target.json
printf '%s\n' '{"url":"http://127.0.0.1:4173"}' |
  heimdal metadata publish app.diagnostics --file -
heimdal metadata get app.diagnostics --run latest --json
```

Each producer should own one namespace. Publishing creates an immutable
version; reading a namespace returns its latest version. `heimdal report`
includes the latest metadata for all namespaces.

Signals represent named milestones and are safe to send more than once. They
replace polling conventions and unbounded sleeps:

```bash
heimdal signal send fixture.ready
heimdal signal wait fixture.ready --run latest --timeout 2m
```

Without `--run`, coordination commands use `HEIMDAL_RUN_DIR` when invoked by a
running fixture. From another shell, select a run with `--run ID` or
`--run latest`, plus `--dir PATH` when needed.

## Command help

Run `heimdal help` for the complete command summary and
`heimdal session --help` for interactive-session options.

## Agent benchmark

On 2026-07-20, two fresh coding agents started from the same React commit with
dependencies preinstalled and the same model and reasoning settings. Each
implemented, tested, built, and browser-verified a persistent theme toggle. One
used the official `playwright-cli` directly; the other used Heimdal. Both passed
the task with independent diffs, including a real click and reload in one named
browser session.

| Measure | Playwright CLI | Heimdal |
| --- | ---: | ---: |
| Core browser invocations | 10 | 7 |
| Core browser output | 5.3 KB | 4.2 KB |
| All shell commands | 23 | 21 |
| All command output | 62.1 KB | 52.4 KB |
| Wall time | 345.2 s | 302.2 s |
| Model input tokens | 980,403 | 722,528 |
| Model output tokens | 11,461 | 11,719 |

Heimdal used 30% fewer core browser invocations and 19% less core browser
output by returning semantic state with startup and state-changing actions. The
full run used 26% fewer input tokens, emitted 16% less command output, and
finished 12% sooner; model output tokens were 2% higher. Core browser figures
exclude tool help and optional screenshot or diagnosis work, while the
whole-task command, output, time, and token totals include those detours. This
is one controlled pair rather than a general performance claim: agent choices
vary and token totals include cached context.

The current implementation captures Playwright's generated locator from native
actions. For targeted actions that require `run-code`, it reuses a unique
role-and-name locator from the retained Playwright snapshot and falls back to
Playwright's locator generator when the snapshot is ambiguous. This avoids a
browser call without guessing. A deterministic local microbenchmark on the same
date measured cached session discovery at about 32 µs versus 12 ms for full
project rediscovery. These timings cover Heimdal overhead only; browser startup
and page behavior remain application-dependent.

## Development

```bash
go test ./...
go vet ./...
go build ./cmd/heimdal
```

Heimdal is released under the [MIT License](LICENSE).
