package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionEvidenceReturnsBoundedNamedJSON(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	root := t.TempDir()
	runDir := filepath.Join(root, defaultArtifactDir, "sessions", "qa", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "calls.log")
	runner := filepath.Join(root, "fake-playwright-cli")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + shellSingleQuote(calls) + "\nprintf '%s\\n' '{\"theme\":\"dark\",\"stored\":\"dark\"}'\n"
	if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	state := SessionState{SchemaVersion: sessionStateVersion, Name: "qa", RunID: "run-1", Root: root, SessionDir: runDir, CLIConfig: filepath.Join(runDir, "playwright-cli.json"), ActionLog: filepath.Join(runDir, "actions.jsonl"), StartedAt: time.Now().UTC()}
	refreshSessionProjectCache(&state, Project{Root: root, Config: defaultConfig(""), AgentRunner: []string{runner}})
	statePath := filepath.Join(filepath.Dir(runDir), "session.json")
	if err := writeSessionState(statePath, state); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionIndex(state); err != nil {
		t.Fatal(err)
	}

	var out, errOut strings.Builder
	code := runSessionEvidence(context.Background(), []string{"theme.state", "() => ({ theme: 'dark' })", "--dir", root, "--session", "qa", "--json"}, &out, &errOut)
	var response SessionResponse
	if err := json.Unmarshal([]byte(out.String()), &response); err != nil {
		t.Fatal(err)
	}
	var evidence map[string]string
	evidenceErr := json.Unmarshal(response.Evidence["theme.state"], &evidence)
	if code != 0 || response.Status != "passed" || response.Output != "captured named evidence theme.state" || evidenceErr != nil || evidence["theme"] != "dark" || evidence["stored"] != "dark" {
		t.Fatalf("evidence response = %#v (exit %d, stderr %s)", response, code, errOut.String())
	}
	contents, err := os.ReadFile(calls)
	if err != nil || !strings.Contains(string(contents), "run-code") {
		t.Fatalf("evidence runtime call = %q, %v", contents, err)
	}
}

func TestSessionEvidenceRejectsMissingExpressionWithCorrection(t *testing.T) {
	var out, errOut strings.Builder
	if code := runSessionEvidence(context.Background(), []string{"theme.state", "--json"}, &out, &errOut); code == 0 {
		t.Fatal("missing evidence expression passed")
	}
	var response map[string]any
	if err := json.Unmarshal([]byte(out.String()), &response); err != nil || !strings.Contains(response["correction"].(string), "session evidence") {
		t.Fatalf("evidence correction = %#v, %v", response, err)
	}
}
