# Runs, reports, and artifacts

Every deterministic run receives an isolated directory under `.heimdal/` by
default. It can contain the final result, stdout, stderr, Playwright output,
reports, screenshots, videos, and traces.

`heimdal run` stays in the foreground until its terminal result. Keep waiting
on that process; `report` is for a separate observer or post-run diagnosis.

## Inspect a run

Start with the bounded report:

```bash
heimdal report --run latest --json
heimdal trace --run latest-failed --json
```

Use `--json=full` only when retained raw detail is required inline. Without
`--json`, `heimdal trace` opens Playwright's interactive trace viewer.

Heimdal prefers the terminal runner error recorded in a trace. Earlier failed
assertions that execution continued past are retained as caught-probe evidence
rather than replacing the terminal cause.

## Browse history

```bash
heimdal runs list --status failed --since 2d
heimdal runs show latest-failed --json
heimdal runs compare older-run newer-run --json
heimdal runs pin important-run
```

Run inventory includes selectors, safe fixture-flag state, Git identity,
status, timing, size, interrupted state, and semantic and exact failure
fingerprints. Pinned runs are protected from retention.

## Artifact retention

Default retention:

- runs older than 14 days become eligible for cleanup;
- retained artifacts are bounded to 5 GiB;
- the newest full run for up to 20 failure fingerprints is protected;
- older copies of the same failure may be compacted;
- duplicate traces, videos, and screenshots within a run may be hard-linked.

Pinned, active, and unrecognized directories are never removed. Pruned runs
keep small history records.

Inspect cleanup before applying it:

```bash
heimdal gc --dry-run
heimdal gc --older-than 14d --keep-failures 20
heimdal gc --max-bytes 5GB --dry-run
```

Automatic cleanup runs at most daily. `heimdal doctor --json` reports artifact
usage, budget, reclaimable bytes, and interrupted runs.

Use `--run-id ID` when another process needs a stable run name. IDs contain
lowercase letters, numbers, and hyphens.

## Session inventory

Discover sessions without filesystem searches:

```bash
heimdal sessions list --json
heimdal sessions list --status stale --json
heimdal sessions prune --dry-run --json
```

Heimdal reports `active`, `stopped`, `stale`, `unknown`, or `broken`. Pruning
finalizes stale state and removes dead global indexes while retaining session
evidence.
