# Heimdal

Heimdal is a small Go binary that gives coding agents a stable, low-ceremony
entry point for browser QA in Git worktrees. It discovers the project and its
Playwright installation, assigns an isolated run identity, forwards tests to
Playwright, captures evidence, and emits a compact result file.

Heimdal does not implement browser automation. Playwright remains the only
browser runtime and the repository remains the owner of tests, fixtures,
locators, assertions, and web-server configuration.

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
```

Go installs binaries into `GOBIN`, or `GOPATH/bin` when `GOBIN` is unset.

## Use from a worktree

```bash
heimdal doctor --json
heimdal install chromium
heimdal run -- tests/browser/combat.spec.ts --grep "victory"
heimdal report --run latest --json
heimdal trace --run latest
```

The default artifact root is `.dev/heimdal/<run-id>/`. Each run contains the
command result, stdout/stderr logs, Playwright test output, and any files that
Playwright produced. Set `--artifacts` to use a different ignored directory.

The CLI discovers npm, pnpm, Yarn, and Bun projects from their lockfiles. A
`.heimdal.json` file is optional; use `heimdal init` to create one and add
project-specific run-id/port environment names when a Playwright config needs
them.

## Design boundary

Playwright already provides isolated browser contexts, auto-waiting assertions,
local `webServer` startup, traces, and reporters. Heimdal only standardizes the
agent-facing shell around those capabilities:

- worktree and package-manager discovery;
- unique run IDs and optional loopback ports;
- streamed human output plus `result.json` for agents;
- retained logs, reports, screenshots, videos, and traces;
- a bundled skill installed with `heimdal skill install`.

The Playwright runner remains the authority for pass/fail. A Heimdal result is
not a replacement for a user-facing assertion.

## Development

```bash
gofmt -w .
go test ./...
go vet ./...
go build ./cmd/heimdal
```
