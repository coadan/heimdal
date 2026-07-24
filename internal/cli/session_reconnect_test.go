package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSessionReconnectOptions(t *testing.T) {
	options, err := parseSessionReconnectOptions([]string{
		"--request", "/events/stream",
		"--offline-for", "750ms",
		"--timeout", "12s",
		"--session", "qa",
		"--json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.Request != "/events/stream" ||
		options.OfflineFor != 750*time.Millisecond ||
		options.Timeout != 12*time.Second ||
		options.Name != "qa" ||
		!options.JSON {
		t.Fatalf("reconnect options = %#v", options)
	}

	defaults, err := parseSessionReconnectOptions(nil)
	if err != nil {
		t.Fatal(err)
	}
	if defaults.OfflineFor != defaultReconnectOfflineFor || defaults.Timeout != defaultReconnectTimeout {
		t.Fatalf("reconnect defaults = %#v", defaults)
	}
}

func TestParseSessionReconnectRejectsInvalidDurationsAndArguments(t *testing.T) {
	for _, args := range [][]string{
		{"--request", ""},
		{"--offline-for", "0s"},
		{"--offline-for", "1ns"},
		{"--timeout", "0s"},
		{"--timeout", "1ns"},
		{"--timeout", "1s", "--timeout-ms", "1000"},
		{"--unknown"},
	} {
		if _, err := parseSessionReconnectOptions(args); err == nil {
			t.Fatalf("reconnect args accepted: %v", args)
		}
	}
}

func TestReconnectPlaywrightCodeAlwaysRestoresNetworkAndWaitsForRequest(t *testing.T) {
	options, err := parseSessionReconnectOptions([]string{
		"--request", `/events/"quoted"`,
		"--offline-for", "500ms",
		"--timeout", "30s",
	})
	if err != nil {
		t.Fatal(err)
	}
	code := reconnectPlaywrightCode(options)
	for _, expected := range []string{
		"page.context()",
		"await context.setOffline(true)",
		"await context.setOffline(false)",
		"window.stop()",
		"finally",
		"page.waitForRequest",
		`"/events/\"quoted\""`,
		"offlineForMs = 500",
		"timeoutMs = 30000",
	} {
		if !strings.Contains(code, expected) {
			t.Fatalf("reconnect code omitted %q:\n%s", expected, code)
		}
	}
}

func TestExecuteSessionReconnectUsesOneCycleAndOneObservation(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "session")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "calls.log")
	runner := filepath.Join(root, "fake-playwright-cli")
	runnerScript := `#!/bin/sh
case "$*" in
  *" run-code "*) printf '%s\n' 'run-code' >> ` + shellSingleQuote(calls) + `; printf '%s\n' '### Result' '{"version":1,"offline_for_ms":500,"request":{"url":"http://example.test/events","method":"GET","resource_type":"fetch"}}' ;;
  *" snapshot"*) printf '%s\n' 'snapshot' >> ` + shellSingleQuote(calls) + `; printf '%s\n' '- main:' '  - status "Connected"' ;;
esac
`
	if err := os.WriteFile(runner, []byte(runnerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	state := SessionState{
		Name:       "qa",
		SessionDir: runDir,
		ActionLog:  filepath.Join(runDir, "actions.jsonl"),
		StartedAt:  time.Now().UTC(),
	}
	statePath := filepath.Join(runDir, "session.json")
	project := Project{Root: root, AgentRunner: []string{runner}, Config: defaultConfig("")}
	options, err := parseSessionReconnectOptions([]string{"--request", "/events"})
	if err != nil {
		t.Fatal(err)
	}

	response := executeSessionReconnectAction(context.Background(), project, &state, statePath, options)
	if response.Status != "passed" ||
		!strings.Contains(response.Output, "connection cycled offline for 500ms") ||
		!strings.Contains(response.Output, `observed reconnect request containing "/events"`) ||
		!strings.Contains(response.Snapshot, "Connected") {
		t.Fatalf("reconnect response = %#v", response)
	}
	contents, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(contents)); len(got) != 2 || got[0] != "run-code" || got[1] != "snapshot" {
		t.Fatalf("Playwright calls = %v, want run-code then snapshot", got)
	}
	actions, err := readSessionActions(state.ActionLog)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 2 || strings.Join(actions[0].Args, " ") != "reconnect --offline-for 500ms --request /events --timeout 30s" {
		t.Fatalf("reconnect actions = %#v", actions)
	}
	if strings.Contains(strings.Join(actions[0].Args, " "), "setOffline") {
		t.Fatalf("logical action leaked runtime code: %#v", actions[0].Args)
	}
}

func TestReconnectTreatsPlaywrightErrorMarkerAsFailure(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "session")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(root, "fake-playwright-cli")
	runnerScript := `#!/bin/sh
case "$*" in
  *" run-code "*) printf '%s\n' '### Error' 'TimeoutError: reconnect request was not observed' ;;
esac
`
	if err := os.WriteFile(runner, []byte(runnerScript), 0o755); err != nil {
		t.Fatal(err)
	}
	state := SessionState{
		Name:       "qa",
		SessionDir: runDir,
		ActionLog:  filepath.Join(runDir, "actions.jsonl"),
		StartedAt:  time.Now().UTC(),
	}
	project := Project{Root: root, AgentRunner: []string{runner}, Config: defaultConfig("")}
	options, err := parseSessionReconnectOptions([]string{"--request", "/events", "--timeout", "5ms"})
	if err != nil {
		t.Fatal(err)
	}

	response := executeSessionReconnectAction(context.Background(), project, &state, filepath.Join(runDir, "session.json"), options)
	if response.Status != "failed" || !strings.Contains(response.Error, "TimeoutError") {
		t.Fatalf("reconnect error response = %#v", response)
	}
}

func TestReconnectCanUseAtomicBatchPath(t *testing.T) {
	document := sessionBatchDocument{
		Version: 1,
		Steps: []sessionBatchStep{
			{Command: "reconnect", Args: []string{"--request", "/events"}},
			{Command: "wait", Args: []string{"--text", "Updated"}},
		},
	}
	plan, ok := planSessionBatchFast(document, SessionState{}, sessionBatchOptions{})
	if !ok || len(plan) != 2 {
		t.Fatalf("reconnect batch plan = %#v, ok=%v", plan, ok)
	}
	if !strings.Contains(plan[0].Code, "setOffline(true)") || !strings.Contains(plan[0].Code, "waitForRequest") || !plan[0].Observe {
		t.Fatalf("reconnect fast step = %#v", plan[0])
	}
}
