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
	"time"
)

const usage = `Heimdal — agent-oriented Playwright orchestration

Usage:
  heimdal version
  heimdal doctor [--dir PATH] [--json]
  heimdal init [--dir PATH] [--force]
  heimdal run [options] [-- PLAYWRIGHT_ARGS...]
  heimdal list [options] [-- PLAYWRIGHT_ARGS...]
  heimdal session start [options]
  heimdal session stop [options]
  heimdal session status [options]
  heimdal session observe [options]
  heimdal session screenshot [options]
  heimdal session diagnose [options]
  heimdal session batch --file FILE|- [options]
  heimdal session save [options]
  heimdal session <PLAYWRIGHT_CLI_COMMAND> [options]
  heimdal report [--dir PATH] [--run ID] [--json]
  heimdal runs list [--dir PATH] [--status STATUS] [--json]
  heimdal runs show SELECTOR [--dir PATH] [--json]
  heimdal runs compare OLD NEW [--dir PATH] [--json]
  heimdal runs pin SELECTOR [--dir PATH] [--remove] [--json]
  heimdal trace [--dir PATH] [--run ID] [TRACE]
  heimdal gc [--dir PATH] [--dry-run] [options]
  heimdal metadata publish NAMESPACE [--dir PATH] [--run ID] [--file FILE|-] [--json]
  heimdal metadata get [NAMESPACE] [--dir PATH] [--run ID] [--json]
  heimdal signal send NAME [--dir PATH] [--run ID] [--json]
  heimdal signal wait NAME [--dir PATH] [--run ID] [--timeout DURATION] [--json]
  heimdal install [--dir PATH] [BROWSER...|agent-cli|agent-browser]
  heimdal skill install [--destination DIR] [--force]
  heimdal skill path

Run options:
  --dir PATH       Discover the project from PATH (defaults to the current directory)
  --run-id ID      Stable artifact/run identity
  --artifacts DIR  Override the artifact root
  --port PORT      Override the project isolation port
  --config FILE    Override the Playwright config
  --headed         Forward --headed to Playwright
  --json           Print only the agent-readable result JSON

Session options:
  --dir PATH       Discover the worktree from PATH; persisted on session start
  --name NAME      Named persistent Playwright agent session
  --url URL        URL to open, or project session.url
  --profile DIR    Persistent browser profile directory
  --browser NAME   Browser engine/channel for Playwright CLI
  --persistent     Keep browser profile on disk
  --no-server      Do not start the configured session command
  --boxes          Include bounding boxes for coordinate-based inspection
  --full           Return a full semantic snapshot instead of a delta
  --verbose        Show complete Playwright CLI output
  --force          Replace an existing Heimdal session state
  --json           Print compact agent-readable JSON
  --json=full      Include repeated session metadata in JSON actions

Examples:
  heimdal doctor
  heimdal run -- tests/browser/navigation.spec.ts --grep "opens the menu"
  heimdal run --headed -- tests/browser/navigation.spec.ts
  heimdal report --run browser-check-20260716t120000z-1234
  heimdal trace --run latest
`

func Run(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) > 0 && (args[0] == "version" || args[0] == "--version") {
		fmt.Fprintln(out, heimdalVersion())
		return 0
	}
	if help, ok := commandHelp(args); ok {
		fmt.Fprint(out, help)
		return 0
	}
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
	case "runs":
		return runRuns(args[1:], out, errOut)
	case "trace":
		return runTrace(ctx, args[1:], out, errOut)
	case "gc":
		return runGC(args[1:], out, errOut)
	case "metadata":
		return runMetadata(ctx, args[1:], out, errOut)
	case "signal":
		return runSignal(ctx, args[1:], out, errOut)
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
	report.ArtifactBudgetBytes = project.Config.Artifacts.Retention.MaxBytes
	if runs, inspectErr := inspectArtifactRuns(report.ArtifactRoot, time.Now().UTC()); inspectErr == nil {
		for _, run := range runs {
			report.ArtifactBytes += run.SizeBytes
			if run.Status == "interrupted" {
				report.InterruptedRuns++
			}
		}
		for _, run := range artifactGarbageCandidates(runs, project.Config.Artifacts.Retention, time.Now().UTC()) {
			report.ReclaimableBytes += run.SizeBytes
		}
		if report.ArtifactBudgetBytes > 0 && report.ArtifactBytes > report.ArtifactBudgetBytes {
			report.Warnings = append(report.Warnings, fmt.Sprintf("artifact usage exceeds the configured %d-byte budget; run `heimdal gc --dry-run`", report.ArtifactBudgetBytes))
		}
	}
	version, versionErr := runCapture(project.Root, append(project.Runner, "--version"), baseEnvironment())
	if versionErr != nil {
		report.Warnings = append(report.Warnings, "repository Playwright is not runnable; deterministic run and list commands require a project Playwright install")
	} else {
		report.PlaywrightReady = true
		report.PlaywrightVersion = version
	}
	if len(project.AgentRunner) > 0 {
		agentVersion, agentErr := runCapture(project.Root, append(project.AgentRunner, "--version"), baseEnvironment())
		if agentErr != nil {
			report.Warnings = append(report.Warnings, "playwright-cli is not available; interactive sessions require `heimdal install agent-cli`")
		} else {
			report.SessionReady = true
			report.AgentVersion = agentVersion
		}
	} else {
		report.Warnings = append(report.Warnings, "playwright-cli is not available; interactive sessions require `heimdal install agent-cli`")
	}
	if !report.PlaywrightReady && !report.SessionReady {
		report.Status = "error"
		report.Error = "neither repository Playwright tests nor interactive Playwright sessions are runnable"
	}
	if report.PlaywrightConfig == "" {
		report.Warnings = append(report.Warnings, "no playwright.config.* found; Playwright defaults will be used")
	}
	return printDoctor(report, asJSON, out, errOut, boolToCode(report.Status != "ok"))
}

type DoctorReport struct {
	SchemaVersion       int      `json:"schema_version"`
	Status              string   `json:"status"`
	Root                string   `json:"root"`
	Branch              string   `json:"branch,omitempty"`
	ConfigFile          string   `json:"config_file,omitempty"`
	PlaywrightConfig    string   `json:"playwright_config,omitempty"`
	PackageManager      string   `json:"package_manager,omitempty"`
	Runner              []string `json:"runner,omitempty"`
	AgentRunner         []string `json:"agent_runner,omitempty"`
	PlaywrightReady     bool     `json:"playwright_ready"`
	SessionReady        bool     `json:"session_ready"`
	PlaywrightVersion   string   `json:"playwright_version,omitempty"`
	AgentVersion        string   `json:"agent_cli_version,omitempty"`
	ArtifactRoot        string   `json:"artifact_root,omitempty"`
	ArtifactBytes       int64    `json:"artifact_bytes,omitempty"`
	ArtifactBudgetBytes int64    `json:"artifact_budget_bytes,omitempty"`
	ReclaimableBytes    int64    `json:"reclaimable_bytes,omitempty"`
	InterruptedRuns     int      `json:"interrupted_runs,omitempty"`
	Warnings            []string `json:"warnings,omitempty"`
	Error               string   `json:"error,omitempty"`
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
	capabilities := make([]string, 0, 2)
	if report.PlaywrightReady {
		capabilities = append(capabilities, "tests")
	}
	if report.SessionReady {
		capabilities = append(capabilities, "sessions")
	}
	fmt.Fprintf(out, "Heimdal doctor: ready (%s; %s)\n", report.Branch, strings.Join(capabilities, ", "))
	fmt.Fprintf(out, "  root: %s\n", report.Root)
	if report.PlaywrightReady {
		fmt.Fprintf(out, "  Playwright tests: %s\n", report.PlaywrightVersion)
	} else {
		fmt.Fprintln(out, "  Playwright tests: unavailable")
	}
	if report.SessionReady {
		fmt.Fprintf(out, "  Playwright sessions: %s\n", report.AgentVersion)
	} else {
		fmt.Fprintln(out, "  Playwright sessions: unavailable")
	}
	fmt.Fprintf(out, "  artifacts: %s\n", report.ArtifactRoot)
	if report.ArtifactBytes > 0 {
		fmt.Fprintf(out, "  artifact bytes: %d (%d reclaimable; %d interrupted runs)\n", report.ArtifactBytes, report.ReclaimableBytes, report.InterruptedRuns)
	}
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
	root, asJSON, fullJSON, runID, err := parseReportOptions(args)
	if err != nil {
		return reportError(asJSON, err, out, errOut)
	}
	project, err := Discover(root)
	if err != nil {
		return reportError(asJSON, err, out, errOut)
	}
	runDir, err := findReportRunDirectory(artifactRoot(project, ""), runID)
	if err != nil {
		return reportError(asJSON, err, out, errOut)
	}
	report, exitCode, err := readRunReportDetailed(runDir, !asJSON || fullJSON)
	if err != nil {
		return reportError(asJSON, err, out, errOut)
	}
	if asJSON {
		if !fullJSON {
			report = compactRunReport(report)
		}
		_ = writeJSONTo(out, report)
	} else {
		switch value := report.(type) {
		case RunResult:
			printResult(out, value)
			if len(value.Artifacts.Files) > 0 {
				fmt.Fprintln(out, "Files:")
				for _, file := range value.Artifacts.Files {
					fmt.Fprintf(out, "  %s\n", file)
				}
			}
		case RunManifest:
			fmt.Fprintf(out, "Result: %s\n", value.Status)
			fmt.Fprintf(out, "Artifacts: %s\n", value.Artifacts.RunDir)
			if len(value.Artifacts.Files) > 0 {
				fmt.Fprintln(out, "Files:")
				for _, file := range value.Artifacts.Files {
					fmt.Fprintf(out, "  %s\n", file)
				}
			}
		}
	}
	return exitCode
}

func runTrace(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseTraceOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if options.Help {
		fmt.Fprint(out, traceUsage)
		return 0
	}
	project, err := Discover(options.Root)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	tracePath, result, err := resolveTrace(project, options.RunID, options.Trace)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if options.JSON {
		summary, err := summarizeTrace(tracePath, result, options.AroundFailure)
		if err != nil {
			return reportError(true, err, out, errOut)
		}
		if err := writeJSONTo(out, summary); err != nil {
			return reportError(true, err, out, errOut)
		}
		return 0
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
		case "--dir", "--root":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i = next
			if err := setDirectoryOption(&options.Root, value, arg); err != nil {
				return options, err
			}
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
		case "--dir", "--root":
			flag := args[i]
			value, next, err := nextValue(args, i, flag)
			if err != nil {
				return root, asJSON, err
			}
			i = next
			if err := setDirectoryOption(&root, value, flag); err != nil {
				return root, asJSON, err
			}
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
		case "--dir", "--root":
			flag := args[i]
			value, next, err := nextValue(args, i, flag)
			if err != nil {
				return root, force, err
			}
			i = next
			if err := setDirectoryOption(&root, value, flag); err != nil {
				return root, force, err
			}
		case "--force":
			force = true
		default:
			return root, force, fmt.Errorf("unknown option %q", args[i])
		}
	}
	return root, force, nil
}

func parseReportOptions(args []string) (string, bool, bool, string, error) {
	root, asJSON, fullJSON, runID := "", false, false, ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir", "--root":
			flag := args[i]
			value, next, err := nextValue(args, i, flag)
			if err != nil {
				return root, asJSON, fullJSON, runID, err
			}
			i = next
			if err := setDirectoryOption(&root, value, flag); err != nil {
				return root, asJSON, fullJSON, runID, err
			}
		case "--json":
			asJSON = true
		case "--json=full":
			asJSON, fullJSON = true, true
		case "--run":
			value, next, nextErr := nextValue(args, i, "--run")
			if nextErr != nil {
				return root, asJSON, fullJSON, runID, nextErr
			}
			i = next
			runID = value
		case "--help", "-h":
			return root, asJSON, fullJSON, runID, errors.New(usage)
		default:
			return root, asJSON, fullJSON, runID, fmt.Errorf("unknown option %q", args[i])
		}
	}
	return root, asJSON, fullJSON, runID, nil
}

func parseSimpleOptionsWithoutUnknown(args []string) (string, bool, error) {
	root, asJSON := "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dir", "--root":
			flag := args[i]
			value, next, err := nextValue(args, i, flag)
			if err != nil {
				return root, asJSON, err
			}
			i = next
			if err := setDirectoryOption(&root, value, flag); err != nil {
				return root, asJSON, err
			}
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

func parseTraceOptions(args []string) (traceOptions, error) {
	options := traceOptions{AroundFailure: 2}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "inspect":
			if i != 0 || options.Inspect {
				return options, errors.New("trace inspect must be the first argument")
			}
			options.Inspect = true
			options.JSON = true
		case "--dir", "--root":
			flag := args[i]
			value, next, err := nextValue(args, i, flag)
			if err != nil {
				return options, err
			}
			i = next
			if err := setDirectoryOption(&options.Root, value, flag); err != nil {
				return options, err
			}
		case "--run":
			value, next, err := nextValue(args, i, "--run")
			if err != nil {
				return options, err
			}
			i = next
			options.RunID = value
		case "--json":
			options.JSON = true
		case "--around-failure":
			options.AroundFailure = 2
		case "--help", "-h":
			options.Help = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return options, fmt.Errorf("unknown option %q", args[i])
			}
			if options.Trace != "" {
				return options, errors.New("only one trace path may be provided")
			}
			options.Trace = args[i]
		}
	}
	if options.Trace != "" && options.RunID != "" {
		return options, errors.New("trace path and --run cannot be used together")
	}
	return options, nil
}

func parseRootAndForward(args []string) (string, []string, error) {
	root := ""
	var forwarded []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--dir" || args[i] == "--root" {
			flag := args[i]
			value, next, err := nextValue(args, i, flag)
			if err != nil {
				return root, forwarded, err
			}
			i = next
			if err := setDirectoryOption(&root, value, flag); err != nil {
				return root, forwarded, err
			}
			continue
		}
		forwarded = append(forwarded, args[i])
	}
	return root, forwarded, nil
}

func setDirectoryOption(current *string, value, flag string) error {
	if value == "" {
		return fmt.Errorf("%s requires a non-empty path", flag)
	}
	if *current != "" && filepath.Clean(*current) != filepath.Clean(value) {
		return fmt.Errorf("--dir and --root cannot specify different paths")
	}
	*current = value
	return nil
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
