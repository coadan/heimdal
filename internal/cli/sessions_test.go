package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionInventoryDetectsAndPrunesStaleIndexes(t *testing.T) {
	registry := t.TempDir()
	t.Setenv("HEIMDAL_STATE_DIR", registry)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "calls.log")
	runner := filepath.Join(root, "fake-playwright-cli")
	script := `#!/bin/sh
printf '%s\n' "$*" >> '` + calls + `'
printf '%s\n' '{"browsers":[{"name":"active","status":"open"}]}'
`
	if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	stopped := now.Add(-time.Hour)
	states := []SessionState{
		{Name: "active", RunID: "active-run", Root: root, StartedAt: now.Add(-3 * time.Hour)},
		{Name: "stopped", RunID: "stopped-run", Root: root, StartedAt: now.Add(-2 * time.Hour), StoppedAt: &stopped},
		{Name: "stale", RunID: "stale-run", Root: root, StartedAt: now.Add(-time.Hour)},
	}
	statePaths := map[string]string{}
	indexPaths := map[string]string{}
	for index := range states {
		state := &states[index]
		state.SessionDir = filepath.Join(root, defaultArtifactDir, "sessions", state.Name, state.RunID)
		state.ActionLog = filepath.Join(state.SessionDir, "actions.jsonl")
		state.ProjectCache = &SessionProjectCache{ConfigFile: filepath.Join(root, configFileName), ConfigStamp: "missing", AgentRunner: []string{runner}}
		if err := os.MkdirAll(state.SessionDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(state.SessionDir, "evidence.txt"), []byte("evidence"), 0o644); err != nil {
			t.Fatal(err)
		}
		statePath := filepath.Join(filepath.Dir(state.SessionDir), "session.json")
		if err := writeSessionState(statePath, *state); err != nil {
			t.Fatal(err)
		}
		if err := writeSessionIndex(*state); err != nil {
			t.Fatal(err)
		}
		statePaths[state.Name] = statePath
		indexPaths[state.Name], _ = sessionIndexPath(*state)
	}
	directory, err := sessionRegistryDirectory()
	if err != nil {
		t.Fatal(err)
	}
	brokenPath := filepath.Join(directory, "broken-index.json")
	if err := os.WriteFile(brokenPath, []byte("not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := inspectSessionInventory("", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Matched != 4 || result.Active != 1 || result.Stopped != 1 || result.Stale != 1 || result.Broken != 1 || result.Unknown != 0 {
		t.Fatalf("session inventory = %#v", result)
	}
	contents, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(strings.TrimSpace(string(contents)), "\n") != 0 {
		t.Fatalf("browser inventory was not cached per worktree: %s", contents)
	}

	dry := result
	if err := pruneSessionInventory(&dry, true, now); err != nil {
		t.Fatal(err)
	}
	if dry.Candidates != 2 || dry.Pruned != 0 || dry.CandidateBytes == 0 {
		t.Fatalf("dry prune = %#v", dry)
	}
	if _, err := os.Stat(indexPaths["stale"]); err != nil {
		t.Fatalf("dry prune removed stale index: %v", err)
	}

	if err := pruneSessionInventory(&result, false, now); err != nil {
		t.Fatal(err)
	}
	if result.Candidates != 2 || result.Pruned != 2 {
		t.Fatalf("prune = %#v", result)
	}
	for _, path := range []string{indexPaths["stale"], brokenPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("pruned index remains %s: %v", path, err)
		}
	}
	for _, path := range []string{indexPaths["active"], indexPaths["stopped"]} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("valid index removed %s: %v", path, err)
		}
	}
	finalized, err := readSessionState(statePaths["stale"])
	if err != nil || finalized.StoppedAt == nil {
		t.Fatalf("stale state not finalized: %#v, %v", finalized, err)
	}
	if _, err := os.Stat(filepath.Join(finalized.SessionDir, "evidence.txt")); err != nil {
		t.Fatalf("stale evidence was removed: %v", err)
	}
}

func TestRunSessionsListReturnsStructuredStatuses(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	var out, errOut strings.Builder
	if code := runSessions([]string{"list", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("sessions list exit = %d: %s", code, errOut.String())
	}
	var result SessionsResult
	if err := json.Unmarshal([]byte(out.String()), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "passed" || result.Matched != 0 || result.Returned != 0 || result.Sessions == nil {
		t.Fatalf("empty inventory = %#v", result)
	}
}

func TestClassifySessionRuntimeRequiresLiveBrowser(t *testing.T) {
	state := SessionState{Name: "qa"}
	available := browserInventory{available: true, names: map[string]bool{"qa": true}}
	if status, reason := classifySessionRuntime(state, available); status != "active" || reason != "" {
		t.Fatalf("active runtime = %q, %q", status, reason)
	}
	if status, reason := classifySessionRuntime(state, browserInventory{available: true, names: map[string]bool{}}); status != "stale" || !strings.Contains(reason, "browser") {
		t.Fatalf("missing runtime = %q, %q", status, reason)
	}
	stopped := time.Now().UTC()
	state.StoppedAt = &stopped
	if status, _ := classifySessionRuntime(state, browserInventory{}); status != "stopped" {
		t.Fatalf("stopped runtime = %q", status)
	}
}
