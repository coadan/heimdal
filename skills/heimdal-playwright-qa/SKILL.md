---
name: heimdal-playwright-qa
description: Use when an agent needs to QA a web application or gameplay flow from a Git worktree with Playwright as the browser runtime, especially when the project needs isolated ports, fixture processes, focused test selection, or actionable screenshots and traces.
---

# Heimdal Playwright QA

Use Heimdal as the thin, worktree-aware entry point to a repository's native
Playwright test runner. Heimdal owns discovery, isolation, process cleanup, and
evidence; Playwright owns browser contexts, locators, assertions, fixtures, and
the actual test runtime.

## Workflow

1. Read the repository's `AGENTS.md` and the docs that own the flow. Identify
   the real player-facing or user-facing control path and the narrowest existing
   Playwright spec that proves it.
2. From the active worktree, check the runtime:

   ```bash
   heimdal doctor --json
   ```

   If Chromium is not installed, run `heimdal install chromium` once. Do not
   install dependencies or start a second copy of the app unless the project
   contract requires it.
3. Run one focused proof. Put Playwright arguments after `--` so Heimdal flags
   remain unambiguous:

   ```bash
   heimdal run -- tests/browser/<flow>.spec.ts --grep "<behavior>"
   ```

   Use `--headed` only when visual inspection is necessary. Pass fixture
   switches as environment variables before the command; the runner preserves
   them and adds the project-configured run id and port.
4. Treat the command's exit code and `result.json` as the verdict. On failure,
   inspect the stdout/stderr tails first, then the saved screenshot, video, or
   trace. Use `heimdal report --run <id> --json` for a machine-readable recap
   and `heimdal trace --run <id>` to open the newest trace.
5. Report the exact worktree, command, run id, result, and artifact directory.
   Keep failure artifacts until the diagnosis or fix is complete.

## QA rules

- Drive real user-facing controls and assert subscribed/rendered outcomes. Do
  not call reducers, APIs, databases, or private test hooks to manufacture a
  state that a player could not reach.
- Prefer one focused spec or grep over the full suite. A retry is evidence only
  after the first failure is captured; do not hide a deterministic failure by
  increasing retries.
- Run from the current Git worktree. Heimdal creates a unique run directory and
  configurable port so concurrent worktrees do not share browser artifacts or
  fixture state.
- Use Playwright only as the browser runtime. Do not substitute the Codex
  in-app browser, MCP browser control, ad hoc scripts, or another automation
  layer for a repository's Playwright tests.
- Keep credentials out of test arguments, screenshots, traces, and logs. Use
  the project's ignored environment file and bounded test fixtures.
- If the app cannot start, classify it as an environment/fixture failure and
  preserve the command and logs; do not rewrite the test to bypass startup.

## Project contract

Projects work without configuration when they have a normal
`playwright.config.*` and a local Playwright install. Add a root
`.heimdal.json` when a project needs custom environment names or a non-default
artifact location:

```json
{
  "version": 1,
  "playwright": {
    "config": "playwright.config.ts",
    "run_id_env": "APP_PLAYWRIGHT_RUN_ID",
    "port_env": "APP_PLAYWRIGHT_PORT",
    "env": {
      "APP_QA_ARTIFACT_DIR": "${RUN_DIR}"
    }
  },
  "artifacts": {
    "directory": ".dev/heimdal"
  }
}
```

`run_id_env` and `port_env` are optional names consumed by the project's
Playwright config or fixture. `env` values may use `${RUN_ID}`, `${RUN_DIR}`,
`${OUTPUT_DIR}`, `${REPORT_DIR}`, `${ROOT}`, `${BRANCH}`, or `${PORT}`.
`runner`, when needed, is an argument array for the Playwright executable
before the `test` subcommand; it is not a shell string.

The project config should consume Heimdal's `HEIMDAL_RUN_DIR`,
`HEIMDAL_PLAYWRIGHT_OUTPUT_DIR`, and `HEIMDAL_PLAYWRIGHT_REPORT_DIR` values for
its Playwright `outputDir` and HTML report folder. Keep app-specific fixture
selection and web-server lifecycle in that config, not in the CLI.
