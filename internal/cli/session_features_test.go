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

func TestCompactSessionJSONOmitsRepeatedMetadata(t *testing.T) {
	response := SessionResponse{
		SchemaVersion: 1,
		Status:        "passed",
		Session:       "qa",
		RunID:         "qa-run",
		Root:          "/tmp/project",
		URL:           "http://127.0.0.1:4173",
		Action:        3,
		Command:       []string{"click", "e2"},
		Snapshot:      "- button \"Saved\" [ref=e3]",
		SnapshotMode:  "delta",
		Artifacts:     map[string]string{"directory": "/tmp/artifacts"},
		CompactJSON:   true,
	}
	var out strings.Builder
	if code := printSessionResponse(&out, io.Discard, response, true); code != 0 {
		t.Fatalf("compact JSON exit code = %d", code)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out.String()), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []string{"run_id", "root", "url", "artifacts"} {
		if _, exists := decoded[omitted]; exists {
			t.Fatalf("compact JSON retained %q: %s", omitted, out.String())
		}
	}
	if decoded["snapshot_mode"] != "delta" || decoded["action"] != float64(3) {
		t.Fatalf("compact JSON omitted action evidence: %s", out.String())
	}
}

func TestSessionOptionsSupportExpandedSnapshotAndJSON(t *testing.T) {
	options, err := parseSessionOptions([]string{"--full", "--json=full"})
	if err != nil {
		t.Fatal(err)
	}
	if !options.Full || !options.JSON || !options.FullJSON {
		t.Fatalf("options = %#v", options)
	}
}

func TestSessionBatchParsingIsBoundedAndRejectsLifecycleCommands(t *testing.T) {
	document, canonical, err := readSessionBatch("-", strings.NewReader(`{
  "version": 1,
  "steps": [
    {"command":"click","args":["e2"]},
    {"command":"eval","args":["() => document.title"]}
  ]
}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Steps) != 2 || !strings.Contains(string(canonical), `"command": "click"`) {
		t.Fatalf("unexpected canonical batch: %#v\n%s", document, canonical)
	}
	if _, _, err := readSessionBatch("-", strings.NewReader(`{"version":1,"steps":[{"command":"stop"}]}`)); err == nil {
		t.Fatal("batch accepted a lifecycle command")
	}
	if _, _, err := readSessionBatch("-", strings.NewReader(`{"version":1,"unexpected":true,"steps":[{"command":"click","args":["e2"]}]}`)); err == nil {
		t.Fatal("batch accepted an unknown field")
	}
}

func TestSessionBatchExecutesEachStepOnceInOneSession(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	root := t.TempDir()
	runDir := filepath.Join(root, defaultArtifactDir, "sessions", "batch", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshotSource := filepath.Join(runDir, "playwright-page.yml")
	if err := os.WriteFile(snapshotSource, []byte("- main [ref=e1]:\n  - button \"Done\" [ref=e3]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "runner-calls.log")
	runner := filepath.Join(root, "fake-playwright-cli")
	runnerScript := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> '" + calls + "'\ncase \"$*\" in\n  *click*) printf '%s\\n' '### Ran Playwright code' '```js' 'await page.getByRole(\"button\", { name: \"Save\" }).click();' '```' '### Snapshot' '- [Snapshot](" + snapshotSource + ")' ;;\n  *) printf '%s\\n' '42' ;;\nesac\n"
	if err := os.WriteFile(runner, []byte(runnerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	state := SessionState{
		SchemaVersion: sessionStateVersion,
		Name:          "batch",
		RunID:         "run-1",
		Root:          root,
		Branch:        "main",
		SessionDir:    runDir,
		CLIConfig:     filepath.Join(runDir, "playwright-cli.json"),
		ActionLog:     filepath.Join(runDir, "actions.jsonl"),
		StartedAt:     time.Now().UTC(),
	}
	refreshSessionProjectCache(&state, Project{Root: root, Branch: "main", Config: defaultConfig(""), AgentRunner: []string{runner}})
	statePath := filepath.Join(filepath.Dir(runDir), "session.json")
	if err := writeSessionState(statePath, state); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionIndex(state); err != nil {
		t.Fatal(err)
	}
	batchFile := filepath.Join(root, "steps.json")
	if err := os.WriteFile(batchFile, []byte(`{"version":1,"steps":[{"command":"click","args":["e2"]},{"command":"eval","args":["() => 42"]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut strings.Builder
	code := runSessionBatch(context.Background(), []string{"--file", batchFile, "--dir", root, "--name", "batch", "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("batch exit = %d\nstdout:\n%s\nstderr:\n%s", code, out.String(), errOut.String())
	}
	var response sessionBatchResponse
	if err := json.Unmarshal([]byte(out.String()), &response); err != nil {
		t.Fatal(err)
	}
	if response.Planned != 2 || len(response.Steps) != 2 || response.Snapshot == "" {
		t.Fatalf("batch response = %#v", response)
	}
	invocations, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(invocations)), "\n")); got != 2 {
		t.Fatalf("runner invocations = %d, want one per batch step:\n%s", got, invocations)
	}
	loaded, err := readSessionState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ActionCount != 2 || loaded.LastSnapshot == "" {
		t.Fatalf("updated session = %#v", loaded)
	}
}

func TestDiscoverSessionUsesValidCachedProjectFromNestedDirectory(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	root := t.TempDir()
	nested := filepath.Join(root, "deep", "path")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	state := SessionState{
		SchemaVersion: sessionStateVersion,
		Name:          "cached",
		RunID:         "run-1",
		Root:          root,
		Branch:        "cached-branch",
		SessionDir:    filepath.Join(root, defaultArtifactDir, "sessions", "cached", "run-1"),
		StartedAt:     time.Now().UTC(),
	}
	if err := os.MkdirAll(state.SessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	refreshSessionProjectCache(&state, Project{
		Root:        root,
		Branch:      state.Branch,
		Config:      defaultConfig(""),
		AgentRunner: []string{"cached-playwright-cli"},
	})
	statePath := filepath.Join(filepath.Dir(state.SessionDir), "session.json")
	if err := writeSessionState(statePath, state); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionIndex(state); err != nil {
		t.Fatal(err)
	}

	project, loaded, gotPath, err := discoverSession(SessionOptions{Root: nested, Name: state.Name})
	if err != nil {
		t.Fatal(err)
	}
	if project.Root != root || project.Branch != state.Branch || strings.Join(project.AgentRunner, " ") != "cached-playwright-cli" {
		t.Fatalf("cached project = %#v", project)
	}
	if loaded.RunID != state.RunID || gotPath != statePath {
		t.Fatalf("cached session = %#v at %q", loaded, gotPath)
	}
}

func TestDiscoverSessionInvalidatesCacheWhenConfigChanges(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	root := t.TempDir()
	state := SessionState{
		SchemaVersion: sessionStateVersion,
		Name:          "cached",
		RunID:         "run-1",
		Root:          root,
		Branch:        "main",
		SessionDir:    filepath.Join(root, defaultArtifactDir, "sessions", "cached", "run-1"),
		StartedAt:     time.Now().UTC(),
	}
	if err := os.MkdirAll(state.SessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	refreshSessionProjectCache(&state, Project{Root: root, Branch: "main", Config: defaultConfig(""), AgentRunner: []string{"cached-playwright-cli"}})
	statePath := filepath.Join(filepath.Dir(state.SessionDir), "session.json")
	if err := writeSessionState(statePath, state); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionIndex(state); err != nil {
		t.Fatal(err)
	}
	config := []byte(`{"version":1,"session":{"url":"http://127.0.0.1:4321"}}`)
	if err := os.WriteFile(filepath.Join(root, configFileName), config, 0o644); err != nil {
		t.Fatal(err)
	}

	project, _, _, err := discoverSession(SessionOptions{Root: root, Name: state.Name})
	if err != nil {
		t.Fatal(err)
	}
	if project.Config.Session.URL != "http://127.0.0.1:4321" {
		t.Fatalf("stale cached config: %#v", project.Config.Session)
	}
}

func TestSessionProjectCacheDoesNotPersistConfigEnvironment(t *testing.T) {
	state := SessionState{Root: t.TempDir()}
	config := defaultConfig("")
	config.Session.Env = map[string]string{"EXAMPLE_TOKEN": "must-not-be-cached"}
	refreshSessionProjectCache(&state, Project{Root: state.Root, Config: config, AgentRunner: []string{"playwright-cli"}})
	contents, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "must-not-be-cached") {
		t.Fatalf("session cache persisted config environment: %s", contents)
	}
}

func BenchmarkCachedSessionDiscovery(b *testing.B) {
	b.Setenv("HEIMDAL_STATE_DIR", b.TempDir())
	root := b.TempDir()
	state := SessionState{
		SchemaVersion: sessionStateVersion,
		Name:          "benchmark",
		RunID:         "run-1",
		Root:          root,
		Branch:        "main",
		SessionDir:    filepath.Join(root, defaultArtifactDir, "sessions", "benchmark", "run-1"),
		StartedAt:     time.Now().UTC(),
	}
	if err := os.MkdirAll(state.SessionDir, 0o755); err != nil {
		b.Fatal(err)
	}
	refreshSessionProjectCache(&state, Project{Root: root, Branch: "main", Config: defaultConfig(""), AgentRunner: []string{"playwright-cli"}})
	statePath := filepath.Join(filepath.Dir(state.SessionDir), "session.json")
	if err := writeSessionState(statePath, state); err != nil {
		b.Fatal(err)
	}
	if err := writeSessionIndex(state); err != nil {
		b.Fatal(err)
	}
	b.Run("cached", func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			if _, _, _, err := discoverSession(SessionOptions{Root: root, Name: state.Name}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("full-project-discovery", func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			if _, err := Discover(root); err != nil {
				b.Fatal(err)
			}
		}
	})
}
