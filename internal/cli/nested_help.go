package cli

import "strings"

var nestedCommandUsage = map[string]string{
	"sessions list": `Discover persistent Heimdal sessions across worktrees

Usage:
  heimdal sessions list [--dir PATH] [--status active|stopped|stale|unknown|broken] [--limit N] [--json]

Each worktree's Playwright workspace is probed so dead browsers and owned app
processes are reported as stale instead of active.
`,
	"sessions prune": `Finalize stale session state without discarding evidence

Usage:
  heimdal sessions prune [--dir PATH] [--dry-run] [--limit N] [--json]

Prune marks stale sessions stopped and removes stale or broken global indexes.
Retained action, screenshot, and diagnostic evidence is preserved.
`,
	"runs list": `List indexed Heimdal runs without scanning artifact trees

Usage:
  heimdal runs list [--dir PATH] [--status STATUS] [--since AGE] [--test TEXT] [--limit N] [--json]
`,
	"runs show": `Show one indexed run and its compact diagnostic report

Usage:
  heimdal runs show RUN_ID|latest|latest-failed [--dir PATH] [--json|--json=full]
`,
	"runs compare": `Compare two indexed run summaries

Usage:
  heimdal runs compare OLD NEW [--dir PATH] [--json]
`,
	"runs pin": `Protect or unprotect a run from artifact retention

Usage:
  heimdal runs pin RUN_ID|latest|latest-failed [--dir PATH] [--remove] [--json]
`,
	"metadata publish": `Publish bounded immutable JSON metadata for a run

Usage:
  heimdal metadata publish NAMESPACE --file FILE|- [--dir PATH] [--run ID] [--json]

Values never belong on the command line. Payloads are limited to 64 KiB.
`,
	"metadata get": `Read the newest run-scoped metadata value or namespace index

Usage:
  heimdal metadata get [NAMESPACE] [--dir PATH] [--run ID] [--json]
`,
	"signal send": `Idempotently publish a named run milestone

Usage:
  heimdal signal send NAME [--dir PATH] [--run ID] [--json]
`,
	"signal wait": `Wait for a named run milestone without polling conventions

Usage:
  heimdal signal wait NAME [--dir PATH] [--run ID] [--timeout AGE] [--json]
`,
	"skill path": `Print the installed destination for Heimdal's bundled agent skill

Usage:
  heimdal skill path
`,
	"skill install": `Install or refresh Heimdal's bundled agent skill

Usage:
  heimdal skill install [--destination DIR] [--force]
`,
	"trace inspect": `Extract bounded failure-centered evidence from a retained Playwright trace

Usage:
  heimdal trace inspect [--dir PATH] [--run ID|latest|latest-failed] [--around-failure] [--json]

Terminal runner errors are correlated with trace errors or the last relevant
action. Continued-past assertion failures are reported separately as caught
probes.
`,
}

func nestedHelpForCommand(command, subcommand string) (string, bool) {
	usage, ok := nestedCommandUsage[strings.ToLower(command)+" "+strings.ToLower(subcommand)]
	return usage, ok
}
