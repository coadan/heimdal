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

## Code Mode Tool Batching

When `functions.exec` is available, run independent tool calls concurrently
within one bounded stage. Prefer `await Promise.allSettled([...])` and inspect
every result. `Promise.all(...)` rejects early but does not cancel calls that
already started, so use it only when discarding other results is intentional.
Keep dependencies, waits/resumes, approvals, adaptive investigations,
conflicting mutations, and builds or mutations that write the same outputs
sequential. Do not split otherwise batchable inspections across outer calls.

Keep each nested call's output bounded. Prefer focused queries and per-call
output limits; broad outputs that can truncate task evidence are not a valid
efficiency gain. If a result is truncated, narrow or page only that result
instead of rerunning the whole batch.

## Minimal Working Rules

- Understand the task and trace the real flow first. Then stop at the first
  sufficient rung: skip speculative work, reuse repository code, use the
  standard library, use native platform features, use an installed dependency,
  and only then write the minimum new code.
- Fix root causes at the shared boundary after checking callers. Prefer
  deletion, boring code, few files, and the shortest correct diff.
- Do not add one-use abstractions, future scaffolding/config, or dependencies
  when existing code or a few direct lines suffice.
- Do not simplify away requested behavior, security, trust-boundary validation,
  data-loss/error handling, or accessibility.
- Leave the smallest runnable regression check for non-trivial logic. Mark a
  deliberate ceiling with a `ponytail:` comment and its upgrade trigger.
