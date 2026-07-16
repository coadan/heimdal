package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type RunOptions struct {
	Root      string
	JSON      bool
	RunID     string
	Artifacts string
	Port      int
	Headed    bool
	Config    string
	Forwarded []string
}

type RunResult struct {
	SchemaVersion int       `json:"schema_version"`
	RunID         string    `json:"run_id"`
	Status        string    `json:"status"`
	ExitCode      int       `json:"exit_code"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	DurationMS    int64     `json:"duration_ms"`
	Root          string    `json:"root"`
	Branch        string    `json:"branch"`
	Command       []string  `json:"command"`
	CommandLine   string    `json:"command_line"`
	Playwright    string    `json:"playwright_config,omitempty"`
	Port          int       `json:"port,omitempty"`
	Failure       string    `json:"failure,omitempty"`
	StdoutTail    string    `json:"stdout_tail,omitempty"`
	StderrTail    string    `json:"stderr_tail,omitempty"`
	Artifacts     Artifacts `json:"artifacts"`
}

type Artifacts struct {
	RunDir     string   `json:"run_dir"`
	Stdout     string   `json:"stdout"`
	Stderr     string   `json:"stderr"`
	Result     string   `json:"result"`
	TestOutput string   `json:"test_output"`
	Report     string   `json:"report"`
	Files      []string `json:"files,omitempty"`
}

func executeRun(ctx context.Context, project Project, options RunOptions, out, errOut io.Writer) (RunResult, error) {
	started := time.Now().UTC()
	runID := options.RunID
	if runID == "" {
		runID = defaultRunID(project.Branch, started)
	}
	runID = sanitize(runID)
	if runID == "" {
		return RunResult{}, errors.New("run id must contain a letter or number")
	}
	root := artifactRoot(project, options.Artifacts)
	runDir := filepath.Join(root, runID)
	if _, statErr := os.Stat(runDir); statErr == nil {
		return RunResult{}, fmt.Errorf("artifact run already exists: %s (choose another --run-id)", runDir)
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return RunResult{}, fmt.Errorf("check artifact run directory %s: %w", runDir, statErr)
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return RunResult{}, fmt.Errorf("create Heimdal artifact directory: %w", err)
	}
	testOutput := filepath.Join(runDir, "test-results")
	report := filepath.Join(runDir, "report")
	if err := os.MkdirAll(testOutput, 0o755); err != nil {
		return RunResult{}, fmt.Errorf("create Playwright output directory: %w", err)
	}
	stdoutPath := filepath.Join(runDir, "stdout.log")
	stderrPath := filepath.Join(runDir, "stderr.log")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return RunResult{}, fmt.Errorf("create %s: %w", stdoutPath, err)
	}
	defer stdoutFile.Close()
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return RunResult{}, fmt.Errorf("create %s: %w", stderrPath, err)
	}
	defer stderrFile.Close()

	port := options.Port
	if project.Config.Playwright.PortEnv != "" && port == 0 {
		port, err = freePort()
		if err != nil {
			return RunResult{}, err
		}
	}
	env := runEnvironment(project, runID, runDir, testOutput, report, port)
	forwarded := append([]string(nil), options.Forwarded...)
	if options.Headed && !containsFlag(forwarded, "--headed") {
		forwarded = append(forwarded, "--headed")
	}
	if !containsFlag(forwarded, "--output") {
		forwarded = append(forwarded, "--output", testOutput)
	}
	if options.Config != "" {
		project.PlaywrightConfig = options.Config
	}
	command := commandFor(project, "test", forwarded)

	var stdoutTail, stderrTail tailBuffer
	stdoutWriter := io.MultiWriter(stdoutFile, &stdoutTail)
	stderrWriter := io.MultiWriter(stderrFile, &stderrTail)
	if !options.JSON {
		stdoutWriter = io.MultiWriter(os.Stdout, stdoutWriter)
		stderrWriter = io.MultiWriter(os.Stderr, stderrWriter)
		fmt.Fprintf(out, "Heimdal %s (%s)\n", runID, project.Branch)
		fmt.Fprintf(out, "  %s\n", commandString(command))
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = project.Root
	cmd.Env = env
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	err = cmd.Run()
	finished := time.Now().UTC()
	result := RunResult{
		SchemaVersion: 1,
		RunID:         runID,
		Status:        "passed",
		ExitCode:      0,
		StartedAt:     started,
		FinishedAt:    finished,
		DurationMS:    finished.Sub(started).Milliseconds(),
		Root:          project.Root,
		Branch:        project.Branch,
		Command:       command,
		CommandLine:   commandString(command),
		Playwright:    project.PlaywrightConfig,
		Port:          port,
		StdoutTail:    stdoutTail.String(),
		StderrTail:    stderrTail.String(),
		Artifacts: Artifacts{
			RunDir:     runDir,
			Stdout:     stdoutPath,
			Stderr:     stderrPath,
			Result:     filepath.Join(runDir, "result.json"),
			TestOutput: testOutput,
			Report:     report,
		},
	}
	if err != nil {
		result.Status = "failed"
		result.ExitCode = processExitCode(err)
		if result.ExitCode == 0 {
			result.ExitCode = 1
		}
		result.Failure = err.Error()
		if ctx.Err() != nil {
			result.Status = "cancelled"
			result.Failure = ctx.Err().Error()
		}
	}
	result.Artifacts.Files = artifactFiles(runDir)
	if err := writeJSON(result.Artifacts.Result, result); err != nil {
		return result, err
	}
	if !options.JSON {
		printResult(out, result)
	}
	return result, nil
}

func runEnvironment(project Project, runID, runDir, testOutput, report string, port int) []string {
	envMap := make(map[string]string)
	for _, entry := range baseEnvironment() {
		key, value, found := strings.Cut(entry, "=")
		if found {
			envMap[key] = value
		}
	}
	values := map[string]string{
		"RUN_ID":       runID,
		"RUN_DIR":      runDir,
		"ARTIFACT_DIR": runDir,
		"OUTPUT_DIR":   testOutput,
		"REPORT_DIR":   report,
		"ROOT":         project.Root,
		"BRANCH":       project.Branch,
		"PORT":         fmt.Sprint(port),
	}
	setEnv := func(key, value string) {
		if key == "" {
			return
		}
		envMap[key] = value
	}
	setEnv("HEIMDAL_RUN_ID", runID)
	setEnv("HEIMDAL_RUN_DIR", runDir)
	setEnv("HEIMDAL_ARTIFACT_DIR", runDir)
	setEnv("HEIMDAL_PLAYWRIGHT_OUTPUT_DIR", testOutput)
	setEnv("HEIMDAL_PLAYWRIGHT_REPORT_DIR", report)
	if port > 0 {
		setEnv("HEIMDAL_PORT", fmt.Sprint(port))
	}
	setEnv(project.Config.Playwright.RunIDEnv, runID)
	if port > 0 {
		setEnv(project.Config.Playwright.PortEnv, fmt.Sprint(port))
	}
	for key, value := range project.Config.Playwright.Env {
		setEnv(key, os.Expand(value, func(name string) string { return values[name] }))
	}
	setEnv("PLAYWRIGHT_HTML_OPEN", "never")
	setEnv("PLAYWRIGHT_HTML_OUTPUT_DIR", report)
	keys := make([]string, 0, len(envMap))
	for key := range envMap {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+envMap[key])
	}
	return env
}

func defaultRunID(branch string, now time.Time) string {
	return fmt.Sprintf("%s-%s-%d", sanitize(branch), now.Format("20060102t150405.000000000z"), os.Getpid())
}

func freePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate isolated Playwright port: %w", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func processExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

func artifactFiles(root string) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || filepath.Base(path) == "result.json" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)
	return files
}

func findLatestResult(root string) (RunResult, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return RunResult{}, fmt.Errorf("read artifact directory %s: %w", root, err)
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(root, entry.Name(), "result.json")
		info, statErr := os.Stat(path)
		if statErr == nil {
			candidates = append(candidates, candidate{path: path, mod: info.ModTime()})
		}
	}
	if len(candidates) == 0 {
		return RunResult{}, fmt.Errorf("no Heimdal runs found in %s", root)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mod.After(candidates[j].mod) })
	return readResult(candidates[0].path)
}

func readResult(path string) (RunResult, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return RunResult{}, fmt.Errorf("read %s: %w", path, err)
	}
	var result RunResult
	if err := json.Unmarshal(contents, &result); err != nil {
		return RunResult{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return result, nil
}

func findTrace(runDir string) (string, error) {
	var traces []string
	_ = filepath.WalkDir(runDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".zip") || entry.Name() == "trace" {
			traces = append(traces, path)
		}
		return nil
	})
	if len(traces) == 0 {
		return "", fmt.Errorf("no Playwright trace found in %s", runDir)
	}
	sort.Strings(traces)
	return traces[len(traces)-1], nil
}

func writeJSON(path string, value any) error {
	contents, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	contents = append(contents, '\n')
	return os.WriteFile(path, contents, 0o644)
}

func printResult(out io.Writer, result RunResult) {
	status := result.Status
	if result.ExitCode != 0 {
		status = fmt.Sprintf("%s (exit %d)", status, result.ExitCode)
	}
	fmt.Fprintf(out, "Result: %s in %dms\n", status, result.DurationMS)
	fmt.Fprintf(out, "Artifacts: %s\n", result.Artifacts.RunDir)
	if result.Failure != "" {
		fmt.Fprintf(out, "Failure: %s\n", result.Failure)
	}
}

type tailBuffer struct {
	bytes []byte
}

func (b *tailBuffer) Write(value []byte) (int, error) {
	const max = 16 * 1024
	b.bytes = append(b.bytes, value...)
	if len(b.bytes) > max {
		b.bytes = b.bytes[len(b.bytes)-max:]
	}
	return len(value), nil
}

func (b *tailBuffer) String() string {
	return strings.TrimSpace(string(b.bytes))
}
