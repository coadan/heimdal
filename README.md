# Heimdal

Agent-oriented orchestration around Playwright CLI.

Heimdal wraps the official `playwright-cli` and Playwright Test with
project-aware fixture lifecycle, isolated browser sessions, compact
observations, and retained test artifacts. It does not implement a browser
protocol.

## Install

Requires Go 1.26 or later.

```bash
go install github.com/coadan/heimdal/cmd/heimdal@latest
heimdal skill install
```

## Quick start

Run from any project supported by Playwright CLI, or pass its path with `--root`:

```bash
heimdal doctor --json
heimdal install agent-cli
heimdal install chromium
heimdal session start --root /path/to/worktree --name qa
heimdal session observe
heimdal session click e12
heimdal session stop --name qa
```

For deterministic tests:

```bash
heimdal run -- tests/browser/example.spec.ts
```

Fixture-backed projects can define their command, URL, and environment in a
`.heimdal.json` file. Run `heimdal help` for the full command reference.

## Development

```bash
go test ./...
go vet ./...
go build ./cmd/heimdal
```

Heimdal is released under the [MIT License](LICENSE).
