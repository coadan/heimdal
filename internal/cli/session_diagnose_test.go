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

func TestCompactSessionDiagnosticsGroupsRepeatedFailures(t *testing.T) {
	console := sessionCommandResult{Stdout: `Total messages: 3 (Errors: 3, Warnings: 0)
[ERROR] Failed to load resource @ http://127.0.0.1:4173/api/tasks/one:0
[ERROR] Failed to load resource @ http://127.0.0.1:4173/api/tasks/one:0
[ERROR] Assertion failed @ http://127.0.0.1:4173/app.js:42`}
	compact := compactSessionDiagnosticOutput([]string{"console", "error"}, console)
	for _, expected := range []string{"3 entries, 2 signatures", "2× [ERROR] Failed to load resource @ /api/tasks/one", "1× [ERROR] Assertion failed @ /app.js"} {
		if !strings.Contains(compact, expected) {
			t.Fatalf("compact console omitted %q:\n%s", expected, compact)
		}
	}
	if strings.Contains(compact, "127.0.0.1") {
		t.Fatalf("compact console retained repeated origin:\n%s", compact)
	}

	requests := sessionCommandResult{Stdout: `25. [GET] http://127.0.0.1:4173/api/tasks/one => [500] Internal Server Error
26. [GET] http://127.0.0.1:4173/api/tasks/one => [500] Internal Server Error
27. [POST] http://127.0.0.1:4173/api/save => [409] Conflict`}
	compact = compactSessionDiagnosticOutput([]string{"requests"}, requests)
	for _, expected := range []string{"3 entries, 2 signatures", "2× GET /api/tasks/one => 500", "1× POST /api/save => 409"} {
		if !strings.Contains(compact, expected) {
			t.Fatalf("compact requests omitted %q:\n%s", expected, compact)
		}
	}
}

func TestCompactSessionDiagnosticsPreservesUnknownOutput(t *testing.T) {
	result := compactSessionDiagnosticOutput([]string{"console", "error"}, sessionCommandResult{Stdout: "upstream format changed"})
	if !strings.Contains(result, "upstream format changed") {
		t.Fatalf("unknown diagnostic output was discarded: %q", result)
	}
}

func TestCompactSessionDiagnosticsCollapsesEmptyRuntimeOutput(t *testing.T) {
	console := compactSessionDiagnosticOutput([]string{"console", "error"}, sessionCommandResult{Stdout: "Total messages: 0 (Errors: 0, Warnings: 0)"})
	if console != "console error: none" {
		t.Fatalf("empty console = %q", console)
	}
	requests := compactSessionDiagnosticOutput([]string{"requests"}, sessionCommandResult{Stdout: "Note: 1 static request not shown, run with --static option to see it."})
	if requests != "requests: none (1 static omitted)" {
		t.Fatalf("static-only requests = %q", requests)
	}
}

func TestSessionDiagnoseCompactsUnchangedStateAndStopsOnce(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	root := t.TempDir()
	runDir := filepath.Join(root, defaultArtifactDir, "sessions", "qa", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(runDir, "page.yml")
	if err := os.WriteFile(snapshot, []byte("- heading \"Ready\" [ref=e2]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "calls.log")
	runner := filepath.Join(root, "fake-playwright-cli")
	script := `#!/bin/sh
printf '%s\n' "$*" >> '` + calls + `'
case "$*" in
  *" screenshot") printf '%s\n' 'Screenshot saved to /tmp/page.png' ;;
  *" console error") printf '%s\n' 'Total messages: 2 (Errors: 0, Warnings: 2)' '[WARNING] Slow response @ http://127.0.0.1:4173/app.js:1' '[WARNING] Slow response @ http://127.0.0.1:4173/app.js:1' ;;
  *" requests") printf '%s\n' '1. [GET] http://127.0.0.1:4173/api/ready => [200] OK' '2. [GET] http://127.0.0.1:4173/api/ready => [200] OK' ;;
  *" snapshot") printf '%s\n' '- heading "Ready" [ref=e2]' ;;
  *" close") printf '%s\n' 'closed' ;;
esac
`
	if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(runDir, "action-0000.snapshot.yml")
	if err := os.WriteFile(previous, []byte("- heading \"Ready\" [ref=e1]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := SessionState{SchemaVersion: sessionStateVersion, Name: "qa", RunID: "run-1", Root: root, SessionDir: runDir, CLIConfig: filepath.Join(runDir, "playwright-cli.json"), ActionLog: filepath.Join(runDir, "actions.jsonl"), LastSnapshot: previous, StartedAt: time.Now().UTC()}
	refreshSessionProjectCache(&state, Project{Root: root, Config: defaultConfig(""), AgentRunner: []string{runner}})
	statePath := filepath.Join(filepath.Dir(runDir), "session.json")
	if err := writeSessionState(statePath, state); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionIndex(state); err != nil {
		t.Fatal(err)
	}

	var out, errOut strings.Builder
	if code := runSessionDiagnose(context.Background(), []string{"--dir", root, "--name", "qa", "--stop", "--screenshot", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("diagnose exit = %d\nstdout=%s\nstderr=%s", code, out.String(), errOut.String())
	}
	var response SessionResponse
	if err := json.Unmarshal([]byte(out.String()), &response); err != nil {
		t.Fatal(err)
	}
	if !response.Closed || response.Status != "passed" || response.SnapshotMode != "delta" || response.Snapshot != "No semantic changes." {
		t.Fatalf("diagnose response = %#v", response)
	}
	for _, expected := range []string{"Screenshot saved to /tmp/page.png", "2 entries, 1 signatures", "2× [WARNING] Slow response @ /app.js", "session: closed"} {
		if !strings.Contains(response.Output, expected) {
			t.Fatalf("diagnose output omitted %q:\n%s", expected, response.Output)
		}
	}
	loaded, err := readSessionState(statePath)
	if err != nil || loaded.StoppedAt == nil {
		t.Fatalf("stopped state = %#v, %v", loaded, err)
	}
	contents, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(contents), " close\n"); got != 1 {
		t.Fatalf("close calls = %d:\n%s", got, contents)
	}
	if got := strings.Count(string(contents), " screenshot\n"); got != 1 {
		t.Fatalf("screenshot calls = %d:\n%s", got, contents)
	}
}

func TestSessionDiagnoseOptionsRejectDuplicateStop(t *testing.T) {
	if _, err := parseSessionDiagnoseOptions([]string{"--stop", "--stop"}); err == nil {
		t.Fatal("duplicate --stop was accepted")
	}
}

func TestSessionDiagnoseOptionsRejectDuplicateScreenshot(t *testing.T) {
	if _, err := parseSessionDiagnoseOptions([]string{"--screenshot", "--screenshot"}); err == nil {
		t.Fatal("duplicate --screenshot was accepted")
	}
}
