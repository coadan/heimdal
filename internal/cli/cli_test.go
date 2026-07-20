package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseRunOptionsPreservesPlaywrightArgs(t *testing.T) {
	options, err := parseRunOptions([]string{
		"--dir", "/tmp/project",
		"--run-id", "branch-run",
		"--headed",
		"tests/example.spec.ts",
		"--grep", "victory",
		"--",
		"--project=chromium",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Root != "/tmp/project" || options.RunID != "branch-run" || !options.Headed {
		t.Fatalf("unexpected options: %#v", options)
	}
	want := []string{"tests/example.spec.ts", "--grep", "victory", "--project=chromium"}
	if strings.Join(options.Forwarded, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("forwarded args = %#v, want %#v", options.Forwarded, want)
	}
}

func TestDirectoryFlagKeepsRootCompatibilityWithoutAmbiguity(t *testing.T) {
	legacy, err := parseRunOptions([]string{"--root", "/tmp/project"})
	if err != nil || legacy.Root != "/tmp/project" {
		t.Fatalf("legacy root option = %#v, %v", legacy, err)
	}
	if _, err := parseRunOptions([]string{"--dir", "/tmp/one", "--root", "/tmp/two"}); err == nil || !strings.Contains(err.Error(), "cannot specify different") {
		t.Fatalf("conflicting directory options should fail clearly, got %v", err)
	}
}

func TestRunEnvironmentUsesConfiguredIsolationNames(t *testing.T) {
	project := Project{
		Root:   "/tmp/project",
		Branch: "codex/test",
		Config: Config{Playwright: PlaywrightConfig{
			RunIDEnv: "APP_RUN_ID",
			PortEnv:  "APP_PORT",
			Env: map[string]string{
				"APP_ARTIFACTS": "${RUN_DIR}",
			},
		}},
	}
	env := strings.Join(runEnvironment(project, "run-1", "/tmp/run-1", "/tmp/run-1/test-results", "/tmp/run-1/report", 4567), "\n")
	for _, expected := range []string{
		"HEIMDAL_RUN_ID=run-1",
		"HEIMDAL_RUN_METADATA_DIR=/tmp/run-1/metadata",
		"HEIMDAL_RUN_SIGNALS_DIR=/tmp/run-1/signals",
		"APP_RUN_ID=run-1",
		"APP_PORT=4567",
		"APP_ARTIFACTS=/tmp/run-1",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("environment did not contain %q:\n%s", expected, env)
		}
	}
}

func TestSessionEnvironmentUsesConfiguredIsolationAndTemplates(t *testing.T) {
	project := Project{
		Root:   "/tmp/project",
		Branch: "codex/session",
		Config: Config{Session: SessionConfig{
			RunIDEnv: "APP_RUN_ID",
			PortEnv:  "APP_PORT",
			Env: map[string]string{
				"APP_DB":  "db-${RUN_ID}",
				"APP_URL": "${URL}",
			},
		}},
	}
	state := SessionState{
		Name:       "demo",
		RunID:      "run-1",
		Root:       project.Root,
		Branch:     project.Branch,
		SessionDir: "/tmp/run-1",
		URL:        "http://127.0.0.1:4567",
		Port:       4567,
	}
	env := strings.Join(sessionEnvironment(project, state), "\n")
	for _, expected := range []string{
		"APP_RUN_ID=run-1",
		"APP_PORT=4567",
		"APP_DB=db-run-1",
		"APP_URL=http://127.0.0.1:4567",
		"HEIMDAL_SESSION_NAME=demo",
	} {
		if !strings.Contains(env, expected) {
			t.Fatalf("session environment did not contain %q:\n%s", expected, env)
		}
	}
}

func TestWriteProjectConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".heimdal.json")
	cfg := defaultConfig("playwright.config.ts")
	if err := writeProjectConfig(path, cfg, false); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Config
	if err := json.Unmarshal(contents, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Playwright.Config != "playwright.config.ts" || decoded.Artifacts.Directory != defaultArtifactDir {
		t.Fatalf("unexpected config: %#v", decoded)
	}
}

func TestRunHelpIsAvailableWithoutAProject(t *testing.T) {
	var out strings.Builder
	if code := Run(context.Background(), []string{"help"}, &out, io.Discard); code != 0 {
		t.Fatalf("help exit code = %d", code)
	}
	if !strings.Contains(out.String(), "heimdal run") {
		t.Fatalf("help output did not mention run: %s", out.String())
	}
}

func TestRunResultJSONIsStable(t *testing.T) {
	result := RunResult{SchemaVersion: 1, RunID: "run-1", Status: "passed", Artifacts: Artifacts{RunDir: "/tmp/run-1"}}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"schema_version":1`) {
		t.Fatalf("unexpected result JSON: %s", encoded)
	}
}

func TestLiveRunReportPrecedesCompletionAndDetectsStaleHeartbeat(t *testing.T) {
	artifactRoot := t.TempDir()
	runDir := filepath.Join(artifactRoot, "run-one")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := RunManifest{
		SchemaVersion: 1,
		RunID:         "run-one",
		Status:        "running",
		StartedAt:     time.Now().UTC(),
		Artifacts:     Artifacts{RunDir: runDir},
	}
	if err := writeJSON(filepath.Join(runDir, "run.json"), manifest); err != nil {
		t.Fatal(err)
	}
	heartbeat := filepath.Join(runDir, ".heartbeat")
	if err := os.WriteFile(heartbeat, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := publishCoordinationMetadata(runDir, "fixture", []byte(`{"port":4173}`)); err != nil {
		t.Fatal(err)
	}
	selected, err := findReportRunDirectory(artifactRoot, "latest")
	if err != nil || selected != runDir {
		t.Fatalf("latest live run = %q, %v; want %q", selected, err, runDir)
	}
	report, code, err := readRunReport(runDir)
	if err != nil || code != 0 || report.(RunManifest).Status != "running" {
		t.Fatalf("live report = %#v, code %d, err %v", report, code, err)
	}
	if got := string(report.(RunManifest).Metadata["fixture"]); got != `{"port":4173}` {
		t.Fatalf("live report metadata = %s", got)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(heartbeat, old, old); err != nil {
		t.Fatal(err)
	}
	report, code, err = readRunReport(runDir)
	if err != nil || code != 1 || report.(RunManifest).Status != "stale" {
		t.Fatalf("stale report = %#v, code %d, err %v", report, code, err)
	}
	result := RunResult{
		SchemaVersion: 1,
		RunID:         "run-one",
		Status:        "passed",
		StartedAt:     manifest.StartedAt,
		ExitCode:      0,
		Artifacts:     Artifacts{RunDir: runDir},
	}
	if err := writeJSON(filepath.Join(runDir, "result.json"), result); err != nil {
		t.Fatal(err)
	}
	report, code, err = readRunReport(runDir)
	if err != nil || code != 0 || report.(RunResult).Status != "passed" {
		t.Fatalf("completed report = %#v, code %d, err %v", report, code, err)
	}
	if got := string(report.(RunResult).Metadata["fixture"]); got != `{"port":4173}` {
		t.Fatalf("completed report metadata = %s", got)
	}
}

func TestReportRunSelectorRejectsTraversal(t *testing.T) {
	if _, err := findReportRunDirectory(t.TempDir(), "../other"); err == nil || !strings.Contains(err.Error(), "run id") {
		t.Fatalf("traversal selector should fail clearly, got %v", err)
	}
}

func TestDefaultRunIDIsValidForEmptyAndLongBranchNames(t *testing.T) {
	now := time.Date(2026, time.July, 20, 12, 0, 0, 0, time.UTC)
	for _, branch := range []string{"", strings.Repeat("very-long-branch/", 30)} {
		runID := defaultRunID(branch, now)
		if !validArtifactID(runID) {
			t.Fatalf("default run id %q is invalid", runID)
		}
	}
}

func TestTopLevelCoordinationCommandsAndReportShareOneRun(t *testing.T) {
	root, _ := coordinationTestRun(t, "run-1")
	t.Setenv("HEIMDAL_RUN_DIR", "")
	payload := filepath.Join(t.TempDir(), "diagnostics.json")
	if err := os.WriteFile(payload, []byte(`{"database":"fixture-test"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	invoke := func(args ...string) string {
		t.Helper()
		var out, errOut strings.Builder
		if code := Run(context.Background(), args, &out, &errOut); code != 0 {
			t.Fatalf("heimdal %s exited %d: stdout=%s stderr=%s", strings.Join(args, " "), code, out.String(), errOut.String())
		}
		return out.String()
	}

	invoke("metadata", "publish", "fixture.diagnostics", "--dir", root, "--run", "run-1", "--file", payload, "--json")
	invoke("signal", "send", "diagnostics.ready", "--dir", root, "--run", "run-1", "--json")
	invoke("signal", "wait", "diagnostics.ready", "--dir", root, "--run", "run-1", "--timeout", "1s", "--json")

	var report RunResult
	if err := json.Unmarshal([]byte(invoke("report", "--dir", root, "--run", "run-1", "--json")), &report); err != nil {
		t.Fatal(err)
	}
	var diagnostics struct {
		Database string `json:"database"`
	}
	if err := json.Unmarshal(report.Metadata["fixture.diagnostics"], &diagnostics); err != nil {
		t.Fatal(err)
	}
	if diagnostics.Database != "fixture-test" {
		t.Fatalf("report metadata = %s", report.Metadata["fixture.diagnostics"])
	}
}

func TestParseSessionOptions(t *testing.T) {
	options, err := parseSessionOptions([]string{
		"--dir", "/tmp/project",
		"--name", "demo/auth",
		"--url", "http://127.0.0.1:${PORT}",
		"--port", "4567",
		"--headed",
		"--persistent",
		"--timeout-ms", "9000",
		"--verbose",
		"--",
		"--depth=4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Root != "/tmp/project" || options.Name != "demo/auth" || options.Port != 4567 {
		t.Fatalf("unexpected session options: %#v", options)
	}
	if !options.Headed || !options.Persistent || options.Timeout != 9*time.Second {
		t.Fatalf("unexpected session flags: %#v", options)
	}
	if !options.Verbose {
		t.Fatalf("verbose flag was not parsed: %#v", options)
	}
	if strings.Join(options.Forwarded, "\x00") != "--depth=4" {
		t.Fatalf("forwarded session args = %#v", options.Forwarded)
	}
}

func TestResolveAgentRunnerPrefersConfiguredRunner(t *testing.T) {
	runner := resolveAgentRunner("/tmp/project", "npm", []string{"custom-playwright-cli"})
	if strings.Join(runner, " ") != "custom-playwright-cli" {
		t.Fatalf("agent runner = %#v", runner)
	}
}

func TestWriteAgentCLIConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "playwright-cli.json")
	if err := writeAgentCLIConfig(path, SessionOptions{Browser: "chromium", Profile: "/tmp/profile"}, SessionConfig{
		BrowserLaunchOptions: BrowserLaunchOptions{
			Args:    []string{"--enable-gpu", "--ignore-gpu-blocklist"},
			Channel: "chrome",
		},
	}); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded agentCLIConfig
	if err := json.Unmarshal(contents, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.OutputDir != filepath.Dir(path) || decoded.Browser == nil || decoded.Browser.BrowserName != "chromium" {
		t.Fatalf("unexpected agent CLI config: %#v", decoded)
	}
	if decoded.Browser.LaunchOptions == nil || strings.Join(decoded.Browser.LaunchOptions.Args, ",") != "--enable-gpu,--ignore-gpu-blocklist" || decoded.Browser.LaunchOptions.Channel != "chrome" {
		t.Fatalf("browser launch options = %#v", decoded.Browser.LaunchOptions)
	}
}

func TestNormalizeGeneratedLocator(t *testing.T) {
	if got := normalizeGeneratedLocator("Locator: page.getByRole('button', { name: 'Save' })\n"); got != "page.getByRole('button', { name: 'Save' })" {
		t.Fatalf("locator = %q", got)
	}
}

func TestSessionActionsReobserveStateChanges(t *testing.T) {
	for _, action := range []string{"click", "fill", "press", "goto", "reload", "resize"} {
		if !shouldObserveAfterSessionAction(action) {
			t.Fatalf("action %q should trigger a post-action observation", action)
		}
	}
	for _, action := range []string{"snapshot", "screenshot", "console", "requests", "highlight"} {
		if shouldObserveAfterSessionAction(action) {
			t.Fatalf("action %q should not trigger a second observation", action)
		}
	}
}

func TestSessionSnapshotArgsBoundDefaultOutput(t *testing.T) {
	if got, want := strings.Join(sessionSnapshotArgs(false, false, nil), " "), "snapshot --boxes --depth=5"; got != want {
		t.Fatalf("default snapshot args = %q, want %q", got, want)
	}
	if got, want := strings.Join(sessionSnapshotArgs(true, false, nil), " "), "snapshot --depth=5"; got != want {
		t.Fatalf("no-boxes snapshot args = %q, want %q", got, want)
	}
	if got, want := strings.Join(sessionSnapshotArgs(false, true, nil), " "), "snapshot --boxes"; got != want {
		t.Fatalf("verbose snapshot args = %q, want %q", got, want)
	}
	if got, want := strings.Join(sessionSnapshotArgs(false, false, []string{"--depth=9", "--boxes"}), " "), "snapshot --depth=9 --boxes"; got != want {
		t.Fatalf("forwarded snapshot args = %q, want %q", got, want)
	}
}

func TestCompactSessionCommandRetainsUsefulResultsWithoutSecrets(t *testing.T) {
	compactArgs := compactSessionArgs([]string{"fill", "e5", "private token"})
	if strings.Join(compactArgs, " ") != "fill e5 <text:13 chars>" {
		t.Fatalf("compact command args = %#v", compactArgs)
	}
	fill := compactSessionCommand(sessionCommandResult{Args: []string{"fill", "e5", "private token"}}, "page.getByLabel('Token')")
	if strings.Contains(fill, "private token") || !strings.Contains(fill, "<text:13 chars>") {
		t.Fatalf("fill output leaked or omitted redaction: %q", fill)
	}
	find := compactSessionCommand(sessionCommandResult{
		Args:   []string{"find", "Save"},
		Stdout: "Found 1 match for \"Save\":\n\n- button \"Save\" [ref=e3]",
	}, "")
	for _, expected := range []string{"find Save", "Found 1 match", "button \"Save\""} {
		if !strings.Contains(find, expected) {
			t.Fatalf("find output omitted %q: %q", expected, find)
		}
	}
}

func TestDiagnosticIssues(t *testing.T) {
	issues := diagnosticIssues(
		"Total messages: 7 (Errors: 2, Warnings: 0)",
		"[FAILED] http://127.0.0.1:4000/assets/app.js net::ERR_CONNECTION_REFUSED",
	)
	if strings.Join(issues, "\n") != "console errors: 2\nfailed network requests detected" {
		t.Fatalf("diagnostic issues = %#v", issues)
	}
}

func TestDiscoverSessionUsesPersistedRootOutsideWorktree(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := SessionState{
		SchemaVersion: sessionStateVersion,
		Name:          "registry-demo",
		RunID:         "run-1",
		Root:          root,
		SessionDir:    filepath.Join(root, defaultArtifactDir, "sessions", "registry-demo", "run-1"),
		StartedAt:     time.Now().UTC(),
	}
	if err := os.MkdirAll(state.SessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(filepath.Dir(state.SessionDir), "session.json")
	if err := writeSessionState(statePath, state); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionIndex(state); err != nil {
		t.Fatal(err)
	}

	project, got, gotPath, err := discoverSession(SessionOptions{Name: state.Name})
	if err != nil {
		t.Fatal(err)
	}
	if project.Root != root || got.Root != root || gotPath != statePath {
		t.Fatalf("discovered session = root %q, state root %q, path %q; want %q, %q, %q", project.Root, got.Root, gotPath, root, root, statePath)
	}
}

func TestSessionTestUsesRecordedSemanticLocator(t *testing.T) {
	state := SessionState{Name: "demo", URL: "http://127.0.0.1:4000"}
	actions := []SessionActionRecord{
		{Sequence: 1, Args: []string{"click", "e5"}, Locator: "page.getByRole('button', { name: 'Save' })"},
		{Sequence: 2, Args: []string{"fill", "e6", "hello"}, Locator: "page.getByLabel('Name')"},
	}
	testCode := sessionTest(state, actions)
	for _, expected := range []string{
		"await page.goto(\"http://127.0.0.1:4000\");",
		"await page.getByRole('button', { name: 'Save' }).click();",
		"await page.getByLabel('Name').fill(\"hello\");",
	} {
		if !strings.Contains(testCode, expected) {
			t.Fatalf("generated test did not contain %q:\n%s", expected, testCode)
		}
	}
}

func TestSessionTestOmitsExplorationCommands(t *testing.T) {
	state := SessionState{Name: "demo", URL: "http://127.0.0.1:4000"}
	actions := []SessionActionRecord{
		{Sequence: 1, Args: []string{"find", "Save"}, Stdout: "Found 1 match", ExitCode: 0},
		{Sequence: 2, Args: []string{"tab-list"}, Stdout: "- 0: current", ExitCode: 0},
		{Sequence: 3, Args: []string{"click", "e5"}, Locator: "page.getByRole('button', { name: 'Save' })", ExitCode: 0},
	}
	testCode := sessionTest(state, actions)
	if strings.Contains(testCode, "Heimdal action") || strings.Contains(testCode, "find Save") || strings.Contains(testCode, "tab-list") {
		t.Fatalf("generated test retained exploration commands:\n%s", testCode)
	}
	if !strings.Contains(testCode, "getByRole('button', { name: 'Save' }).click()") {
		t.Fatalf("generated test omitted interaction:\n%s", testCode)
	}
}

func TestStartSessionUsesPersistentAgentCLIState(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	root := t.TempDir()
	runner := filepath.Join(root, "fake-playwright-cli")
	if err := os.WriteFile(runner, []byte("#!/bin/sh\nprintf 'fake playwright cli\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	project := Project{
		Root:        root,
		Branch:      "test-branch",
		Config:      defaultConfig(""),
		AgentRunner: []string{runner},
	}
	var out strings.Builder
	if code := startSession(context.Background(), project, SessionOptions{
		Name:  "demo",
		RunID: "run-1",
		URL:   "http://127.0.0.1:4567",
		JSON:  true,
	}, &out, io.Discard); code != 0 {
		t.Fatalf("start session exit code = %d: %s", code, out.String())
	}
	statePath := filepath.Join(root, defaultArtifactDir, "sessions", "demo", "session.json")
	state, err := readSessionState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.ActionCount != 2 || state.Name != "demo" || state.URL != "http://127.0.0.1:4567" {
		t.Fatalf("unexpected session state: %#v", state)
	}
	if _, err := os.Stat(state.CLIConfig); err != nil {
		t.Fatalf("missing CLI config: %v", err)
	}
	if _, err := runSessionCommand(context.Background(), project, &state, statePath, []string{"click", "e2"}, "page.getByRole('button', { name: 'Save' })"); err != nil {
		t.Fatal(err)
	}
	if state.ActionCount != 3 {
		t.Fatalf("action count = %d, want 3", state.ActionCount)
	}
	actions, err := readSessionActions(state.ActionLog)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 3 || actions[2].Locator == "" {
		t.Fatalf("unexpected actions: %#v", actions)
	}
}

func TestInstallSkillMaterializesEmbeddedFiles(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "skills", "heimdal-playwright-qa")
	if err := installSkill(destination, false); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"SKILL.md", "agents/openai.yaml"} {
		if _, err := os.Stat(filepath.Join(destination, relative)); err != nil {
			t.Fatalf("installed skill is missing %s: %v", relative, err)
		}
	}
	if err := installSkill(destination, false); err == nil {
		t.Fatal("second install should preserve the existing skill unless forced")
	}
}
