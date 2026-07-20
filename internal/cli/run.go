package cli

import (
	"context"
	"crypto/sha256"
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
	SchemaVersion  int                        `json:"schema_version"`
	RunID          string                     `json:"run_id"`
	Status         string                     `json:"status"`
	ExitCode       int                        `json:"exit_code"`
	StartedAt      time.Time                  `json:"started_at"`
	FinishedAt     time.Time                  `json:"finished_at"`
	DurationMS     int64                      `json:"duration_ms"`
	Root           string                     `json:"root"`
	Branch         string                     `json:"branch"`
	Command        []string                   `json:"command"`
	CommandLine    string                     `json:"command_line"`
	Playwright     string                     `json:"playwright_config,omitempty"`
	Port           int                        `json:"port,omitempty"`
	Failure        string                     `json:"failure,omitempty"`
	ProcessError   string                     `json:"process_error,omitempty"`
	PrimaryFailure *PrimaryFailure            `json:"primary_failure,omitempty"`
	FailureContext string                     `json:"failure_context,omitempty"`
	TraceDiagnosis *TraceSummary              `json:"trace_diagnosis,omitempty"`
	DiagnosisError string                     `json:"diagnosis_error,omitempty"`
	Tests          *TestCounts                `json:"tests,omitempty"`
	Warnings       []RunWarning               `json:"warnings,omitempty"`
	NextCommand    string                     `json:"next_command,omitempty"`
	Invocation     RunInvocation              `json:"invocation"`
	Environment    []RunEnvironmentVariable   `json:"environment,omitempty"`
	StdoutTail     string                     `json:"stdout_tail,omitempty"`
	StderrTail     string                     `json:"stderr_tail,omitempty"`
	Artifacts      Artifacts                  `json:"artifacts"`
	Metadata       map[string]json.RawMessage `json:"metadata,omitempty"`
	MetadataError  string                     `json:"metadata_error,omitempty"`
}

// RunManifest is written as soon as Playwright starts so report and
// coordination commands can inspect a live run before result.json exists.
// result.json remains the immutable completion record.
type RunManifest struct {
	SchemaVersion int                        `json:"schema_version"`
	RunID         string                     `json:"run_id"`
	Status        string                     `json:"status"`
	OwnerPID      int                        `json:"owner_pid"`
	ChildPID      int                        `json:"child_pid"`
	StartedAt     time.Time                  `json:"started_at"`
	Root          string                     `json:"root"`
	Branch        string                     `json:"branch"`
	Command       []string                   `json:"command"`
	CommandLine   string                     `json:"command_line"`
	Playwright    string                     `json:"playwright_config,omitempty"`
	Port          int                        `json:"port,omitempty"`
	Invocation    RunInvocation              `json:"invocation"`
	Environment   []RunEnvironmentVariable   `json:"environment,omitempty"`
	Artifacts     Artifacts                  `json:"artifacts"`
	Metadata      map[string]json.RawMessage `json:"metadata,omitempty"`
	MetadataError string                     `json:"metadata_error,omitempty"`
	Progress      *RunProgress               `json:"progress,omitempty"`
}

type RunProgress struct {
	ElapsedMS   int64  `json:"elapsed_ms"`
	LastOutput  string `json:"last_output,omitempty"`
	StdoutBytes int64  `json:"stdout_bytes"`
	StderrBytes int64  `json:"stderr_bytes"`
}

type Artifacts struct {
	RunDir          string   `json:"run_dir"`
	Stdout          string   `json:"stdout"`
	Stderr          string   `json:"stderr"`
	Result          string   `json:"result"`
	TestOutput      string   `json:"test_output"`
	Report          string   `json:"report"`
	Files           []string `json:"files,omitempty"`
	RunBytes        int64    `json:"run_bytes,omitempty"`
	TestOutputBytes int64    `json:"test_output_bytes,omitempty"`
	ReportBytes     int64    `json:"report_bytes,omitempty"`
}

func executeRun(ctx context.Context, project Project, options RunOptions, out, errOut io.Writer) (RunResult, error) {
	started := time.Now().UTC()
	runID := options.RunID
	if runID == "" {
		runID = defaultRunID(project.Branch, started)
	}
	if !validArtifactID(runID) {
		return RunResult{}, errors.New("run id must contain only lowercase letters, numbers, and hyphens")
	}
	root := artifactRoot(project, options.Artifacts)
	if options.Artifacts == "" {
		maybeCollectArtifacts(project)
	}
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
	environment := runEnvironmentProvenance(project.Config.Playwright, env)
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
	if err = cmd.Start(); err == nil {
		stopHeartbeat, heartbeatErr := startRunHeartbeat(filepath.Join(runDir, ".heartbeat"))
		if heartbeatErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return RunResult{}, heartbeatErr
		}
		defer stopHeartbeat()
		manifest := RunManifest{
			SchemaVersion: 1,
			RunID:         runID,
			Status:        "running",
			OwnerPID:      os.Getpid(),
			ChildPID:      cmd.Process.Pid,
			StartedAt:     started,
			Root:          project.Root,
			Branch:        project.Branch,
			Command:       append([]string(nil), command...),
			CommandLine:   commandString(command),
			Playwright:    project.PlaywrightConfig,
			Port:          port,
			Invocation:    parseRunInvocation(options.Forwarded),
			Environment:   environment,
			Artifacts: Artifacts{
				RunDir:     runDir,
				Stdout:     stdoutPath,
				Stderr:     stderrPath,
				Result:     filepath.Join(runDir, "result.json"),
				TestOutput: testOutput,
				Report:     report,
			},
		}
		if manifestErr := writeJSON(filepath.Join(runDir, "run.json"), manifest); manifestErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return RunResult{}, fmt.Errorf("write live run manifest: %w", manifestErr)
		}
		stopProgress := func() {}
		if options.JSON {
			stopProgress = startRunProgress(manifest, runDir, errOut, 15*time.Second)
		}
		err = cmd.Wait()
		stopProgress()
	}
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
		Invocation:    parseRunInvocation(options.Forwarded),
		Environment:   environment,
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
	enrichRunResult(&result)
	result.Metadata, err = allCoordinationMetadata(runDir)
	if err != nil {
		result.MetadataError = err.Error()
	}
	result.Artifacts.Files = artifactFiles(runDir)
	result.FailureContext = failureContextExcerpt(result.Artifacts.Files)
	if result.Status == "failed" {
		if _, traceErr := findTrace(runDir); traceErr != nil {
			result.NextCommand = ""
		}
	}
	result.Artifacts.RunBytes = directoryBytes(runDir)
	result.Artifacts.TestOutputBytes = directoryBytes(testOutput)
	result.Artifacts.ReportBytes = directoryBytes(report)
	if err := writeJSON(result.Artifacts.Result, result); err != nil {
		return result, err
	}
	if !options.JSON {
		printResult(out, result)
	}
	return result, nil
}

func startRunProgress(manifest RunManifest, runDir string, out io.Writer, interval time.Duration) func() {
	done := make(chan struct{})
	stopped := make(chan struct{})
	fmt.Fprintf(out, "heimdal: run %s started; poll with `heimdal report --run %s --json`\n", manifest.RunID, manifest.RunID)
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case now := <-ticker.C:
				progress := liveRunProgress(manifest, runDir, now.UTC())
				fmt.Fprintf(out, "heimdal: run %s active %ds; %d log bytes", manifest.RunID, progress.ElapsedMS/1000, progress.StdoutBytes+progress.StderrBytes)
				if progress.LastOutput != "" {
					fmt.Fprintf(out, "; %s", progress.LastOutput)
				}
				fmt.Fprintln(out)
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

func startRunHeartbeat(path string) (func(), error) {
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return nil, fmt.Errorf("create run heartbeat: %w", err)
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				now := time.Now()
				_ = os.Chtimes(path, now, now)
			case <-done:
				return
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
		_ = os.Remove(path)
	}, nil
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
	setEnv("HEIMDAL_RUN_METADATA_DIR", filepath.Join(runDir, "metadata"))
	setEnv("HEIMDAL_RUN_SIGNALS_DIR", filepath.Join(runDir, "signals"))
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
	slug := sanitize(branch)
	if slug == "" {
		slug = "run"
	}
	suffix := fmt.Sprintf("-%s-%d", now.Format("20060102t150405000000000z"), os.Getpid())
	if len(slug)+len(suffix) > 160 {
		digest := sha256.Sum256([]byte(branch))
		hash := fmt.Sprintf("-%x", digest[:6])
		slug = strings.TrimRight(slug[:160-len(suffix)-len(hash)], "-") + hash
	}
	return slug + suffix
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
		if err != nil || entry.IsDir() || filepath.Base(path) == "result.json" || filepath.Base(path) == ".heartbeat" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)
	return files
}

func findLatestResult(root string) (RunResult, error) {
	return findLatestResultByStatus(root, "")
}

func findLatestResultByStatus(root, status string) (RunResult, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return RunResult{}, fmt.Errorf("read artifact directory %s: %w", root, err)
	}
	var candidates []RunResult
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		result, err := readResult(filepath.Join(root, entry.Name(), "result.json"))
		if err == nil && (status == "" || result.Status == status) {
			candidates = append(candidates, result)
		}
	}
	if len(candidates) == 0 {
		if status != "" {
			return RunResult{}, fmt.Errorf("no %s Heimdal runs found in %s", status, root)
		}
		return RunResult{}, fmt.Errorf("no completed Heimdal runs found in %s", root)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].StartedAt.After(candidates[j].StartedAt) })
	return candidates[0], nil
}

func findReportRunDirectory(root, selector string) (string, error) {
	if selector != "" && selector != "latest" && selector != "latest-failed" {
		if !validArtifactID(selector) {
			return "", fmt.Errorf("run id must contain only lowercase letters, numbers, and hyphens")
		}
		runDir := filepath.Join(root, selector)
		info, err := os.Lstat(runDir)
		if err != nil {
			return "", fmt.Errorf("find Heimdal run %s: %w", selector, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("Heimdal run %s is not a directory", selector)
		}
		return runDir, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("read artifact directory %s: %w", root, err)
	}
	type candidate struct {
		directory string
		startedAt time.Time
	}
	var candidates []candidate
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		runDir := filepath.Join(root, entry.Name())
		if result, resultErr := readResult(filepath.Join(runDir, "result.json")); resultErr == nil {
			if selector == "latest-failed" && result.Status != "failed" {
				continue
			}
			candidates = append(candidates, candidate{directory: runDir, startedAt: result.StartedAt})
			continue
		}
		if selector == "latest-failed" {
			continue
		}
		if manifest, manifestErr := readRunManifest(filepath.Join(runDir, "run.json")); manifestErr == nil {
			candidates = append(candidates, candidate{directory: runDir, startedAt: manifest.StartedAt})
		}
	}
	if len(candidates) == 0 {
		if selector == "latest-failed" {
			return "", fmt.Errorf("no failed Heimdal runs found in %s", root)
		}
		return "", fmt.Errorf("no Heimdal runs found in %s", root)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].startedAt.Equal(candidates[j].startedAt) {
			return candidates[i].directory > candidates[j].directory
		}
		return candidates[i].startedAt.After(candidates[j].startedAt)
	})
	return candidates[0].directory, nil
}

func validArtifactID(value string) bool {
	if value == "" || len(value) > 160 {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || (char == '-' && index > 0) {
			continue
		}
		return false
	}
	return !strings.HasSuffix(value, "-")
}

func readRunReport(runDir string) (any, int, error) {
	return readRunReportDetailed(runDir, true)
}

func readRunReportDetailed(runDir string, includeFiles bool) (any, int, error) {
	if result, err := readResult(filepath.Join(runDir, "result.json")); err == nil {
		if result.Tests == nil || (result.Status == "failed" && result.PrimaryFailure == nil) {
			enrichRunResult(&result)
		}
		if includeFiles {
			result.Artifacts.Files = artifactFiles(runDir)
		}
		if result.Status == "failed" && result.FailureContext == "" {
			result.FailureContext = failureContextExcerptInDirectory(runDir)
		}
		if result.Status == "failed" {
			addRunTraceDiagnosis(&result, runDir)
		}
		metadata, metadataErr := allCoordinationMetadata(runDir)
		if metadataErr != nil {
			result.MetadataError = metadataErr.Error()
		} else {
			result.Metadata = metadata
		}
		return result, normalizeExitCode(result.ExitCode), nil
	}
	manifest, err := readRunManifest(filepath.Join(runDir, "run.json"))
	if err != nil {
		return nil, 1, err
	}
	if includeFiles {
		manifest.Artifacts.Files = artifactFiles(runDir)
	}
	progress := liveRunProgress(manifest, runDir, time.Now().UTC())
	manifest.Progress = &progress
	metadata, metadataErr := allCoordinationMetadata(runDir)
	if metadataErr != nil {
		manifest.MetadataError = metadataErr.Error()
	} else {
		manifest.Metadata = metadata
	}
	heartbeat, heartbeatErr := os.Stat(filepath.Join(runDir, ".heartbeat"))
	if heartbeatErr != nil || time.Since(heartbeat.ModTime()) > 15*time.Second {
		manifest.Status = "stale"
		return manifest, 1, nil
	}
	return manifest, 0, nil
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

func readRunManifest(path string) (RunManifest, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return RunManifest{}, fmt.Errorf("read %s: %w", path, err)
	}
	var manifest RunManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		return RunManifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if manifest.SchemaVersion != 1 || manifest.RunID == "" || manifest.Status != "running" {
		return RunManifest{}, fmt.Errorf("unsupported live run manifest %s", path)
	}
	return manifest, nil
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
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(contents); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
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
