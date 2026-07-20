# Heimdal

Agent-oriented orchestration around Playwright.

Playwright is the browser automation runtime and test authority. Heimdal is a
thin, project-aware control plane around the official `playwright-cli`: it
manages fixture and server lifecycle, isolated sessions, compact agent-facing
output, and retained evidence. Browser automation, locators, assertions, and
reports remain in Playwright; Heimdal does not implement a browser protocol.

Playwright drives the browser well, but agents also need a consistent way to
start projects, keep sessions alive, isolate worktrees, and consume useful
results. Heimdal provides that missing workflow layer without replacing the
Playwright runtime.

## Install

Requires Go 1.26 or later.

```bash
go install github.com/coadan/heimdal/cmd/heimdal@latest
heimdal skill install
```

## Quick start

Run from any project supported by Playwright CLI, or pass its path with `--dir`:

```bash
heimdal doctor --json
heimdal install agent-cli
heimdal install chromium
heimdal session start --dir /path/to/worktree --name qa
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
