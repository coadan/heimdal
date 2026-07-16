# Heimdal

Heimdal is a small Go binary that gives coding agents a stable, low-ceremony
entry point for interactive and deterministic browser QA in Git worktrees. It
starts project fixtures, creates isolated Playwright agent sessions, returns
compact observations with element refs and bounding boxes, captures evidence,
and can turn an exploratory session into a Playwright test draft.

Heimdal does not implement a browser protocol. The official Playwright CLI
remains the only interactive browser runtime, and Playwright Test remains the
authority for tests, fixtures, locators, assertions, and reports.

## Install

With a tagged or reachable Go module:

```bash
go install github.com/coadan/heimdal/cmd/heimdal@latest
heimdal skill install
```

During local development:

```bash
go install ./cmd/heimdal
heimdal skill install --force
heimdal install agent-cli
```

Go installs binaries into `GOBIN`, or `GOPATH/bin` when `GOBIN` is unset.

## Use from a worktree

```bash
heimdal doctor --json
heimdal install agent-cli
heimdal install agent-browser chromium
heimdal install chromium
heimdal session start --root /path/to/worktree --name combat --headed
heimdal session observe
heimdal session click e12
heimdal session diagnose --json
heimdal session save --test tests/browser/combat-exploration.spec.ts
heimdal session stop --name combat
heimdal run -- tests/browser/combat.spec.ts --grep "victory"
heimdal report --run latest --json
heimdal trace --run latest
```

`heimdal session start` keeps a named Playwright browser alive between CLI
calls. `--root` selects the worktree and is persisted for that named session,
so later commands can be run from another directory; pass `--root` again when
the same name exists in multiple worktrees. `observe` captures an accessibility
snapshot with refs and bounding boxes; state-changing actions automatically
return a fresh snapshot with boxes. Read-only commands retain their useful
result, while the default response removes Playwright protocol chatter and
limits snapshots to depth 5. Use `--verbose` on any session command to show
the complete Playwright CLI output, or forward options such as
`-- --depth=10` when a larger snapshot is needed.

All command stdout/stderr, snapshots, and screenshots remain in the session
artifact directory even when the terminal response is compact. Re-observe
after navigation or DOM mutation because refs are scoped to the snapshot that
created them. `diagnose` reports console errors, failed network requests, and
an exited fixture process as issues and returns a non-zero status.

Session artifacts live under `.dev/heimdal/sessions/<name>/<run-id>/` and
include the generated Playwright CLI config, action logs, snapshots, console
and network output, screenshots, server logs, and optional test draft. Test
artifacts continue to use `.dev/heimdal/<run-id>/`. Set `--artifacts` to use a
different ignored directory.

The CLI discovers npm, pnpm, Yarn, and Bun projects from their lockfiles. A
`.heimdal.json` file is optional; use `heimdal init` to create one and add
project-specific run-id/port environment names when a Playwright config needs
them.

## Project contract

Projects work without session configuration when an existing app URL is
available:

```bash
heimdal session start --url http://127.0.0.1:3000
```

For a fixture-backed project, add a `session` object to `.heimdal.json`:

```json
{
  "version": 1,
  "session": {
    "command": ["npm", "run", "fixture:dev"],
    "url": "http://127.0.0.1:${PORT}",
    "run_id_env": "APP_QA_RUN_ID",
    "port_env": "APP_QA_PORT",
    "env": {
      "APP_QA_DB": "${RUN_ID}"
    },
    "browser": "chromium",
    "server_timeout_ms": 45000
  }
}
```

Commands are argument arrays, not shell strings. `${RUN_ID}`, `${RUN_DIR}`,
`${OUTPUT_DIR}`, `${REPORT_DIR}`, `${ROOT}`, `${BRANCH}`, `${PORT}`,
`${SESSION}`, and `${URL}` are available in session URLs and environment
values. Set `session.runner` to an argument array if the local
`playwright-cli` executable cannot be discovered. Keep project-specific
fixture switches in the contract; keep browser actions in Playwright.

## Design boundary

Playwright CLI provides the persistent daemon, named sessions, refs, snapshots,
screenshots, storage, network inspection, tracing, and human takeover. Heimdal
standardizes the project-aware layer around those capabilities:

- worktree and package-manager discovery;
- fixture/server lifecycle and unique loopback ports;
- compact observe/action output plus JSONL session transcripts;
- retained logs, reports, screenshots, videos, traces, and server evidence;
- semantic locator capture when saving an exploratory session;
- a bundled skill installed with `heimdal skill install`.

Use `heimdal session` for observe → act → observe exploration, then use
`heimdal session save` to create a test draft and add explicit assertions.
Use `heimdal run` for deterministic regression suites. A Heimdal session result
is evidence, not a replacement for a user-facing assertion.

## Development

```bash
gofmt -w .
go test ./...
go vet ./...
go build ./cmd/heimdal
```
