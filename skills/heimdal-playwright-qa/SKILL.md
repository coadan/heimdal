---
name: heimdal-playwright-qa
description: Use when an agent needs to explore, diagnose, or regression-test a web application from a Git worktree with Playwright as the only browser runtime.
---

# Heimdal Playwright QA

Run in the target worktree. Playwright is the only browser runtime; Heimdal
owns lifecycle and compact evidence.
Run `heimdal doctor` once per worktree/config state; repeat only after config or
dependency drift.

## Choose one path

Known behavior:

```bash
heimdal run -- tests/browser/<flow>.spec.ts --grep <behavior>
```

Only `passed` is evidence. Doctor `issues` and run `skipped` fail. Use shell
`yield_time_ms: 30000`, then empty
30-second waits on the same process. Never short-poll or restart.

Unknown or visual behavior:

```bash
heimdal session start --dir .
heimdal session diagnose --json
heimdal session stop
```

Use `--name` only for concurrent/cross-directory lookup; `--headed` only for a
human observer. Multiplayer: `session group start --actors host,guest`, target
with `--actor`, then stop the group.

## Minimize rounds

- Use snapshots/deltas returned by start and actions; do not immediately
  observe again. Add `--full` only for the whole tree.
- Prefer current refs and role/name/text over CSS, XPath, or coordinates.
- Wait once instead of observe/find loops or sleeps: `session wait --role
  button --name Continue --state enabled --timeout 30s`, `--text`, or
  `--change [--settle 300ms]`.
- Test SSE recovery with `session reconnect --request /events --json`; it
  cycles offline/online and requires a new matching request.
- Once known, batch actions with `session batch --json -- ... --then ...`.
  It stops at first failure and returns step attribution plus fresh refs. Keep
  `wait --change` outside atomic batches. Check `execution` and
  `playwright_invocations`.
- Use `session expect` for outcomes. Named `evidence NAME EXPRESSION` returns
  bounded, secret-free JSON.
- Use `session measure --viewport 360x800 --json` for geometry; screenshot only
  visual qualities it cannot measure. Use `--boxes` only for coordinates.
- For canvas, use `click` or `pointer move/drag --within REF`; use raw
  coordinates only without a stable semantic target.
- Use `--verbose` or forwarded options only when compact output lacks a fact.

Use `session checkpoint` for meaningful long-flow states and bounded
`session timeline --json`/`session report --json` for history.

## Diagnose

- Interactive: `heimdal session diagnose --json`; add `--screenshot` only for
  visual evidence and `--stop` only for final non-group inspection.
- Deterministic: `heimdal report --run latest --json`; inspect structured
  counts, `failure_source`, classification, fingerprint, probes, and bounded
  trace context before logs. If insufficient, use `trace inspect --run latest
  --around-failure`. Use `runs list/show/compare`, not artifact scans.
- `session save --test PATH --ready` audits a draft. Repair its findings and run
  the repository test before claiming regression evidence.

## Fixture contract

Use `session start --url URL` for an existing app; otherwise use repository
`.heimdal.json`. Coordinate with `heimdal signal send/wait`, never sleeps or
artifact files. Publish bounded, non-secret metadata through Heimdal.

## Invariants

- Honor target-repository instructions and read its owning QA docs.
- Drive real controls; assert visible and subscribed/canonical outcomes. Never
  manufacture state through private APIs, databases, hooks, or reducers.
- Keep one focused flow; never hide failure with retries or sleeps.
- Never put secrets in commands, screenshots, traces, metadata, evidence, or
  generated tests.
- Preserve evidence, stop sessions, and keep runs in their originating
  worktree.
