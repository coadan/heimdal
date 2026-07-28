# Fixture coordination

Heimdal gives tests and fixtures stable directories for metadata and signals:

```text
HEIMDAL_RUN_ID
HEIMDAL_RUN_DIR
HEIMDAL_RUN_METADATA_DIR
HEIMDAL_RUN_SIGNALS_DIR
```

## Publish metadata

Metadata is bounded, non-secret JSON such as a local diagnostic URL or database
identity:

```bash
heimdal metadata publish app.diagnostics --file ./target.json
printf '%s\n' '{"url":"http://127.0.0.1:4173"}' |
  heimdal metadata publish app.diagnostics --file -
heimdal metadata get app.diagnostics --run latest --json
```

Each producer should own one namespace. Publishing creates an immutable
version; reading returns the latest version. Payloads are limited to 64 KiB
and come from a file or stdin, never a command-line value.

## Coordinate milestones

Signals replace polling and unbounded sleeps:

```bash
heimdal signal send fixture.ready
heimdal signal wait fixture.ready --run latest --timeout 2m
```

Signals are idempotent. Inside a running fixture, coordination commands can use
`HEIMDAL_RUN_DIR`, including an interactive session directory whose generated
run id contains a fractional timestamp. Another shell selects a deterministic
run using `--run` and, when needed, `--dir`.

## Publish test evidence

A test can emit bounded named JSON evidence:

```text
HEIMDAL_EVIDENCE design.metrics {"iterations":2,"latency_ms":42}
```

`run` and `report` expose these values under `evidence`. Heimdal also recognizes
named `application/json` Playwright attachments reported with their artifact
path. Names use letters, numbers, dots, dashes, or underscores; payloads are
limited to 64 KiB.
