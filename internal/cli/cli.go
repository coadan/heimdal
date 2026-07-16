package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const usage = `Heimdal — agent-oriented Playwright orchestration

Usage:
  heimdal doctor [--root DIR] [--json]
  heimdal init [--root DIR] [--force]
  heimdal run [options] [-- PLAYWRIGHT_ARGS...]
  heimdal list [options] [-- PLAYWRIGHT_ARGS...]
  heimdal session start [options]
  heimdal session stop [options]
  heimdal session status [options]
  heimdal session observe [options]
  heimdal session screenshot [options]
  heimdal session diagnose [options]
  heimdal session save [options]
  heimdal session <PLAYWRIGHT_CLI_COMMAND> [options]
  heimdal report [--root DIR] [--run ID] [--json]
  heimdal trace [--root DIR] [--run ID] [TRACE]
  heimdal install [--root DIR] [BROWSER...|agent-cli|agent-browser]
  heimdal skill install [--destination DIR] [--force]
  heimdal skill path

Run options:
  --root DIR       Run as if invoked from DIR (defaults to the current worktree)
  --run-id ID      Stable artifact/run identity
  --artifacts DIR  Override the artifact root
  --port PORT      Override the project isolation port
  --config FILE    Override the Playwright config
  --headed         Forward --headed to Playwright
  --json           Print only the agent-readable result JSON

Session options:
  --root DIR       Worktree root; persisted on session start
  --name NAME      Named persistent Playwright agent session
  --url URL        URL to open, or project session.url
  --profile DIR    Persistent browser profile directory
  --browser NAME   Browser engine/channel for Playwright CLI
  --persistent     Keep browser profile on disk
  --no-server      Do not start the configured session command
  --no-boxes       Omit bounding boxes from snapshots
  --verbose        Show complete Playwright CLI output
  --force          Replace an existing Heimdal session state

Examples:
  heimdal doctor
  heimdal run -- tests/browser/combat.spec.ts --grep "victory"
  heimdal run --headed -- tests/browser/combat.spec.ts
  heimdal report --run codex-browser-20260716t120000z-1234
  heimdal trace --run latest
`

func Run(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, usage)
		return 0
	}
	switch args[0] {
	case "doctor":
		return runDoctor(args[1:], out, errOut)
	case "init":
		return runInit(args[1:], out, errOut)
	case "run":
		return runTests(ctx, args[1:], out, errOut, false)
	case "list":
		return runTests(ctx, args[1:], out, errOut, true)
	case "session":
		return runSession(ctx, args[1:], out, errOut)
	case "report":
		return runReport(args[1:], out, errOut)
	case "trace":
		return runTrace(ctx, args[1:], out, errOut)
	case "install":
		return runInstall(ctx, args[1:], out, errOut)
	case "skill":
		return runSkill(args[1:], out, errOut)
	default:
		fmt.Fprintf(errOut, "heimdal: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func runTests(ctx context.Context, args []string, out, errOut io.Writer, list bool) int {
	options, err := parseRunOptions(args)
	if err != nil {
		fmt.Fprintln(errOut, "heimdal:", err)
		return 2
	}
	project, err := Discover(options.Root)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if options.Config != "" {
		options.Config = relativeToRoot(project.Root, options.Config)
	}
	if list && !containsFlag(options.Forwarded, "--list") {
		options.Forwarded = append(options.Forwarded, "--list")
	}
	result, err := executeRun(ctx, project, options, out, errOut)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if options.JSON {
		_ = writeJSONTo(out, result)
	}
	return normalizeExitCode(result.ExitCode)
}

func runDoctor(args []string, out, errOut io.Writer) int {
	root, asJSON, err := parseSimpleOptions(args, true)
	if err != nil {
		return reportError(asJSON, err, out, errOut)
	}
	project, discoverErr := Discover(root)
	report := DoctorReport{SchemaVersion: 1, Status: "ok", Root: root}
	if discoverErr != nil {
		report.Status = "error"
		report.Error = discoverErr.Error()
		return printDoctor(report, asJSON, out, errOut, 1)
	}
	report.Root = project.Root
	report.Branch = project.Branch
	report.ConfigFile = project.ConfigFile
	report.PlaywrightConfig = project.PlaywrightConfig
	report.PackageManager = project.PackageManager
	report.Runner = project.Runner
	report.AgentRunner = project.AgentRunner
	report.ArtifactRoot = artifactRoot(project, "")
	version, versionErr := runCapture(project.Root, append(project.Runner, "--version"), baseEnvironment())
	if versionErr != nil {
		report.Status = "error"
		report.Error = fmt.Sprintf("Playwright is not runnable: %s", versionErr)
	} else {
		report.PlaywrightVersion = version
	}
	if len(project.AgentRunner) > 0 {
		agentVersion, agentErr := runCapture(project.Root, append(project.AgentRunner, "--version"), baseEnvironment())
		if agentErr != nil {
			report.Warnings = append(report.Warnings, "playwright-cli is not available; run `heimdal install agent-cli`")
		} else {
			report.AgentVersion = agentVersion
		}
	} else {
		report.Warnings = append(report.Warnings, "playwright-cli is not available; run `heimdal install agent-cli`")
	}
	if report.PlaywrightConfig == "" {
		report.Warnings = append(report.Warnings, "no playwright.config.* found; Playwright defaults will be used")
	}
	return printDoctor(report, asJSON, out, errOut, boolToCode(report.Status != "ok"))
}

type DoctorReport struct {
	SchemaVersion     int      `json:"schema_version"`
	Status            string   `json:"status"`
	Root              string   `json:"root"`
	Branch            string   `json:"branch,omitempty"`
	ConfigFile        string   `json:"config_file,omitempty"`
	PlaywrightConfig  string   `json:"playwright_config,omitempty"`
	PackageManager    string   `json:"package_manager,omitempty"`
	Runner            []string `json:"runner,omitempty"`
	AgentRunner       []string `json:"agent_runner,omitempty"`
	PlaywrightVersion string   `json:"playwright_version,omitempty"`
	AgentVersion      string   `json:"agent_cli_version,omitempty"`
	ArtifactRoot      string   `json:"artifact_root,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
	Error             string   `json:"error,omitempty"`
}

func printDoctor(report DoctorReport, asJSON bool, out, errOut io.Writer, exitCode int) int {
	if asJSON {
		_ = writeJSONTo(out, report)
		return exitCode
	}
	if exitCode != 0 {
		fmt.Fprintln(errOut, "Heimdal doctor: error")
		if report.Error != "" {
			fmt.Fprintln(errOut, report.Error)
		}
		return exitCode
	}
	fmt.Fprintf(out, "Heimdal doctor: ready (%s)\n", report.Branch)
	fmt.Fprintf(out, "  root: %s\n  Playwright: %s\n  artifacts: %s\n", report.Root, report.PlaywrightVersion, report.ArtifactRoot)
	for _, warning := range report.Warnings {
		fmt.Fprintf(out, "  warning: %s\n", warning)
	}
	return 0
}

func runInit(args []string, out, errOut io.Writer) int {
	root, force, err := parseInitOptions(args)
	if err != nil {
		return reportError(false, err, out, errOut)
	}
	project, err := Discover(root)
	if err != nil {
		return reportError(false, err, out, errOut)
	}
	cfg := defaultConfig(project.PlaywrightConfig)
	path := filepath.Join(project.Root, configFileName)
	if err := writeProjectConfig(path, cfg, force); err != nil {
		return reportError(false, err, out, errOut)
	}
	fmt.Fprintf(out, "Created %s\n", path)
	return 0
}

func runReport(args []string, out, errOut io.Writer) int {
	root, asJSON, runID, err := parseReportOptions(args)
	if err != nil {
		return reportError(asJSON, err, out, errOut)
	}
	project, err := Discover(root)
	if err != nil {
		return reportError(asJSON, err, out, errOut)
	}
	path := ""
	if runID != "" && runID != "latest" {
		path = filepath.Join(artifactRoot(project, ""), sanitize(runID), "result.json")
	}
	var result RunResult
	if path == "" {
		result, err = findLatestResult(artifactRoot(project, ""))
	} else {
		result, err = readResult(path)
	}
	if err != nil {
		return reportError(asJSON, err, out, errOut)
	}
	if asJSON {
		_ = writeJSONTo(out, result)
	} else {
		printResult(out, result)
		if len(result.Artifacts.Files) > 0 {
			fmt.Fprintln(out, "Files:")
			for _, file := range result.Artifacts.Files {
				fmt.Fprintf(out, "  %s\n", file)
			}
		}
	}
	return normalizeExitCode(result.ExitCode)
}

func runTrace(ctx context.Context, args []string, out, errOut io.Writer) int {
	root, runID, tracePath, err := parseTraceOptions(args)
	if err != nil {
		return reportError(false, err, out, errOut)
	}
	project, err := Discover(root)
	if err != nil {
		return reportError(false, err, out, errOut)
	}
	if tracePath == "" {
		var result RunResult
		if runID == "" || runID == "latest" {
			result, err = findLatestResult(artifactRoot(project, ""))
		} else {
			result, err = readResult(filepath.Join(artifactRoot(project, ""), sanitize(runID), "result.json"))
		}
		if err == nil {
			tracePath, err = findTrace(result.Artifacts.RunDir)
		}
	}
	if err != nil {
		return reportError(false, err, out, errOut)
	}
	command := append(project.Runner, "show-trace", tracePath)
	fmt.Fprintf(out, "Opening trace: %s\n", tracePath)
	cmd := execCommandContext(ctx, project.Root, command, os.Environ())
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return normalizeExitCode(processExitCode(err))
	}
	return 0
}

func runInstall(ctx context.Context, args []string, out, errOut io.Writer) int {
	root, forwarded, err := parseRootAndForward(args)
	if err != nil {
		return reportError(false, err, out, errOut)
	}
	project, err := Discover(root)
	if err != nil {
		return reportError(false, err, out, errOut)
	}
	if len(forwarded) == 1 && (forwarded[0] == "agent-cli" || forwarded[0] == "playwright-cli") {
		return installAgentCLI(ctx, project, out, errOut)
	}
	if len(forwarded) >= 1 && (forwarded[0] == "agent-browser" || forwarded[0] == "playwright-cli-browser") {
		browser := "chromium"
		if len(forwarded) > 1 {
			if len(forwarded) != 2 {
				return reportError(false, errors.New("agent-browser accepts one browser name"), out, errOut)
			}
			browser = forwarded[1]
		}
		return installAgentBrowser(ctx, project, browser, out, errOut)
	}
	command := append(project.Runner, "install")
	command = append(command, forwarded...)
	fmt.Fprintf(out, "%s\n", commandString(command))
	cmd := execCommandContext(ctx, project.Root, command, os.Environ())
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return normalizeExitCode(processExitCode(err))
	}
	return 0
}

func runSkill(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] == "path" {
		if len(args) > 1 {
			return reportError(false, errors.New("skill path does not accept arguments"), out, errOut)
		}
		fmt.Fprintln(out, defaultSkillDestination())
		return 0
	}
	if args[0] != "install" {
		return reportError(false, fmt.Errorf("unknown skill command %q", args[0]), out, errOut)
	}
	destination := ""
	force := false
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--force":
			force = true
		case "--destination":
			if i+1 >= len(args) {
				return reportError(false, errors.New("--destination requires a directory"), out, errOut)
			}
			i++
			destination = args[i]
		default:
			return reportError(false, fmt.Errorf("unknown skill install option %q", args[i]), out, errOut)
		}
	}
	if destination == "" {
		destination = defaultSkillDestination()
	}
	if err := installSkill(destination, force); err != nil {
		return reportError(false, err, out, errOut)
	}
	fmt.Fprintf(out, "Installed Heimdal skill at %s\n", destination)
	return 0
}

func parseRunOptions(args []string) (RunOptions, error) {
	options := RunOptions{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			options.Forwarded = append(options.Forwarded, args[i+1:]...)
			break
		}
		switch arg {
		case "--root":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i = next
			options.Root = value
		case "--json":
			options.JSON = true
		case "--headed":
			options.Headed = true
		case "--run-id":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i = next
			options.RunID = value
		case "--artifacts":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i = next
			options.Artifacts = value
		case "--port":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i = next
			port, parseErr := strconv.Atoi(value)
			if parseErr != nil || port < 1 || port > 65535 {
				return options, fmt.Errorf("--port must be between 1 and 65535 (got %q)", value)
			}
			options.Port = port
		case "--config", "-c":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i = next
			options.Config = value
		case "--help", "-h":
			return options, errors.New(usage)
		default:
			options.Forwarded = append(options.Forwarded, arg)
		}
	}
	return options, nil
}

func nextValue(args []string, index int, flag string) (string, int, error) {
	if index+1 >= len(args) || args[index+1] == "--" {
		return "", index, fmt.Errorf("%s requires a value", flag)
	}
	return args[index+1], index + 1, nil
}

func parseSimpleOptions(args []string, allowJSON bool) (string, bool, error) {
	root := ""
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--root":
			value, next, err := nextValue(args, i, "--root")
			if err != nil {
				return root, asJSON, err
			}
			i = next
			root = value
		case "--json":
			if !allowJSON {
				return root, asJSON, errors.New("--json is not supported here")
			}
			asJSON = true
		case "--help", "-h":
			return root, asJSON, errors.New(usage)
		default:
			return root, asJSON, fmt.Errorf("unknown option %q", args[i])
		}
	}
	return root, asJSON, nil
}

func parseInitOptions(args []string) (string, bool, error) {
	root := ""
	force := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--root":
			value, next, err := nextValue(args, i, "--root")
			if err != nil {
				return root, force, err
			}
			i = next
			root = value
		case "--force":
			force = true
		default:
			return root, force, fmt.Errorf("unknown option %q", args[i])
		}
	}
	return root, force, nil
}

func parseReportOptions(args []string) (string, bool, string, error) {
	root, asJSON, err := parseSimpleOptionsWithoutUnknown(args)
	if err != nil {
		return root, asJSON, "", err
	}
	runID := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--run" {
			value, next, nextErr := nextValue(args, i, "--run")
			if nextErr != nil {
				return root, asJSON, runID, nextErr
			}
			i = next
			runID = value
		}
	}
	return root, asJSON, runID, nil
}

func parseSimpleOptionsWithoutUnknown(args []string) (string, bool, error) {
	root, asJSON := "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--root":
			value, next, err := nextValue(args, i, "--root")
			if err != nil {
				return root, asJSON, err
			}
			i = next
			root = value
		case "--json":
			asJSON = true
		case "--run":
			_, next, err := nextValue(args, i, "--run")
			if err != nil {
				return root, asJSON, err
			}
			i = next
		case "--help", "-h":
			return root, asJSON, errors.New(usage)
		default:
			return root, asJSON, fmt.Errorf("unknown option %q", args[i])
		}
	}
	return root, asJSON, nil
}

func parseTraceOptions(args []string) (string, string, string, error) {
	root, runID := "", ""
	trace := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--root":
			value, next, err := nextValue(args, i, "--root")
			if err != nil {
				return root, runID, trace, err
			}
			i = next
			root = value
		case "--run":
			value, next, err := nextValue(args, i, "--run")
			if err != nil {
				return root, runID, trace, err
			}
			i = next
			runID = value
		default:
			if strings.HasPrefix(args[i], "-") {
				return root, runID, trace, fmt.Errorf("unknown option %q", args[i])
			}
			if trace != "" {
				return root, runID, trace, errors.New("only one trace path may be provided")
			}
			trace = args[i]
		}
	}
	return root, runID, trace, nil
}

func parseRootAndForward(args []string) (string, []string, error) {
	root := ""
	var forwarded []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--root" {
			value, next, err := nextValue(args, i, "--root")
			if err != nil {
				return root, forwarded, err
			}
			i = next
			root = value
			continue
		}
		forwarded = append(forwarded, args[i])
	}
	return root, forwarded, nil
}

func relativeToRoot(root, value string) string {
	if !filepath.IsAbs(value) {
		return value
	}
	if relative, err := filepath.Rel(root, value); err == nil {
		return relative
	}
	return value
}

func reportError(asJSON bool, err error, out, errOut io.Writer) int {
	if asJSON {
		_ = writeJSONTo(out, map[string]any{"schema_version": 1, "status": "error", "error": err.Error()})
	} else {
		fmt.Fprintln(errOut, "heimdal:", err)
	}
	return 1
}

func writeJSONTo(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func normalizeExitCode(code int) int {
	if code < 0 || code > 125 {
		return 1
	}
	return code
}

func boolToCode(value bool) int {
	if value {
		return 1
	}
	return 0
}
