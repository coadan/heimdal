package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	sessionStateVersion  = 1
	defaultSessionName   = "default"
	defaultServerWait    = 45 * time.Second
	defaultSnapshotDepth = 5
)

type SessionOptions struct {
	Root       string
	Name       string
	JSON       bool
	RunID      string
	Artifacts  string
	URL        string
	Port       int
	Headed     bool
	Persistent bool
	Profile    string
	Browser    string
	NoServer   bool
	Force      bool
	NoBoxes    bool
	Verbose    bool
	Timeout    time.Duration
	Forwarded  []string
}

type SessionState struct {
	SchemaVersion int        `json:"schema_version"`
	Name          string     `json:"name"`
	RunID         string     `json:"run_id"`
	Root          string     `json:"root"`
	Branch        string     `json:"branch"`
	SessionDir    string     `json:"session_dir"`
	CLIConfig     string     `json:"cli_config"`
	ActionLog     string     `json:"action_log"`
	URL           string     `json:"url,omitempty"`
	Port          int        `json:"port,omitempty"`
	ServerPID     int        `json:"server_pid,omitempty"`
	ServerCommand []string   `json:"server_command,omitempty"`
	ServerStdout  string     `json:"server_stdout,omitempty"`
	ServerStderr  string     `json:"server_stderr,omitempty"`
	ActionCount   int        `json:"action_count"`
	StartedAt     time.Time  `json:"started_at"`
	StoppedAt     *time.Time `json:"stopped_at,omitempty"`
}

type SessionActionRecord struct {
	Sequence   int       `json:"sequence"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Args       []string  `json:"args"`
	Locator    string    `json:"locator,omitempty"`
	Stdout     string    `json:"stdout,omitempty"`
	Stderr     string    `json:"stderr,omitempty"`
	StdoutFile string    `json:"stdout_file,omitempty"`
	StderrFile string    `json:"stderr_file,omitempty"`
	ExitCode   int       `json:"exit_code"`
}

type sessionCommandResult struct {
	Args      []string
	Stdout    string
	Stderr    string
	ExitCode  int
	Sequence  int
	StartedAt time.Time
}

type SessionResponse struct {
	SchemaVersion int               `json:"schema_version"`
	Status        string            `json:"status"`
	Session       string            `json:"session"`
	RunID         string            `json:"run_id,omitempty"`
	Root          string            `json:"root,omitempty"`
	URL           string            `json:"url,omitempty"`
	Port          int               `json:"port,omitempty"`
	Action        int               `json:"action,omitempty"`
	Command       []string          `json:"command,omitempty"`
	Output        string            `json:"output,omitempty"`
	Snapshot      string            `json:"snapshot,omitempty"`
	Stderr        string            `json:"stderr,omitempty"`
	Error         string            `json:"error,omitempty"`
	Issues        []string          `json:"issues,omitempty"`
	Server        string            `json:"server,omitempty"`
	Artifacts     map[string]string `json:"artifacts,omitempty"`
	State         *SessionState     `json:"state,omitempty"`
}

type sessionSaveOptions struct {
	SessionOptions
	TestPath string
}

type agentCLIConfig struct {
	Browser    *agentBrowserConfig `json:"browser,omitempty"`
	OutputDir  string              `json:"outputDir"`
	OutputMode string              `json:"outputMode"`
	Console    agentConsoleConfig  `json:"console"`
}

type agentBrowserConfig struct {
	BrowserName   string              `json:"browserName,omitempty"`
	UserDataDir   string              `json:"userDataDir,omitempty"`
	LaunchOptions *agentLaunchOptions `json:"launchOptions,omitempty"`
}

type agentLaunchOptions struct {
	Args    []string `json:"args,omitempty"`
	Channel string   `json:"channel,omitempty"`
}

type agentConsoleConfig struct {
	Level string `json:"level"`
}

func runSession(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, sessionUsage)
		return 0
	}

	switch args[0] {
	case "start":
		return runSessionStart(ctx, args[1:], out, errOut)
	case "stop":
		return runSessionStop(ctx, args[1:], out, errOut)
	case "status":
		return runSessionStatus(args[1:], out, errOut)
	case "observe":
		return runSessionAction(ctx, "snapshot", args[1:], out, errOut)
	case "screenshot":
		return runSessionAction(ctx, "screenshot", args[1:], out, errOut)
	case "diagnose":
		return runSessionDiagnose(ctx, args[1:], out, errOut)
	case "save":
		return runSessionSave(args[1:], out, errOut)
	default:
		return runSessionAction(ctx, args[0], args[1:], out, errOut)
	}
}

const sessionUsage = `Heimdal interactive Playwright sessions

Usage:
  heimdal session start [options]
  heimdal session stop [options]
  heimdal session status [options]
  heimdal session observe [options] [-- PLAYWRIGHT_CLI_ARGS...]
  heimdal session screenshot [options] [-- PLAYWRIGHT_CLI_ARGS...]
  heimdal session diagnose [options]
  heimdal session save [options]
  heimdal session <PLAYWRIGHT_CLI_COMMAND> [options]

Start options:
  --dir DIR        Locate the session from DIR (persisted root is canonicalized)
  --name NAME      Named persistent agent session (default: default)
  --url URL        URL to open, or session.url from .heimdal.json
  --run-id ID      Session artifact identity
  --artifacts DIR  Override the artifact root
  --port PORT      Fixture/server port
  --headed         Show the browser window
  --persistent     Persist browser profile between browser restarts
  --profile DIR    Persistent browser profile directory
  --browser NAME   Playwright browser engine/channel
  --no-server      Skip the configured session.command
  --no-boxes       Omit bounding boxes from snapshots
  --verbose        Include full Playwright CLI output; default output is compact
  --force          Replace existing Heimdal session state
  --json           Print only agent-readable JSON

Examples:
  heimdal session start --name void --headed
  heimdal session observe
  heimdal session click e12
  heimdal session fill e5 "hello"
  heimdal session diagnose --json
  heimdal session save --test tests/browser/exploration.spec.ts
`

func runSessionStart(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseSessionOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if len(options.Forwarded) > 0 {
		return reportError(options.JSON, errors.New("session start does not accept Playwright command arguments"), out, errOut)
	}
	project, err := Discover(options.Root)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	return startSession(ctx, project, options, out, errOut)
}

func startSession(ctx context.Context, project Project, options SessionOptions, out, errOut io.Writer) int {
	if len(project.AgentRunner) == 0 {
		return reportError(options.JSON, errors.New("playwright-cli is not configured; run `heimdal install agent-cli`"), out, errOut)
	}
	name := sanitize(options.Name)
	if name == "" {
		name = defaultSessionName
	}
	baseDir := filepath.Join(artifactRoot(project, options.Artifacts), "sessions", name)
	statePath := filepath.Join(baseDir, "session.json")
	if existing, err := readSessionState(statePath); err == nil && existing.StoppedAt == nil {
		if !options.Force {
			return reportError(options.JSON, fmt.Errorf("session %q is already active (use --force or `heimdal session status --name %s`)", name, name), out, errOut)
		}
		stopSessionResources(ctx, project, existing)
	}

	started := time.Now().UTC()
	runID := sanitize(options.RunID)
	if runID == "" {
		runID = fmt.Sprintf("%s-%s-%d", name, started.Format("20060102t150405.000000000z"), os.Getpid())
	}
	if runID == "" {
		return reportError(options.JSON, errors.New("session run id must contain a letter or number"), out, errOut)
	}
	runDir := filepath.Join(baseDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return reportError(options.JSON, fmt.Errorf("create session artifact directory: %w", err), out, errOut)
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return reportError(options.JSON, fmt.Errorf("create session state directory: %w", err), out, errOut)
	}

	url, port, serverCommand, planErr := sessionServerPlan(project, options, name, runID, runDir)
	if planErr != nil {
		return reportError(options.JSON, planErr, out, errOut)
	}
	state := SessionState{
		SchemaVersion: sessionStateVersion,
		Name:          name,
		RunID:         runID,
		Root:          project.Root,
		Branch:        project.Branch,
		SessionDir:    runDir,
		CLIConfig:     filepath.Join(runDir, "playwright-cli.json"),
		ActionLog:     filepath.Join(runDir, "actions.jsonl"),
		URL:           url,
		Port:          port,
		ServerCommand: serverCommand,
		StartedAt:     started,
	}
	if err := writeAgentCLIConfig(state.CLIConfig, options, project.Config.Session); err != nil {
		return reportError(options.JSON, err, out, errOut)
	}

	if len(serverCommand) > 0 && !options.NoServer {
		server, err := startSessionServer(project, state)
		if err != nil {
			return reportError(options.JSON, err, out, errOut)
		}
		state.ServerPID = server.PID
		state.ServerStdout = server.Stdout
		state.ServerStderr = server.Stderr
		if err := writeSessionState(statePath, state); err != nil {
			stopSessionServer(state.ServerPID)
			return reportError(options.JSON, err, out, errOut)
		}
		if state.URL != "" {
			wait := options.Timeout
			if wait == 0 && project.Config.Session.ServerTimeoutMS > 0 {
				wait = time.Duration(project.Config.Session.ServerTimeoutMS) * time.Millisecond
			}
			if wait == 0 {
				wait = defaultServerWait
			}
			if err := waitForSessionURL(ctx, state.URL, wait); err != nil {
				stopSessionServer(state.ServerPID)
				markSessionStopped(statePath, &state)
				return reportError(options.JSON, fmt.Errorf("session server did not become ready at %s: %w (logs: %s, %s)", state.URL, err, state.ServerStdout, state.ServerStderr), out, errOut)
			}
		}
	}
	if err := writeSessionState(statePath, state); err != nil {
		stopSessionResources(ctx, project, state)
		return reportError(options.JSON, err, out, errOut)
	}
	if err := writeSessionIndex(state); err != nil {
		stopSessionResources(ctx, project, state)
		markSessionStopped(statePath, &state)
		return reportError(options.JSON, fmt.Errorf("persist session root lookup: %w", err), out, errOut)
	}

	openArgs := []string{"open"}
	if state.URL != "" {
		openArgs = append(openArgs, state.URL)
	}
	if options.Headed {
		openArgs = append(openArgs, "--headed")
	}
	if options.Persistent || options.Profile != "" {
		openArgs = append(openArgs, "--persistent")
	}
	if options.Profile != "" {
		openArgs = append(openArgs, "--profile="+absoluteFromRoot(project.Root, options.Profile))
	}
	open, openErr := runSessionCommandMode(ctx, project, &state, statePath, openArgs, "", !options.Verbose)
	if openErr != nil {
		stopSessionResources(ctx, project, state)
		markSessionStopped(statePath, &state)
		response := sessionResponse(state, open, openErr)
		if !options.Verbose {
			response.Output = compactSessionCommand(open, "")
			response.Stderr = compactCLIOutput(open.Stderr)
		}
		return printSessionResponse(out, errOut, response, options.JSON)
	}

	observeArgs := sessionSnapshotArgs(options.NoBoxes, options.Verbose, nil)
	observe, observeErr := runSessionCommandMode(ctx, project, &state, statePath, observeArgs, "", !options.Verbose)
	if observeErr != nil {
		stopSessionResources(ctx, project, state)
		markSessionStopped(statePath, &state)
	}
	response := sessionResponse(state, observe, observeErr)
	response.Status = "started"
	if options.Verbose {
		response.Output = joinOutputs(open.Stdout, observe.Stdout)
	} else {
		response.Output = fmt.Sprintf("opened %s", state.URL)
		response.Snapshot = observe.Stdout
	}
	if observeErr != nil {
		response.Status = "failed"
	}
	return printSessionResponse(out, errOut, response, options.JSON)
}

func runSessionStop(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseSessionOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if len(options.Forwarded) > 0 {
		return reportError(options.JSON, errors.New("session stop does not accept Playwright command arguments"), out, errOut)
	}
	project, state, statePath, err := discoverSession(options)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if state.StoppedAt != nil {
		response := sessionResponse(state, sessionCommandResult{}, nil)
		response.Status = "stopped"
		return printSessionResponse(out, errOut, response, options.JSON)
	}
	closeResult, closeErr := runSessionCommand(ctx, project, &state, statePath, []string{"close"}, "")
	stopSessionServer(state.ServerPID)
	stopped := time.Now().UTC()
	state.StoppedAt = &stopped
	stateWriteErr := writeSessionState(statePath, state)
	indexWriteErr := writeSessionIndex(state)
	response := sessionResponse(state, closeResult, closeErr)
	response.Status = "stopped"
	if !options.Verbose {
		response.Output = compactSessionCommand(closeResult, "")
		response.Stderr = compactCLIOutput(closeResult.Stderr)
	}
	if closeErr != nil {
		response.Status = "failed"
	} else if stateWriteErr != nil {
		response.Status = "failed"
		response.Error = stateWriteErr.Error()
	} else if indexWriteErr != nil {
		response.Status = "failed"
		response.Error = indexWriteErr.Error()
	}
	return printSessionResponse(out, errOut, response, options.JSON)
}

func runSessionStatus(args []string, out, errOut io.Writer) int {
	options, err := parseSessionOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if len(options.Forwarded) > 0 {
		return reportError(options.JSON, errors.New("session status does not accept Playwright command arguments"), out, errOut)
	}
	_, state, _, err := discoverSession(options)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	response := sessionResponse(state, sessionCommandResult{}, nil)
	if state.StoppedAt == nil {
		if response.Server == "stopped" {
			response.Status = "issues"
		} else {
			response.Status = "active"
		}
	} else {
		response.Status = "stopped"
	}
	response.State = &state
	return printSessionResponse(out, errOut, response, options.JSON)
}

func runSessionAction(ctx context.Context, action string, args []string, out, errOut io.Writer) int {
	options, err := parseSessionOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	project, state, statePath, err := discoverSession(options)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if state.StoppedAt != nil {
		return reportError(options.JSON, fmt.Errorf("session %q is stopped", state.Name), out, errOut)
	}

	logicalArgs := append([]string{action}, options.Forwarded...)
	if action == "snapshot" {
		logicalArgs = sessionSnapshotArgs(options.NoBoxes, options.Verbose, options.Forwarded)
	}
	locator := ""
	if isLocatorAction(action, logicalArgs) {
		locator = generateSessionLocator(ctx, project, state, logicalArgs[1])
	}
	result, commandErr := runSessionCommandMode(ctx, project, &state, statePath, logicalArgs, locator, !options.Verbose)
	output := result.Stdout
	stderr := result.Stderr
	var snapshot string
	if commandErr == nil && shouldObserveAfterSessionAction(action) {
		observationArgs := sessionSnapshotArgs(options.NoBoxes, options.Verbose, nil)
		observation, observationErr := runSessionCommandMode(ctx, project, &state, statePath, observationArgs, "", !options.Verbose)
		if options.Verbose {
			output = joinOutputs(output, observation.Stdout)
		} else {
			snapshot = observation.Stdout
		}
		stderr = joinOutputs(stderr, observation.Stderr)
		if observationErr != nil {
			commandErr = fmt.Errorf("post-action observation failed: %w", observationErr)
		}
	}
	response := sessionResponse(state, result, commandErr)
	if options.Verbose {
		response.Output = output
		response.Stderr = stderr
	} else {
		response.Command = compactSessionArgs(result.Args)
		response.Output = compactSessionCommand(result, locator)
		response.Snapshot = snapshot
		response.Stderr = compactCLIOutput(stderr)
	}
	if action == "snapshot" && !options.Verbose {
		response.Output = "observed"
		response.Snapshot = result.Stdout
	}
	if commandErr != nil {
		response.Status = "failed"
	} else if response.Status != "issues" {
		response.Status = "passed"
	}
	return printSessionResponse(out, errOut, response, options.JSON)
}

func runSessionDiagnose(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseSessionOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if len(options.Forwarded) > 0 {
		return reportError(options.JSON, errors.New("session diagnose does not accept Playwright command arguments"), out, errOut)
	}
	project, state, statePath, err := discoverSession(options)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if state.StoppedAt != nil {
		return reportError(options.JSON, fmt.Errorf("session %q is stopped", state.Name), out, errOut)
	}

	commands := [][]string{{"console", "error"}, {"requests"}, sessionSnapshotArgs(options.NoBoxes, options.Verbose, nil)}
	var output []string
	var snapshot string
	var last sessionCommandResult
	var firstErr error
	var diagnosticOutput []string
	for _, command := range commands {
		result, commandErr := runSessionCommandMode(ctx, project, &state, statePath, command, "", !options.Verbose)
		last = result
		if commandErr != nil && firstErr == nil {
			firstErr = commandErr
		}
		if command[0] == "snapshot" && !options.Verbose {
			snapshot = result.Stdout
			continue
		}
		diagnosticOutput = append(diagnosticOutput, result.Stdout, result.Stderr)
		if options.Verbose {
			output = append(output, fmt.Sprintf("$ %s\n%s", strings.Join(command, " "), joinOutputs(result.Stdout, result.Stderr)))
		} else {
			output = append(output, compactDiagnostic(command, result))
		}
	}
	response := sessionResponse(state, last, firstErr)
	response.Command = []string{"diagnose"}
	response.Output = strings.TrimSpace(strings.Join(output, "\n\n"))
	response.Snapshot = snapshot
	response.Issues = append(response.Issues, diagnosticIssues(diagnosticOutput...)...)
	if firstErr != nil {
		response.Status = "failed"
	} else if len(response.Issues) > 0 {
		response.Status = "issues"
	} else {
		response.Status = "passed"
	}
	return printSessionResponse(out, errOut, response, options.JSON)
}

func runSessionSave(args []string, out, errOut io.Writer) int {
	options, err := parseSessionSaveOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	project, state, _, err := discoverSession(options.SessionOptions)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	actions, err := readSessionActions(state.ActionLog)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	markdownPath := filepath.Join(state.SessionDir, "session.md")
	if err := os.WriteFile(markdownPath, []byte(sessionMarkdown(state, actions)), 0o644); err != nil {
		return reportError(options.JSON, fmt.Errorf("write session transcript: %w", err), out, errOut)
	}
	response := sessionResponse(state, sessionCommandResult{}, nil)
	response.Status = "saved"
	response.Artifacts["transcript"] = markdownPath
	if options.TestPath != "" {
		testPath := options.TestPath
		if !filepath.IsAbs(testPath) {
			testPath = filepath.Join(project.Root, testPath)
		}
		if err := os.MkdirAll(filepath.Dir(testPath), 0o755); err != nil {
			return reportError(options.JSON, fmt.Errorf("create generated test directory: %w", err), out, errOut)
		}
		if err := os.WriteFile(testPath, []byte(sessionTest(state, actions)), 0o644); err != nil {
			return reportError(options.JSON, fmt.Errorf("write generated test: %w", err), out, errOut)
		}
		response.Artifacts["test"] = testPath
	}
	return printSessionResponse(out, errOut, response, options.JSON)
}

func parseSessionOptions(args []string) (SessionOptions, error) {
	options := SessionOptions{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			options.Forwarded = append(options.Forwarded, args[i+1:]...)
			break
		}
		switch arg {
		case "--dir", "--root":
			value, next, err := nextDirectoryValue(args, i, options.Root)
			if err != nil {
				return options, err
			}
			i, options.Root = next, value
		case "--name", "--session":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i, options.Name = next, value
		case "--run-id":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i, options.RunID = next, value
		case "--artifacts":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i, options.Artifacts = next, value
		case "--url":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i, options.URL = next, value
		case "--port":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			port, parseErr := strconv.Atoi(value)
			if parseErr != nil || port < 1 || port > 65535 {
				return options, fmt.Errorf("--port must be between 1 and 65535 (got %q)", value)
			}
			i, options.Port = next, port
		case "--headed":
			options.Headed = true
		case "--persistent":
			options.Persistent = true
		case "--profile":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i, options.Profile = next, value
		case "--browser":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			i, options.Browser = next, value
		case "--no-server":
			options.NoServer = true
		case "--force":
			options.Force = true
		case "--no-boxes":
			options.NoBoxes = true
		case "--verbose":
			options.Verbose = true
		case "--timeout-ms":
			value, next, err := nextValue(args, i, arg)
			if err != nil {
				return options, err
			}
			milliseconds, parseErr := strconv.Atoi(value)
			if parseErr != nil || milliseconds < 1 {
				return options, fmt.Errorf("--timeout-ms must be a positive integer (got %q)", value)
			}
			i, options.Timeout = next, time.Duration(milliseconds)*time.Millisecond
		case "--json":
			options.JSON = true
		case "--help", "-h":
			return options, errors.New(sessionUsage)
		default:
			options.Forwarded = append(options.Forwarded, arg)
		}
	}
	return options, nil
}

func parseSessionSaveOptions(args []string) (sessionSaveOptions, error) {
	var common []string
	options := sessionSaveOptions{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--test" {
			value, next, err := nextValue(args, i, "--test")
			if err != nil {
				return options, err
			}
			i = next
			options.TestPath = value
			continue
		}
		common = append(common, args[i])
	}
	parsed, err := parseSessionOptions(common)
	options.SessionOptions = parsed
	return options, err
}

func sessionServerPlan(project Project, options SessionOptions, name, runID, runDir string) (string, int, []string, error) {
	configuredURL := options.URL
	if configuredURL == "" {
		configuredURL = project.Config.Session.URL
	}
	needsPort := options.Port > 0 || len(project.Config.Session.Command) > 0 || strings.Contains(configuredURL, "${PORT}")
	port := options.Port
	if port == 0 && needsPort {
		var err error
		port, err = freePort()
		if err != nil {
			return "", 0, nil, err
		}
	}
	values := sessionTemplateValues(project, name, runID, runDir, port, configuredURL)
	url := os.Expand(configuredURL, func(key string) string { return values[key] })
	if url == "" && len(project.Config.Session.Command) > 0 && port > 0 {
		url = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	values["URL"] = url
	var command []string
	for _, part := range project.Config.Session.Command {
		command = append(command, os.Expand(part, func(key string) string { return values[key] }))
	}
	return url, port, command, nil
}

func sessionSnapshotArgs(noBoxes, verbose bool, forwarded []string) []string {
	args := []string{"snapshot"}
	args = append(args, forwarded...)
	if !noBoxes {
		if !containsFlag(forwarded, "--boxes") {
			args = append(args, "--boxes")
		}
	}
	if !verbose && !containsFlag(forwarded, "--depth") {
		args = append(args, fmt.Sprintf("--depth=%d", defaultSnapshotDepth))
	}
	return args
}

func sessionTemplateValues(project Project, name, runID, runDir string, port int, configuredURL string) map[string]string {
	return map[string]string{
		"RUN_ID":       runID,
		"RUN_DIR":      runDir,
		"ARTIFACT_DIR": runDir,
		"OUTPUT_DIR":   filepath.Join(runDir, "test-results"),
		"REPORT_DIR":   filepath.Join(runDir, "report"),
		"ROOT":         project.Root,
		"BRANCH":       project.Branch,
		"PORT":         strconv.Itoa(port),
		"SESSION":      name,
		"URL":          configuredURL,
	}
}

func writeAgentCLIConfig(path string, options SessionOptions, configured SessionConfig) error {
	browserName := options.Browser
	if browserName == "" {
		browserName = configured.Browser
	}
	if browserName == "" {
		browserName = "chromium"
	}
	config := agentCLIConfig{
		OutputDir:  filepath.Dir(path),
		OutputMode: "file",
		Console:    agentConsoleConfig{Level: "info"},
	}
	if browserName != "" || options.Profile != "" {
		config.Browser = &agentBrowserConfig{BrowserName: browserName}
		if options.Profile != "" {
			config.Browser.UserDataDir = options.Profile
		}
		if len(configured.BrowserLaunchOptions.Args) > 0 || configured.BrowserLaunchOptions.Channel != "" {
			config.Browser.LaunchOptions = &agentLaunchOptions{
				Args:    append([]string(nil), configured.BrowserLaunchOptions.Args...),
				Channel: configured.BrowserLaunchOptions.Channel,
			}
		}
	}
	contents, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Playwright CLI config: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return fmt.Errorf("write Playwright CLI config: %w", err)
	}
	return nil
}

type sessionServer struct {
	PID    int
	Stdout string
	Stderr string
}

func startSessionServer(project Project, state SessionState) (sessionServer, error) {
	if len(state.ServerCommand) == 0 {
		return sessionServer{}, errors.New("session server command is empty")
	}
	stdoutPath := filepath.Join(state.SessionDir, "server.stdout.log")
	stderrPath := filepath.Join(state.SessionDir, "server.stderr.log")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return sessionServer{}, fmt.Errorf("create session server stdout: %w", err)
	}
	defer stdoutFile.Close()
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return sessionServer{}, fmt.Errorf("create session server stderr: %w", err)
	}
	defer stderrFile.Close()

	cmd := exec.Command(state.ServerCommand[0], state.ServerCommand[1:]...)
	cmd.Dir = project.Root
	cmd.Env = sessionEnvironment(project, state)
	configureDetachedProcess(cmd)
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	if err := cmd.Start(); err != nil {
		return sessionServer{}, fmt.Errorf("start session server %s: %w", commandString(state.ServerCommand), err)
	}
	return sessionServer{PID: cmd.Process.Pid, Stdout: stdoutPath, Stderr: stderrPath}, nil
}

func waitForSessionURL(parent context.Context, url string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_ = response.Body.Close()
			return nil
		}
		lastErr = requestErr
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return lastErr
			}
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func runSessionCommand(ctx context.Context, project Project, state *SessionState, statePath string, logicalArgs []string, locator string) (sessionCommandResult, error) {
	return runSessionCommandMode(ctx, project, state, statePath, logicalArgs, locator, false)
}

func runSessionCommandMode(ctx context.Context, project Project, state *SessionState, statePath string, logicalArgs []string, locator string, raw bool) (sessionCommandResult, error) {
	started := time.Now().UTC()
	command := agentSessionCommand(project, *state, logicalArgs, raw)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = project.Root
	cmd.Env = sessionEnvironment(project, *state)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	finished := time.Now().UTC()
	exitCode := 0
	if err != nil {
		exitCode = processExitCode(err)
		if exitCode == 0 {
			exitCode = 1
		}
	}
	state.ActionCount++
	sequence := state.ActionCount
	prefix := fmt.Sprintf("action-%04d", sequence)
	stdoutPath := filepath.Join(state.SessionDir, prefix+".stdout.log")
	stderrPath := filepath.Join(state.SessionDir, prefix+".stderr.log")
	if writeErr := os.WriteFile(stdoutPath, stdout.Bytes(), 0o644); writeErr != nil && err == nil {
		err = fmt.Errorf("write session action stdout: %w", writeErr)
	}
	if writeErr := os.WriteFile(stderrPath, stderr.Bytes(), 0o644); writeErr != nil && err == nil {
		err = fmt.Errorf("write session action stderr: %w", writeErr)
	}
	record := SessionActionRecord{
		Sequence:   sequence,
		StartedAt:  started,
		FinishedAt: finished,
		Args:       append([]string(nil), logicalArgs...),
		Locator:    locator,
		Stdout:     truncateOutput(stdout.String()),
		Stderr:     truncateOutput(stderr.String()),
		StdoutFile: stdoutPath,
		StderrFile: stderrPath,
		ExitCode:   exitCode,
	}
	if appendErr := appendSessionAction(state.ActionLog, record); appendErr != nil && err == nil {
		err = appendErr
	}
	if writeErr := writeSessionState(statePath, *state); writeErr != nil && err == nil {
		err = writeErr
	}
	if err != nil && !strings.Contains(err.Error(), "playwright-cli") && len(command) > 0 {
		if exitCode != 0 {
			err = fmt.Errorf("Playwright CLI command failed (%s): %w", commandString(command), err)
		}
	}
	if exitCode != 0 && err == nil {
		err = fmt.Errorf("Playwright CLI exited with code %d", exitCode)
	}
	return sessionCommandResult{Args: logicalArgs, Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: exitCode, Sequence: sequence, StartedAt: started}, err
}

func generateSessionLocator(ctx context.Context, project Project, state SessionState, target string) string {
	if target == "" {
		return ""
	}
	command := append([]string(nil), project.AgentRunner...)
	command = append(command, "--raw", "-s="+state.Name, "generate-locator", target)
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = project.Root
	cmd.Env = sessionEnvironment(project, state)
	output, err := cmd.Output()
	if err != nil {
		return ""
	}
	return normalizeGeneratedLocator(string(output))
}

func normalizeGeneratedLocator(output string) string {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if index := strings.Index(line, "page."); index >= 0 {
			return strings.TrimSpace(line[index:])
		}
		if strings.HasPrefix(line, "getBy") {
			return "page." + line
		}
	}
	return ""
}

func agentSessionCommand(project Project, state SessionState, args []string, raw bool) []string {
	command := append([]string(nil), project.AgentRunner...)
	if raw {
		command = append(command, "--raw")
	}
	if len(args) > 0 && args[0] == "open" {
		command = append(command, "--config="+state.CLIConfig)
	}
	command = append(command, "-s="+state.Name)
	return append(command, args...)
}

func sessionEnvironment(project Project, state SessionState) []string {
	envMap := make(map[string]string)
	for _, entry := range runEnvironment(project, state.RunID, state.SessionDir, filepath.Join(state.SessionDir, "test-results"), filepath.Join(state.SessionDir, "report"), state.Port) {
		key, value, found := strings.Cut(entry, "=")
		if found {
			envMap[key] = value
		}
	}
	values := sessionTemplateValues(project, state.Name, state.RunID, state.SessionDir, state.Port, state.URL)
	setEnv := func(key, value string) {
		if key != "" {
			envMap[key] = value
		}
	}
	setEnv("HEIMDAL_SESSION_NAME", state.Name)
	setEnv("HEIMDAL_SESSION_DIR", state.SessionDir)
	if project.Config.Session.RunIDEnv != "" {
		setEnv(project.Config.Session.RunIDEnv, state.RunID)
	}
	if state.Port > 0 && project.Config.Session.PortEnv != "" {
		setEnv(project.Config.Session.PortEnv, strconv.Itoa(state.Port))
	}
	values["URL"] = state.URL
	for key, value := range project.Config.Session.Env {
		setEnv(key, os.Expand(value, func(name string) string {
			if resolved, ok := values[name]; ok {
				return resolved
			}
			return os.Getenv(name)
		}))
	}
	keys := make([]string, 0, len(envMap))
	for key := range envMap {
		keys = append(keys, key)
	}
	// Reuse the stable ordering rule from runEnvironment by sorting here.
	sortStrings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+envMap[key])
	}
	return env
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

func loadSession(project Project, options SessionOptions) (SessionState, string, error) {
	name := sanitize(options.Name)
	if name == "" {
		name = defaultSessionName
	}
	path := filepath.Join(artifactRoot(project, options.Artifacts), "sessions", name, "session.json")
	state, err := readSessionState(path)
	if err != nil {
		return SessionState{}, path, fmt.Errorf("read session %q: %w (run `heimdal session start --name %s` first)", name, err, name)
	}
	return state, path, nil
}

func readSessionState(path string) (SessionState, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return SessionState{}, err
	}
	var state SessionState
	if err := json.Unmarshal(contents, &state); err != nil {
		return SessionState{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return state, nil
}

func writeSessionState(path string, state SessionState) error {
	contents, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode session state: %w", err)
	}
	contents = append(contents, '\n')
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		return fmt.Errorf("write session state: %w", err)
	}
	return nil
}

func appendSessionAction(path string, record SessionActionRecord) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open session action log: %w", err)
	}
	defer file.Close()
	contents, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode session action: %w", err)
	}
	contents = append(contents, '\n')
	if _, err := file.Write(contents); err != nil {
		return fmt.Errorf("write session action log: %w", err)
	}
	return nil
}

func readSessionActions(path string) ([]SessionActionRecord, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open session action log: %w", err)
	}
	defer file.Close()
	var actions []SessionActionRecord
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var action SessionActionRecord
		if err := json.Unmarshal(scanner.Bytes(), &action); err != nil {
			return nil, fmt.Errorf("parse session action: %w", err)
		}
		actions = append(actions, action)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read session action log: %w", err)
	}
	return actions, nil
}

func stopSessionResources(ctx context.Context, project Project, state SessionState) {
	if state.StoppedAt == nil && len(project.AgentRunner) > 0 {
		command := agentSessionCommand(project, state, []string{"close"}, false)
		cmd := exec.CommandContext(ctx, command[0], command[1:]...)
		cmd.Dir = project.Root
		cmd.Env = sessionEnvironment(project, state)
		_ = cmd.Run()
	}
	stopSessionServer(state.ServerPID)
}

func markSessionStopped(path string, state *SessionState) {
	stopped := time.Now().UTC()
	state.StoppedAt = &stopped
	_ = writeSessionState(path, *state)
	_ = writeSessionIndex(*state)
}

func stopSessionServer(pid int) {
	stopDetachedProcess(pid)
}

func sessionResponse(state SessionState, result sessionCommandResult, commandErr error) SessionResponse {
	response := SessionResponse{
		SchemaVersion: 1,
		Status:        "passed",
		Session:       state.Name,
		RunID:         state.RunID,
		Root:          state.Root,
		URL:           state.URL,
		Port:          state.Port,
		Action:        result.Sequence,
		Command:       result.Args,
		Output:        result.Stdout,
		Stderr:        result.Stderr,
		Server:        sessionServerStatus(state),
		Artifacts: map[string]string{
			"directory":  state.SessionDir,
			"state":      filepath.Join(filepath.Dir(state.SessionDir), "session.json"),
			"cli_config": state.CLIConfig,
			"actions":    state.ActionLog,
		},
	}
	if commandErr != nil {
		response.Status = "failed"
		response.Error = commandErr.Error()
	} else if response.Server == "stopped" && state.StoppedAt == nil {
		response.Status = "issues"
		response.Issues = append(response.Issues, "fixture process is not running")
	}
	return response
}

func sessionServerStatus(state SessionState) string {
	if state.ServerPID <= 0 {
		return ""
	}
	if state.StoppedAt != nil {
		return "stopped"
	}
	process, err := os.FindProcess(state.ServerPID)
	if err != nil {
		return "stopped"
	}
	if err := process.Signal(syscall.Signal(0)); err != nil {
		return "stopped"
	}
	return "running"
}

func compactSessionCommand(result sessionCommandResult, locator string) string {
	if len(result.Args) == 0 {
		return ""
	}
	command := result.Args[0]
	switch command {
	case "open":
		if len(result.Args) > 1 {
			return "opened " + result.Args[1]
		}
		return "opened"
	case "close":
		return "closed"
	case "screenshot":
		if line := firstOutputLine(result.Stdout, "Screenshot"); line != "" {
			return line
		}
		if output := compactCLIOutput(joinOutputs(result.Stdout, result.Stderr)); output != "" {
			return output
		}
		return "screenshot saved"
	case "snapshot":
		return "observed"
	}
	args := compactSessionArgs(result.Args)
	output := commandString(args)
	if locator != "" {
		output += "\nlocator: " + locator
	}
	if !shouldObserveAfterSessionAction(command) {
		if detail := compactCLIOutput(joinOutputs(result.Stdout, result.Stderr)); detail != "" {
			output += "\n" + detail
		}
	}
	return output
}

func compactSessionArgs(args []string) []string {
	compact := append([]string(nil), args...)
	if len(compact) == 0 {
		return compact
	}
	switch compact[0] {
	case "fill":
		if len(compact) > 2 {
			compact[2] = fmt.Sprintf("<text:%d chars>", len(compact[2]))
		}
	case "type", "upload":
		if len(compact) > 1 {
			compact[1] = fmt.Sprintf("<text:%d chars>", len(compact[1]))
		}
	}
	return compact
}

func firstOutputLine(output, contains string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, contains) {
			return line
		}
	}
	return ""
}

func compactCLIOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	if start := strings.Index(output, "### Result"); start >= 0 {
		output = strings.TrimSpace(output[start+len("### Result"):])
	}
	if end := strings.Index(output, "\n### Ran Playwright code"); end >= 0 {
		output = strings.TrimSpace(output[:end])
	}
	return truncateDisplay(output, 6000)
}

func compactDiagnostic(command []string, result sessionCommandResult) string {
	label := strings.Join(command, " ")
	output := compactCLIOutput(joinOutputs(result.Stdout, result.Stderr))
	if output == "" {
		return label + ": none"
	}
	return label + ":\n" + output
}

func diagnosticIssues(outputs ...string) []string {
	var issues []string
	for _, output := range outputs {
		if count := parseDiagnosticCount(output, "Errors:"); count > 0 {
			issues = appendUnique(issues, fmt.Sprintf("console errors: %d", count))
		}
		if strings.Contains(output, "[FAILED]") || strings.Contains(output, "net::ERR_") {
			issues = appendUnique(issues, "failed network requests detected")
		}
	}
	return issues
}

func parseDiagnosticCount(output, marker string) int {
	index := strings.Index(output, marker)
	if index < 0 {
		return 0
	}
	value := strings.TrimLeft(output[index+len(marker):], " \t")
	end := 0
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	count, err := strconv.Atoi(value[:end])
	if err != nil {
		return 0
	}
	return count
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func truncateDisplay(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max] + "\n… output truncated; see session artifacts"
}

func printSessionResponse(out, errOut io.Writer, response SessionResponse, asJSON bool) int {
	if asJSON {
		if err := writeJSONTo(out, response); err != nil {
			fmt.Fprintln(errOut, "heimdal:", err)
			return 1
		}
		if response.Status == "failed" || response.Status == "issues" {
			return 1
		}
		return 0
	}
	if response.Output != "" {
		fmt.Fprint(out, response.Output)
		if !strings.HasSuffix(response.Output, "\n") {
			fmt.Fprintln(out)
		}
	}
	if response.Snapshot != "" {
		fmt.Fprintln(out, "Snapshot:")
		fmt.Fprintln(out, response.Snapshot)
	}
	if response.Stderr != "" {
		fmt.Fprint(errOut, response.Stderr)
		if !strings.HasSuffix(response.Stderr, "\n") {
			fmt.Fprintln(errOut)
		}
	}
	fmt.Fprintf(out, "Heimdal session %s: %s\n", response.Session, response.Status)
	if response.Action > 0 {
		fmt.Fprintf(out, "  action: %d\n", response.Action)
	}
	if response.URL != "" {
		fmt.Fprintf(out, "  url: %s\n", response.URL)
	}
	fmt.Fprintf(out, "  artifacts: %s\n", response.Artifacts["directory"])
	if response.Server != "" {
		fmt.Fprintf(out, "  server: %s\n", response.Server)
	}
	for _, issue := range response.Issues {
		fmt.Fprintf(errOut, "heimdal: issue: %s\n", issue)
	}
	if response.Error != "" {
		fmt.Fprintf(errOut, "heimdal: %s\n", response.Error)
		return 1
	}
	if response.Status == "issues" {
		return 1
	}
	return 0
}

func sessionMarkdown(state SessionState, actions []SessionActionRecord) string {
	var output strings.Builder
	fmt.Fprintf(&output, "# Heimdal session `%s`\n\n", state.Name)
	fmt.Fprintf(&output, "- URL: `%s`\n- Run ID: `%s`\n- Worktree: `%s`\n\n", state.URL, state.RunID, state.Root)
	output.WriteString("## Actions\n\n")
	for _, action := range actions {
		fmt.Fprintf(&output, "%d. `%s`", action.Sequence, strings.Join(action.Args, " "))
		if action.Locator != "" {
			fmt.Fprintf(&output, "\n   locator: `%s`", action.Locator)
		}
		if action.ExitCode != 0 {
			fmt.Fprintf(&output, "\n   exit code: `%d`", action.ExitCode)
		}
		fmt.Fprintf(&output, "\n   stdout: `%s`\n\n", action.StdoutFile)
	}
	return output.String()
}

func sessionTest(state SessionState, actions []SessionActionRecord) string {
	var output strings.Builder
	output.WriteString("import { test } from '@playwright/test';\n\n")
	fmt.Fprintf(&output, "test(%s, async ({ page }) => {\n", quoteTypeScript("recorded Heimdal session "+state.Name))
	if state.URL != "" {
		fmt.Fprintf(&output, "  await page.goto(%s);\n", quoteTypeScript(state.URL))
	}
	for _, action := range actions {
		for _, line := range sessionActionTestLines(action) {
			fmt.Fprintf(&output, "  %s\n", line)
		}
	}
	output.WriteString("});\n")
	return output.String()
}

func sessionActionTestLines(action SessionActionRecord) []string {
	if len(action.Args) == 0 || action.ExitCode != 0 {
		return nil
	}
	command := action.Args[0]
	target := ""
	if len(action.Args) > 1 {
		target = action.Args[1]
	}
	locator := action.Locator
	if locator == "" {
		locator = fallbackLocator(target)
	}
	quoted := func(index int) string {
		if len(action.Args) <= index {
			return "\"\""
		}
		return quoteTypeScript(action.Args[index])
	}
	switch command {
	case "open", "snapshot", "screenshot", "console", "requests", "highlight", "find", "tab-list", "request", "request-headers", "request-body", "response-headers", "response-body", "cookie-list", "cookie-get", "localstorage-list", "localstorage-get", "sessionstorage-list", "sessionstorage-get":
		return nil
	case "goto":
		return []string{"await page.goto(" + quoted(1) + ");"}
	case "reload":
		return []string{"await page.reload();"}
	case "go-back":
		return []string{"await page.goBack();"}
	case "go-forward":
		return []string{"await page.goForward();"}
	case "click":
		return locatorAction(locator, "click")
	case "dblclick":
		return locatorAction(locator, "dblclick")
	case "hover":
		return locatorAction(locator, "hover")
	case "check":
		return locatorAction(locator, "check")
	case "uncheck":
		return locatorAction(locator, "uncheck")
	case "fill":
		return []string{"await " + locator + ".fill(" + quoted(2) + ");"}
	case "select":
		return []string{"await " + locator + ".selectOption(" + quoted(2) + ");"}
	case "type":
		return []string{"await page.keyboard.type(" + quoted(1) + ");"}
	case "press":
		return []string{"await page.keyboard.press(" + quoted(1) + ");"}
	case "resize":
		if len(action.Args) < 3 {
			return nil
		}
		return []string{fmt.Sprintf("await page.setViewportSize({ width: %s, height: %s });", action.Args[1], action.Args[2])}
	default:
		return []string{"// Heimdal action: " + strings.Join(action.Args, " ")}
	}
}

func shouldObserveAfterSessionAction(action string) bool {
	switch action {
	case "click", "dblclick", "drag", "fill", "select", "check", "uncheck", "hover", "press", "type", "tap", "focus", "goto", "reload", "go-back", "go-forward", "resize", "set-input-files":
		return true
	default:
		return false
	}
}

func locatorAction(locator, method string) []string {
	if locator == "" {
		return []string{"// TODO: replace the recorded element ref with a semantic locator"}
	}
	return []string{"await " + locator + "." + method + "();"}
}

func fallbackLocator(target string) string {
	if strings.HasPrefix(target, "page.") {
		return target
	}
	if strings.HasPrefix(target, "getBy") {
		return "page." + target
	}
	if target == "" {
		return ""
	}
	return "page.locator(" + quoteTypeScript(target) + ")"
}

func quoteTypeScript(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return strconv.Quote(value)
	}
	return string(encoded)
}

func isLocatorAction(action string, args []string) bool {
	if len(args) < 2 {
		return false
	}
	switch action {
	case "click", "dblclick", "fill", "select", "check", "uncheck", "hover", "highlight", "screenshot":
		return strings.HasPrefix(args[1], "e")
	default:
		return false
	}
}

func absoluteFromRoot(root, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(root, value)
}

func joinOutputs(values ...string) string {
	var nonEmpty []string
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			nonEmpty = append(nonEmpty, strings.TrimSpace(value))
		}
	}
	return strings.Join(nonEmpty, "\n")
}

func truncateOutput(value string) string {
	const max = 64 * 1024
	if len(value) <= max {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(value[:max]) + "\n… output truncated; see the action log file"
}

func installAgentCLI(ctx context.Context, project Project, out, errOut io.Writer) int {
	var command []string
	switch project.PackageManager {
	case "pnpm":
		command = []string{"pnpm", "add", "--save-dev", "@playwright/cli@latest"}
	case "yarn":
		command = []string{"yarn", "add", "--dev", "@playwright/cli@latest"}
	case "bun":
		command = []string{"bun", "add", "--dev", "@playwright/cli@latest"}
	default:
		command = []string{"npm", "install", "--save-dev", "@playwright/cli@latest"}
	}
	fmt.Fprintf(out, "%s\n", commandString(command))
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = project.Root
	cmd.Env = baseEnvironment()
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return normalizeExitCode(processExitCode(err))
	}
	return 0
}

func installAgentBrowser(ctx context.Context, project Project, browser string, out, errOut io.Writer) int {
	if len(project.AgentRunner) == 0 {
		return reportError(false, errors.New("playwright-cli is not configured; run `heimdal install agent-cli` first"), out, errOut)
	}
	command := append([]string(nil), project.AgentRunner...)
	command = append(command, "install-browser", browser)
	fmt.Fprintf(out, "%s\n", commandString(command))
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = project.Root
	cmd.Env = baseEnvironment()
	cmd.Stdout = out
	cmd.Stderr = errOut
	if err := cmd.Run(); err != nil {
		return normalizeExitCode(processExitCode(err))
	}
	return 0
}
