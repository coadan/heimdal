# Heimdal Agent Guide

Heimdal is a thin Go control plane around the official Playwright CLI and
repository-owned Playwright tests. Keep browser automation in Playwright; keep
fixture lifecycle, worktree isolation, artifact capture, and agent-facing
output in the CLI.

Before changing behavior, run `go test ./...` and inspect the focused command
with `go run ./cmd/heimdal help`. Do not add a second browser protocol, direct
DOM automation, MCP dependency, or project-specific gameplay logic here.

The bundled skill lives at `skills/heimdal-playwright-qa/SKILL.md`. If its
workflow changes, update the embedded skill tests and run `heimdal skill
install --force` against a temporary `CODEX_HOME` to verify materialization.

After each accepted Heimdal improvement, validate it, commit and push `main`,
then reinstall the CLI and bundled skill from that exact commit. Compare the
repository skill with the installed copy before starting the next improvement;
do not carry skill drift or unpushed implementation changes between slices.

Do not add a second browser protocol or direct DOM automation. Session commands
must delegate browser work to the project's official `playwright-cli` package.
The Go layer may create a session config, launch a project fixture process,
capture evidence, and write JSON; it must not become a competing Playwright
implementation.

Keep Heimdal repository-independent. Source, tests, docs, examples, and the
embedded skill use generic project and fixture names, never names or contracts
from a consuming repository.
