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

Run JSON includes parsed test counts, invocation provenance, deduplicated
warnings, artifact sizes, and a fingerprinted primary failure when available.
A semantic fingerprint groups the same test/helper/failure class despite added
diagnostic detail; an exact fingerprint distinguishes the full messages.
If Playwright discovers tests but executes none, Heimdal returns status
`skipped` with a nonzero exit instead of treating the run as passing.
`heimdal report --json` omits raw log tails and long file inventories by
default, but keeps a bounded failure-context excerpt when Playwright produced
one. For a failed run with a retained trace, it also folds in the failing
Playwright action and locator, nearby actions, and relevant DOM excerpt; use
`--json=full` when the retained raw detail is required inline.

Failure attribution prefers the terminal runner error recorded in the trace.
If a helper throws after a successful Playwright action, that action is labeled
`terminal_context`; earlier failed assertions that execution continued past are
counted separately as `caught_probe` evidence instead of replacing the real
failure.

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

The inventory includes test selectors, safe fixture-flag state, Git
commit/dirty identity, status, timing, size, interrupted state, and repeated
failure fingerprints. `latest-failed` also works with `report` and `trace`;
pinned runs are protected from retention.

Trace JSON resolves the retained trace without opening a viewer and returns the
failing action, nearby actions and locators, bounded DOM snapshot excerpts,
run timing, and artifact indexes. Use `heimdal trace --run latest` without
`--json` when a person needs Playwright's interactive viewer; `heimdal trace
--help` documents both modes. Start with `report --json`; use `trace --json`
only when inspecting a direct trace path or requesting trace data separately.

Artifact retention is enabled by default: runs older than 14 days are eligible
for removal, retained run artifacts are bounded to 5 GiB, and the newest full
run for up to 20 distinct failure fingerprints remains protected. Older copies
of the same semantic failure are compacted, and exact trace/video/screenshot
copies within a run are hard-linked without changing their paths. Pinned,
active, and unrecognized directories are never removed. Pruned runs keep small
history records, so `runs list` can still group repeated failures. Inspect any
cleanup first:

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

`session observe` returns semantic changes since the preceding session command;
use `--full` only when the entire current tree is needed.

`session diagnose` groups recurring console and request failures into bounded
signatures and returns a delta when the semantic page state is unchanged. On a
final inspection, add `--stop` to collect the packet and close a non-group
browser and its owned app in one command.

Discover sessions without guessing names or reading state files:

```bash
heimdal sessions list --json
heimdal sessions list --status stale --json
heimdal sessions prune --dry-run --json
```

The inventory probes each indexed worktree's Playwright workspace and reports
`active`, `stopped`, `stale`, `unknown`, or `broken`. Pruning finalizes stale
state and removes dead global indexes while preserving session evidence;
`heimdal gc` performs the same stale-index cleanup. `session list` and `session
prune` are accepted as singular aliases. Lists return the newest 8 matches by
default; filter or raise `--limit` for older rows. Starting a known-stale session
name recovers it without requiring `--force`.

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

Exercise browser reconnection behavior without restarting the app or reaching
into its private state:

```bash
heimdal session reconnect --request /events --json
heimdal session batch --json -- \
  reconnect --request /events --then \
  wait --text "Updated" --timeout 30s
```

`session reconnect` takes the Playwright browser context offline for 500ms,
restores it in the same command, and optionally waits for a new request whose
URL contains `--request`. It works for EventSource and fetch-stream SSE
clients. Override the disruption with `--offline-for`; matching requests use a
30-second default `--timeout`. A reconnect plus its visible outcome can use the
atomic batch path, reducing the interaction to one Playwright code invocation
and one final semantic snapshot.

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
`mouse click X Y` or `mouse move X Y` are canonical forms. Invalid shapes
return a bounded correction instead of embedding the upstream help page.

Mark important states and inspect a long exploration without reconstructing
hundreds of action files:

```bash
heimdal session checkpoint "entered checkout"
heimdal session timeline --json
heimdal session report --json
heimdal session timeline --failures --limit 20 --json
heimdal session timeline --category evidence --from 200 --limit 50 --json
```

The default timeline is a bounded phase, failure, and recent-change summary;
`next_from` continues an explicitly filtered page. Filters include
`--failures`, `--category`, `--from`, `--to`, and `--limit`. Use
`--json=full` only when every retained entry and evidence summary is required.
Reports keep phases, failures, and recent meaningful changes bounded, and do
not treat a successful `console error` check with zero errors as an issue.
They also suggest `wait --change`, checkpoints, or batching when the timeline
shows avoidable polling or round trips. Suggestions do not change session
status, and Heimdal never rewrites actions automatically.
Checkpoints are durable labels, not promises that an arbitrary Playwright test
fixture can resume from that browser state.

For layout decisions, request one bounded measurement packet instead of
iterating through screenshots and ad hoc evaluation scripts:

```bash
heimdal session measure --json
heimdal session measure --viewport 360x800 --json
heimdal session measure e12 --json
```

The page packet reports viewport and document geometry, overflow and clipping,
touch-target warnings, bounded controls and early leaf content, plus semantic,
grid/flex, and padded/scroll regions with tracks or direction, padding, gap,
and overflow. `--viewport WIDTHxHEIGHT` resizes and measures in one Playwright
call; one packet per viewport is usually enough for a layout decision. Targeted
measurement adds one element's rectangle and key computed styles. It is
read-only and runs through Playwright's evaluation command.

For canvas or spatial controls, keep coordinates relative to a measured
element instead of calculating viewport pixels:

```bash
heimdal session click --within e42 --at 62%,35%
heimdal session pointer move --within e42 --at 62%,35%
heimdal session pointer drag --within e42 --from 20%,50% --to 80%,50%
```

Heimdal resolves the retained semantic ref once, then Playwright reads its
bounding box and performs the pointer action. Saved tests retain the same
bounding-box calculation, so the interaction survives viewport and layout
changes better than an absolute coordinate sequence. Use `pointer move` for a
hover target; reserve `mouse move X Y` for canvas regions without a stable
element target.

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
a materially different page returns a full snapshot. Reordering unique content
or moving it between meaningful regions also counts as a change. Add `--full`
when the complete semantic tree is needed.

Once a verification flow is known, actions, semantic assertions, and bounded
named JSON evidence can share one agent round trip:

```json
{
  "version": 1,
  "steps": [
    { "command": "click", "args": ["e8"] },
    { "command": "expect", "args": ["--role", "button", "--name", "Saved"] },
    { "command": "evidence", "args": ["save.state", "() => ({ saved: true })"] }
  ]
}
```

```bash
heimdal session batch --file browser-steps.json --name qa --json
```

For a short flow, skip the temporary file:

```bash
heimdal session batch --session qa --json -- \
  click e8 --then \
  expect --role button --name "Saved" --then \
  evidence save.state "() => ({ saved: true })"
```

Batch execution stops at the first failed step. Ordinary action JSON omits
repeated session metadata; use `--json=full` when that metadata is required.
When every step has an unambiguous retained semantic locator and a stable
shape, Heimdal compiles the batch into one Playwright `run-code` invocation and
uses one final snapshot to return fresh refs. State-changing steps retain
bounded semantic deltas; passing assertions and named evidence avoid redundant
snapshots. The response reports `execution: "atomic"`,
`playwright_invocations: 2`, and an `evidence` object. Arbitrary commands,
ambiguous refs, expanded/boxed output, and change waits use the stepwise path
for correctness.

Capture one bounded measurement without a batch when no action surrounds it:

```bash
heimdal session evidence save.state "() => ({ saved: true })" --json
```

For final visual evidence and cleanup, combine the screenshot with diagnosis:

```bash
heimdal session diagnose --screenshot --stop --json
```

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

For bounded multi-user flows, start isolated Playwright actors against one
shared app fixture:

```bash
heimdal session group start --actors host,guest
heimdal session click --actor guest e12
heimdal session group timeline --json
heimdal session group stop
```

The first actor owns the configured app process; the other actors reuse its URL
without starting duplicate servers. Each actor keeps independent browser
state, while group timeline and report commands merge their evidence in time
order. Use `--group NAME` when the same actor name appears in multiple active
groups.

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
      "max_bytes": 5368709120,
      "thin_repeated_failures": true
    }
  }
}
```

Heimdal allocates a free port when a run or session needs one. `${PORT}` and
other configured environment templates are expanded for the app command.
`provenance_env` records only each listed variable's name and set/unset state
in run evidence; values are never persisted. Set `artifacts.retention.enabled`
to `false` to disable automatic cleanup; set `thin_repeated_failures` to `false`
to keep every recent repeated trace. Manual `heimdal gc` remains available.
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

In paired runs on 2026-07-21, agents completed the same tasks from the same
small React workspace. They used identical model settings and preinstalled
dependencies. All four results passed independent tests, builds, diff review,
and browser verification.

### What the agents showed

- **Coding:** The Heimdal agent used the skill's six-step persistence batch. It
  needed 3 rather than 6 browser rounds, issued 4 rather than 12 top-level
  browser commands, used 27% fewer input and 11% fewer output tokens, and
  finished 18% sooner.
- **Responsive design:** Both agents reached the result in one CSS iteration.
  Heimdal used 9 rather than 20 browser rounds, 29% fewer input and 27% fewer
  output tokens, and 20% less wall time.

These are single agent pairs, not a general performance claim. Token totals
include cached context, and agent choices vary. The narrower finding is that
combined semantic and layout evidence can reduce browser back-and-forth when a
task depends on visual decisions. It does not guarantee lower whole-task cost.

<details>
<summary>Raw agent results and definitions</summary>

| Task | Tool | Rounds | Commands | Time | Input tokens | Output tokens |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| Theme feature | Playwright CLI | 6 | 12 | 275.1 s | 826,995 | 10,536 |
| Theme feature | Heimdal | 3 | 4 | 224.4 s | 600,828 | 9,418 |
| Responsive design | Playwright CLI | 20 | 20 | 275.8 s | 1,243,463 | 9,022 |
| Responsive design | Heimdal | 9 | 9 | 220.6 s | 878,756 | 6,630 |

A browser round is one agent shell turn containing browser work. Browser
commands are CLI commands issued by the agent; a composite Heimdal command can
perform several upstream Playwright operations. The coding batch reported
`execution: "atomic"` and two upstream Playwright invocations while preserving
per-step assertions, evidence, and failure attribution. Help-only commands are
excluded.

</details>

### Focused measurements

- `measure --viewport` averaged 0.71 seconds, compared with 1.11 seconds for
  separate resize and measure commands: 36% less time across six local runs.
- Cached session discovery took about 32 µs, compared with 12 ms for full
  project rediscovery in a deterministic local microbenchmark.

Focused measurements cover Heimdal overhead only. Browser startup and page
behavior remain application-dependent.

## Development

```bash
go test ./...
go vet ./...
go build ./cmd/heimdal
```

Heimdal is released under the [MIT License](LICENSE).
