package cli

import "fmt"

var sessionCommandUsage = map[string]string{
	"list": `Discover persistent Heimdal sessions across worktrees

Usage:
  heimdal session list [--dir PATH] [--status active|stopped|stale|unknown|broken] [--limit N] [--json]

This is an alias for heimdal sessions list.
`,
	"prune": `Finalize stale session state without discarding evidence

Usage:
  heimdal session prune [--dir PATH] [--dry-run] [--limit N] [--json]

This is an alias for heimdal sessions prune.
`,
	"start": `Start a persistent Playwright browser session and optional project app

Usage:
  heimdal session start [--dir PATH] [--name NAME] [--url URL] [--headed] [--no-server] [--json]

The initial response includes a bounded semantic snapshot. Headless is the
default; --headed keeps the same session and evidence behavior in a visible
browser. A configured session.command is started and stopped with the browser.
`,
	"stop": `Stop a persistent Playwright browser session and its owned app process

Usage:
  heimdal session stop [--dir PATH] [--name NAME] [--json]
`,
	"status": `Inspect persisted session and app-process state without browser work

Usage:
  heimdal session status [--dir PATH] [--name NAME] [--json|--json=full]
`,
	"observe": `Return a bounded Playwright semantic snapshot with current refs

Usage:
  heimdal session observe [--name NAME] [--boxes] [--full] [--json] [-- PLAYWRIGHT_OPTIONS...]
`,
	"screenshot": `Capture a page or target screenshot through Playwright

Usage:
  heimdal session screenshot [TARGET] [--name NAME] [--json] [-- PLAYWRIGHT_OPTIONS...]
`,
	"diagnose": `Collect console errors, failed requests, and current semantic state

Usage:
  heimdal session diagnose [--name NAME] [--stop] [--json|--json=full]

Repeated console and request failures are grouped into bounded signatures.
--stop closes a non-group browser and its owned app after evidence capture.
`,
	"wait": `Wait for user-visible semantic state through Playwright

Usage:
  heimdal session wait --role ROLE [--name ACCESSIBLE_NAME] [--state STATE] [--timeout AGE] [--settle AGE]
  heimdal session wait --text TEXT [--state STATE] [--timeout AGE] [--settle AGE]
  heimdal session wait --change [--timeout AGE] [--settle AGE]

Use --session NAME to select a named browser because --name is the accessible
name paired with --role.
`,
	"expect": `Record and execute a Playwright-backed outcome assertion

Usage:
  heimdal session expect --role ROLE [--name ACCESSIBLE_NAME] [--state STATE] [--timeout AGE]
  heimdal session expect --text TEXT [--state visible|hidden] [--timeout AGE]
  heimdal session expect --url URL [--timeout AGE]
  heimdal session expect --target TARGET --value VALUE [--timeout AGE]

Use --session NAME to select a named browser. Passing assertions graduate into
session save --test output.
`,
	"timeline": `Synthesize an ordered session timeline from retained action evidence

Usage:
  heimdal session timeline [NAME] [--dir PATH] [--failures] [--category NAME]
                           [--from N] [--to N] [--limit N] [--json|--json=full]

The default is a bounded phase/failure/recent-change view. Use filters to page
chronologically; next_from identifies the next sequence. --json=full opts into
all retained entries, including evidence summaries and snapshot paths.
`,
	"report": `Summarize session navigation, interactions, assertions, and failures

Usage:
  heimdal session report [NAME] [--dir PATH] [--failures] [--category NAME]
                         [--from N] [--to N] [--limit N] [--json|--json=full]

The default report contains bounded phases, causal failures, and recent
meaningful changes. Successful zero-result diagnostic checks are not issues.
`,
	"checkpoint": `Add a durable label to the current session timeline

Usage:
  heimdal session checkpoint LABEL [--name NAME] [--json]

Checkpoints label recoverable session state; they do not resume arbitrary test
fixtures from a step.
`,
	"measure": `Return bounded decision-ready layout evidence

Usage:
  heimdal session measure [TARGET] [--session NAME] [--json]

Without TARGET, the packet includes viewport/document geometry, overflow,
controls, early content, and semantic or grid/flex regions. With TARGET, it
also includes that element's rectangle and key computed styles.
`,
	"batch": `Execute a bounded JSON sequence in one agent round trip

Usage:
  heimdal session batch --file FILE|- [--name NAME] [--json|--json=full]

Safe unambiguous batches use one Playwright code invocation plus one final ref
refresh; JSON reports execution mode and Playwright invocation count.
`,
	"save": `Save a session transcript and optional Playwright test draft

Usage:
  heimdal session save [--name NAME] [--test PATH] [--ready] [--json]

--ready returns nonzero when assertions or portable actions are missing while
still writing the draft for repair.
`,
	"group": `Manage multiple isolated Playwright actors sharing one project fixture

Usage:
  heimdal session group --help
`,
	"click": `Click a target or an element-relative point through Playwright

Usage:
  heimdal session click TARGET [left|right|middle|--force] [--name NAME] [--json]
  heimdal session click --within TARGET --at X%,Y% [--name NAME] [--json]
`,
	"fill": `Fill a target and optionally submit it through Playwright

Usage:
  heimdal session fill TARGET TEXT [--submit] [--name NAME] [--json]
`,
	"press": `Press a key globally or against a target through Playwright

Usage:
  heimdal session press KEY [--name NAME] [--json]
  heimdal session press TARGET KEY [--name NAME] [--json]
`,
	"type": `Type text globally or against a target through Playwright

Usage:
  heimdal session type TEXT [--name NAME] [--json]
  heimdal session type TARGET TEXT [--name NAME] [--json]
`,
	"mouse": `Perform a stable absolute coordinate click through Playwright

Usage:
  heimdal session mouse click X Y [--name NAME] [--json]

Prefer click --within for layout-resilient spatial interaction.
`,
	"pointer": `Drag between element-relative points through Playwright

Usage:
  heimdal session pointer drag --within TARGET --from X%,Y% --to X%,Y% [--name NAME] [--json]
`,
}

func sessionHelpForCommand(command string) string {
	if usage, ok := sessionCommandUsage[command]; ok {
		return usage
	}
	return fmt.Sprintf(`Delegate a browser command to the official Playwright CLI

Usage:
  heimdal session %s [HEIMDAL_SESSION_OPTIONS] [-- PLAYWRIGHT_OPTIONS...]

Heimdal resolves the persistent worktree session, records bounded evidence, and
delegates this command to Playwright. Run playwright-cli --help for the runtime
command's upstream grammar.
`, command)
}
