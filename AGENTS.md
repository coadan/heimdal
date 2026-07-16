# Heimdal Agent Guide

Heimdal is a thin Go orchestrator around repository-owned Playwright tests.
Keep browser automation in Playwright; keep discovery, process lifecycle,
worktree isolation, artifact capture, and agent-facing output in the CLI.

Before changing behavior, run `go test ./...` and inspect the focused command
with `go run ./cmd/heimdal help`. Do not add a second browser protocol, direct
DOM automation, MCP dependency, or project-specific gameplay logic here.

The bundled skill lives at `skills/heimdal-playwright-qa/SKILL.md`. If its
workflow changes, update the embedded skill tests and run `heimdal skill
install --force` against a temporary `CODEX_HOME` to verify materialization.
