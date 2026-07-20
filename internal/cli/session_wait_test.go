package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSessionWaitSupportsSemanticTargets(t *testing.T) {
	role, err := parseSessionWaitOptions([]string{"--role", "button", "--name", "Continue", "--state", "enabled", "--timeout", "30s", "--settle", "300ms", "--session", "guest", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if role.Role != "button" || role.Name != "Continue" || role.State != "enabled" || role.Timeout != 30*time.Second || role.Settle != 300*time.Millisecond || role.SessionOptions.Name != "guest" || !role.JSON {
		t.Fatalf("role wait = %#v", role)
	}
	code := waitPlaywrightCode(role)
	for _, expected := range []string{`page.getByRole("button", { name: "Continue" })`, "isEnabled()", "30000", "semantic state to settle"} {
		if !strings.Contains(code, expected) {
			t.Fatalf("wait code omitted %q:\n%s", expected, code)
		}
	}

	change, err := parseSessionWaitOptions([]string{"--change", "--timeout-ms", "2500"})
	if err != nil || !change.Change || change.Timeout != 2500*time.Millisecond || !strings.Contains(waitPlaywrightCode(change), "ariaSnapshot") {
		t.Fatalf("change wait = %#v, %v", change, err)
	}
}

func TestEnabledWaitUsesOneTimeoutDeadline(t *testing.T) {
	options, err := parseSessionWaitOptions([]string{"--role", "button", "--name", "Continue", "--state", "enabled", "--timeout", "5s"})
	if err != nil {
		t.Fatal(err)
	}
	code := waitPlaywrightCode(options)
	if count := strings.Count(code, "Date.now() + 5000"); count != 1 {
		t.Fatalf("enabled wait has %d timeout deadlines:\n%s", count, code)
	}
	if !strings.Contains(code, "deadline - Date.now()") {
		t.Fatalf("enabled wait did not share remaining timeout:\n%s", code)
	}
}

func TestChangeWaitRecognizesChangeSinceRetainedSnapshot(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "session")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	previous := filepath.Join(runDir, "previous.yml")
	if err := os.WriteFile(previous, []byte("- main [ref=e1]:\n  - button \"Before\" [ref=e2]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(root, "fake-playwright-cli")
	if err := os.WriteFile(runner, []byte("#!/bin/sh\nprintf '%s\\n' '- main [ref=f1]:' '  - button \"After\" [ref=f2]'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := SessionState{Name: "qa", SessionDir: runDir, LastSnapshot: previous, ActionLog: filepath.Join(runDir, "actions.jsonl"), StartedAt: time.Now().UTC()}
	statePath := filepath.Join(runDir, "session.json")
	project := Project{Root: root, AgentRunner: []string{runner}, Config: defaultConfig("")}
	options := sessionWaitOptions{SessionOptions: SessionOptions{JSON: true, Timeout: time.Second}, Change: true, State: "visible"}

	response, completed := completedChangeBeforeWait(context.Background(), project, &state, statePath, options)
	if !completed || response.Status != "passed" || !strings.Contains(response.Snapshot, "After") || !strings.Contains(response.Output, "already observed") {
		t.Fatalf("precompleted wait = completed %v, response %#v", completed, response)
	}
	if state.ActionCount != 1 {
		t.Fatalf("precompleted wait used %d Playwright calls, want 1", state.ActionCount)
	}
}

func TestParseSessionWaitRejectsAmbiguousOrBlindWaits(t *testing.T) {
	for _, args := range [][]string{
		{"--role", "button", "--text", "Continue"},
		{"--change", "--state", "hidden"},
		{"--ms", "30000"},
		{"--text", "ready", "--state", "enabled", "--timeout", "0s"},
	} {
		if _, err := parseSessionWaitOptions(args); err == nil {
			t.Fatalf("wait args accepted: %v", args)
		}
	}
}

func TestWaitLogicalArgsDoNotExposeRuntimeCode(t *testing.T) {
	options, err := parseSessionWaitOptions([]string{"--text", "The world answers"})
	if err != nil {
		t.Fatal(err)
	}
	args := waitLogicalArgs(options)
	if strings.Contains(strings.Join(args, " "), "run-code") || strings.Contains(strings.Join(args, " "), "ariaSnapshot") {
		t.Fatalf("logical args leaked runtime code: %v", args)
	}
}
