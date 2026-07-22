---
name: heimdal-playwright-qa
description: Use when an agent needs to explore, diagnose, or regression-test a web application from a Git worktree with Playwright as the only browser runtime.
---

# Heimdal Playwright QA

Run Heimdal from the target worktree. Playwright is the only browser runtime;
Heimdal owns app/session lifecycle and compact evidence.

## Choose one flow

**Known behavior:** run the focused repository test.

```bash
heimdal doctor
heimdal run -- tests/browser/<flow>.spec.ts --grep <behavior>
```

`doctor` status `issues` fails preflight. Only run status `passed` is passing
evidence; `skipped` is nonzero when Playwright discovers but executes no tests.

**Unknown behavior or visual exploration:** use one persistent session.

```bash
heimdal doctor
heimdal session start --dir .
heimdal session diagnose --json
heimdal session stop
```

Use `--name` only for concurrent/cross-directory lookup and `--headed` only
when a person must watch. Use a session group for bounded multi-user flows:
`session group start --actors host,guest`; target commands with `--actor`; stop
the group once.

## Interact without polling

- Start/actions return snapshots or semantic deltas with fresh refs. Use them;
  do not immediately `observe` again. Observe also returns a delta by default;
  add `--full` only when the entire current tree is needed.
- Prefer current refs and role/name/text locators over CSS, XPath, or viewport
  coordinates. Use `--boxes` only for layout/coordinate work.
- Use `session wait` instead of repeated observe/find calls or sleeps:
  `wait --role button --name Continue --state enabled --timeout 30s`,
  `wait --text <text>`, or `wait --change [--settle 300ms]`.
- Record outcomes with `session expect`; these graduate into Playwright
  assertions.
- Use `session measure --viewport 360x800 --json` for layout geometry,
  overflow, clipping, touch targets, and grid/flex structure. Request
  screenshots only for visual qualities measurement cannot represent.
- For canvas/spatial controls, use element-relative `click`, `pointer move`, or
  `pointer drag` with `--within REF`; reserve raw mouse coordinates for regions
  without a stable semantic target.
- Use `--verbose` or forward options after `--` only when compact output omits
  a needed fact.

## Collapse known interaction rounds

Once refs and assertions are known, use `session batch --json --` with actions
joined by `--then`. A batch stops on the first failure, preserves step-level
attribution, and returns fresh refs. Named `evidence NAME EXPRESSION` must
return bounded JSON without secrets. Check `execution` and
`playwright_invocations`; unsupported batches fall back stepwise. Keep
`wait --change` outside atomic batches so retained-snapshot race detection
remains active.

Use `session checkpoint` for meaningful long-flow states and
`session timeline --json` or `session report --json` for bounded history.
Page with filters/limits; use full JSON only when every entry is required.

## Diagnose summaries first

- Interactive: `heimdal session diagnose --json`; add `--screenshot` only for
  visual evidence and `--stop` only for final non-group inspection.
- Deterministic: `heimdal report --run latest --json`. Trust structured test
  counts, `failure_source`, classification, fingerprint, caught probes, and
  bounded trace context before logs or raw artifacts.
- Use `trace inspect --run latest --around-failure` only when the report is
  insufficient. Use indexed `runs list/show/compare`, not artifact-directory
  scans. Run `heimdal gc --dry-run` before manual cleanup.
- `session save --test PATH --ready` writes and audits a draft. Repair missing
  assertions, coordinates, stale refs, eval/run-code, or unsupported actions,
  then run the repository test before calling it regression evidence.

## Fixture contract

Use `session start --url URL` for an existing app. Otherwise let the
repository's `.heimdal.json` define argument-array commands and templated
run/worktree paths. Coordinate fixtures with `heimdal signal send/wait`, never
sleeps or artifact files. Publish only bounded non-secret metadata/evidence;
read it through Heimdal rather than scraping stdout.

## Invariants

- Honor the active target-repository instructions and read its owning QA docs.
- Drive real controls and assert user-visible plus subscribed/canonical
  outcomes; never manufacture state through private APIs, databases, hooks, or
  reducers.
- Keep one focused flow; do not hide failures with retries or sleeps.
- Never put secrets in commands, screenshots, traces, metadata, evidence, or
  generated tests.
- Preserve failure evidence, stop sessions, and keep every run/session in its
  originating worktree.
