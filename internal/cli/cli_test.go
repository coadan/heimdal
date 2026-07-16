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
		"--root", "/tmp/project",
		"--run-id", "branch/run",
		"--headed",
		"tests/example.spec.ts",
		"--grep", "victory",
		"--",
		"--project=chromium",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Root != "/tmp/project" || options.RunID != "branch/run" || !options.Headed {
		t.Fatalf("unexpected options: %#v", options)
	}
	want := []string{"tests/example.spec.ts", "--grep", "victory", "--project=chromium"}
	if strings.Join(options.Forwarded, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("forwarded args = %#v, want %#v", options.Forwarded, want)
	}
}

func TestRunEnvironmentUsesConfiguredIsolationNames(t *testing.T) {
	project := Project{
		Root:   "/tmp/project",
		Branch: "codex/test",
		Config: Config{Playwright: PlaywrightConfig{
			RunIDEnv: "VOID_RUN_ID",
			PortEnv:  "VOID_PORT",
			Env: map[string]string{
				"VOID_ARTIFACTS": "${RUN_DIR}",
			},
		}},
	}
	env := strings.Join(runEnvironment(project, "run-1", "/tmp/run-1", "/tmp/run-1/test-results", "/tmp/run-1/report", 4567), "\n")
	for _, expected := range []string{
		"HEIMDAL_RUN_ID=run-1",
		"VOID_RUN_ID=run-1",
		"VOID_PORT=4567",
		"VOID_ARTIFACTS=/tmp/run-1",
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

func TestParseSessionOptions(t *testing.T) {
	options, err := parseSessionOptions([]string{
		"--root", "/tmp/project",
		"--name", "void/auth",
		"--url", "http://127.0.0.1:${PORT}",
		"--port", "4567",
		"--headed",
		"--persistent",
		"--timeout-ms", "9000",
		"--",
		"--depth=4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Root != "/tmp/project" || options.Name != "void/auth" || options.Port != 4567 {
		t.Fatalf("unexpected session options: %#v", options)
	}
	if !options.Headed || !options.Persistent || options.Timeout != 9*time.Second {
		t.Fatalf("unexpected session flags: %#v", options)
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
	if err := writeAgentCLIConfig(path, SessionOptions{Browser: "chromium", Profile: "/tmp/profile"}, SessionConfig{}); err != nil {
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

func TestStartSessionUsesPersistentAgentCLIState(t *testing.T) {
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
