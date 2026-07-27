# Getting started

Heimdal delegates browser execution to Playwright. Start by checking what the
project already provides:

```bash
heimdal doctor --dir /path/to/project --json
```

When `doctor` reports that the Playwright agent CLI is missing:

```bash
heimdal install --dir /path/to/project agent-cli
```

## Run a Playwright test

```bash
heimdal run --dir /path/to/project -- tests/browser/example.spec.ts
heimdal report --run latest --json
```

Arguments after `--` are passed to Playwright. Each run gets an isolated
artifact directory and, when required, a free port. A run that discovers tests
but executes none returns `skipped` with a nonzero exit.

## Start an interactive session

```bash
heimdal session start \
  --dir /path/to/project \
  --name qa \
  --url http://127.0.0.1:3000

heimdal session observe --name qa
heimdal session click e12 --name qa
heimdal session diagnose --name qa --json
heimdal session stop --name qa
```

Sessions are headless by default. Add `--headed` when a person should watch or
take over the browser.

If `.heimdal.json` defines a session command and URL, `session start` launches
the app and waits for it automatically. See
[Project configuration](project-configuration.md).

