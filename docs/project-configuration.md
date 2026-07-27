# Project configuration

Run `heimdal init` to create `.heimdal.json`.

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
      {
        "name": "typecheck-runtime",
        "command": ["npm", "run", "typecheck", "--", "--version"],
        "timeout_ms": 10000
      }
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

Heimdal allocates a free port when a run or session needs one. Environment
templates such as `${PORT}` are expanded for the app command.

`provenance_env` records only whether each named variable was set; values are
never persisted.

Set `artifacts.retention.enabled` to `false` to disable automatic cleanup, or
`thin_repeated_failures` to `false` to retain every repeated trace. Manual
`heimdal gc` remains available.

Doctor checks execute argument arrays directly from the project root. They are
not shell strings.

When `session.command` and `session.url` are configured, `session start`
launches the app, waits for the URL, and lets `session stop` close both the
browser and app. Use `--no-server` to connect to an already running app.

