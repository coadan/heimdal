package cli

import "strings"

const doctorUsage = `Check Heimdal capabilities for a project

Usage:
  heimdal doctor [--dir PATH] [--json]

Reports the resolved worktree, Playwright runtimes, configuration, artifact
root, and actionable readiness warnings without changing the project.
`

const initUsage = `Create a minimal Heimdal project configuration

Usage:
  heimdal init [--dir PATH] [--force]

Writes .heimdal.json at the discovered project root. Existing configuration is
preserved unless --force is supplied.
`

const runUsage = `Run repository-owned Playwright tests with isolated evidence

Usage:
  heimdal run [options] [-- PLAYWRIGHT_ARGS...]
  heimdal list [options] [-- PLAYWRIGHT_ARGS...]

Options:
  --dir PATH       Discover the project from PATH
  --run-id ID      Stable artifact identity
  --artifacts DIR  Override the configured artifact root
  --port PORT      Override the isolated fixture port
  --config FILE    Override the Playwright config
  --headed         Run Playwright headed
  --json           Print structured result JSON
  --help           Print this help

Arguments after -- are forwarded unchanged to Playwright. A run that discovers
tests but executes none returns status skipped and a nonzero exit.
`

const reportUsage = `Inspect a live or completed Heimdal run

Usage:
  heimdal report [--dir PATH] [--run ID] [--json|--json=full]

Options:
  --dir PATH   Discover the project from PATH
  --run ID     Select a run (default: latest)
  --json       Print compact diagnostic JSON
  --json=full  Include raw log tails and the complete file index
  --help       Print this help
`

const metadataUsage = `Publish or read bounded run-scoped JSON metadata

Usage:
  heimdal metadata publish NAMESPACE --file FILE|- [--dir PATH] [--run ID] [--json]
  heimdal metadata get [NAMESPACE] [--dir PATH] [--run ID] [--json]

Payloads are limited to 64 KiB and must come from a file or stdin, never a
command-line value. Without --run, HEIMDAL_RUN_DIR is used when available.
`

const signalUsage = `Send or wait for an idempotent run-scoped signal

Usage:
  heimdal signal send NAME [--dir PATH] [--run ID] [--json]
  heimdal signal wait NAME [--dir PATH] [--run ID] [--timeout DURATION] [--json]

Signals coordinate fixtures and agents without arbitrary sleeps.
`

const installUsage = `Install a Playwright runtime component

Usage:
  heimdal install [--dir PATH] [BROWSER...]
  heimdal install [--dir PATH] agent-cli
  heimdal install [--dir PATH] agent-browser [BROWSER]

Repository browser installs delegate to the project's Playwright runner.
Agent CLI and agent-browser installs support persistent Heimdal sessions.
`

const skillUsage = `Inspect or install Heimdal's bundled coding-agent skill

Usage:
  heimdal skill path
  heimdal skill install [--destination DIR] [--force]

The default destination is $CODEX_HOME/skills/heimdal-playwright-qa.
`

func commandHelp(args []string) (string, bool) {
	if len(args) >= 2 && args[0] == "help" {
		return helpForCommand(args[1])
	}
	if len(args) == 0 {
		return "", false
	}
	wantsHelp := false
	for _, arg := range args[1:] {
		if arg == "--" {
			break
		}
		if arg == "--help" || arg == "-h" || arg == "help" {
			wantsHelp = true
			break
		}
	}
	if !wantsHelp {
		return "", false
	}
	return helpForCommand(args[0])
}

func helpForCommand(command string) (string, bool) {
	switch strings.ToLower(command) {
	case "doctor":
		return doctorUsage, true
	case "init":
		return initUsage, true
	case "run", "list":
		return runUsage, true
	case "report":
		return reportUsage, true
	case "trace":
		return traceUsage, true
	case "gc":
		return gcUsage, true
	case "metadata":
		return metadataUsage, true
	case "signal":
		return signalUsage, true
	case "install":
		return installUsage, true
	case "skill":
		return skillUsage, true
	case "session":
		return sessionUsage, true
	default:
		return "", false
	}
}
