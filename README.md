# Heimdal

![Heimdal logo](assets/heimdal-logo.png)

Heimdal is a worktree-aware browser QA harness for coding agents. It keeps
Playwright as the browser and test authority while providing stable sessions,
compact semantic observations, reproducible evidence, and bounded artifacts.

## Install

```bash
go install github.com/coadan/heimdal/cmd/heimdal@latest
heimdal doctor --dir /path/to/project --json
```

If the project does not already provide the Playwright agent CLI:

```bash
heimdal install --dir /path/to/project agent-cli
```

## Quick start

Run an existing Playwright test:

```bash
heimdal run --dir /path/to/project -- tests/browser/example.spec.ts
heimdal report --run latest --json
```

Explore interactively:

```bash
heimdal session start --dir /path/to/project --name qa --url http://127.0.0.1:3000
heimdal session observe --name qa
heimdal session click e12 --name qa
heimdal session diagnose --name qa --json
heimdal session stop --name qa
```

Refs such as `e12` come from the latest semantic observation. Re-observe after
navigation or meaningful DOM changes.

## Why Heimdal

- isolated artifacts and free ports per run;
- persistent named browser sessions across agent commands;
- compact semantic deltas instead of repeated full page dumps;
- traces, screenshots, videos, logs, and failure fingerprints;
- explicit waits, assertions, reconnect testing, and multi-actor sessions;
- deterministic graduation from exploration to a Playwright test draft.

## Documentation

- [Documentation index](docs/README.md)
- [Getting started](docs/getting-started.md)
- [Interactive sessions](docs/interactive-sessions.md)
- [Runs, reports, and artifacts](docs/runs-and-artifacts.md)
- [Project configuration](docs/project-configuration.md)
- [Fixture coordination](docs/fixture-coordination.md)
- [Agent efficiency measurements](docs/benchmarks.md)

Run `heimdal help` or `heimdal session --help` for the complete command
reference.

## Development

```bash
go test ./...
go vet ./...
go build ./cmd/heimdal
```

Heimdal is released under the [MIT License](LICENSE).
