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

func TestSessionBatchFastPathUsesOneRunCodeAndOneFinalSnapshot(t *testing.T) {
	payload := sessionBatchFastPayload{Version: 1, Steps: []sessionBatchFastStepPayload{
		{Index: 1, Status: "passed", Snapshot: "- main:\n  - textbox \"Name\": Alice\n  - button \"Save\""},
		{Index: 2, Status: "passed", Snapshot: "- main:\n  - textbox \"Name\": Alice\n  - button \"Saved\""},
		{Index: 3, Status: "passed", Snapshot: "- main:\n  - textbox \"Name\": Alice\n  - button \"Saved\""},
	}}
	root, statePath, calls := setupSessionBatchFastFixture(t, payload)
	response, code, stderr := runSessionBatchFixture(t, root, `{
  "version": 1,
  "steps": [
    {"command":"fill","args":["e2","Alice"]},
    {"command":"click","args":["e3"]},
    {"command":"press","args":["Enter"]}
  ]
}`)
	if code != 0 {
		t.Fatalf("batch exit = %d: %s", code, stderr)
	}
	if response.Status != "passed" || response.Execution != "atomic" || response.Invocations != 2 || len(response.Steps) != 3 {
		t.Fatalf("batch response = %#v", response)
	}
	for index, step := range response.Steps {
		if step.Status != "passed" || step.Snapshot == "" || step.Action != index+1 {
			t.Fatalf("step %d evidence = %#v", index+1, step)
		}
	}
	if !strings.Contains(response.Snapshot, "[ref=f3]") {
		t.Fatalf("final official snapshot did not refresh refs: %q", response.Snapshot)
	}
	assertSessionBatchCalls(t, calls, []string{"run-code", "snapshot"})

	state, err := readSessionState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := readSessionActions(state.ActionLog)
	if err != nil {
		t.Fatal(err)
	}
	if state.ActionCount != 4 || len(actions) != 4 {
		t.Fatalf("recorded actions = %d, state count = %d; want three logical steps plus final snapshot", len(actions), state.ActionCount)
	}
	if strings.Join(actions[0].Args, " ") != "fill e2 Alice" || actions[0].Locator == "" || strings.Join(actions[1].Args, " ") != "click e3" || actions[1].Locator == "" || strings.Join(actions[2].Args, " ") != "press Enter" || strings.Join(actions[3].Args, " ") != "snapshot" {
		t.Fatalf("batch action transcript = %#v", actions)
	}
	generated := sessionTest(state, actions)
	for _, expected := range []string{"getByRole(\"textbox\"", "getByRole(\"button\"", "page.keyboard.press(\"Enter\")"} {
		if !strings.Contains(generated, expected) {
			t.Fatalf("generated test omitted %q:\n%s", expected, generated)
		}
	}
}

func TestSessionBatchFastPathIdentifiesExactFailingStepAndStillRefreshes(t *testing.T) {
	payload := sessionBatchFastPayload{Version: 1, Steps: []sessionBatchFastStepPayload{
		{Index: 1, Status: "passed", Snapshot: "- main:\n  - button \"Save\""},
		{Index: 2, Status: "failed", Snapshot: "- main:\n  - textbox \"Name\"", Error: "textbox became detached"},
	}}
	root, statePath, calls := setupSessionBatchFastFixture(t, payload)
	response, code, _ := runSessionBatchFixture(t, root, `{
  "version": 1,
  "steps": [
    {"command":"click","args":["e3"]},
    {"command":"fill","args":["e2","Alice"]},
    {"command":"press","args":["Enter"]}
  ]
}`)
	if code != 1 || response.Status != "failed" || response.Planned != 3 || len(response.Steps) != 2 {
		t.Fatalf("failed batch response = %#v (exit %d)", response, code)
	}
	if response.Steps[1].Index != 2 || response.Steps[1].Status != "failed" || !strings.Contains(response.Steps[1].Error, "detached") || !strings.Contains(response.Error, "detached") {
		t.Fatalf("failing step was not preserved: %#v", response)
	}
	if !strings.Contains(response.Snapshot, "[ref=f3]") {
		t.Fatalf("failure did not retain final refreshed refs: %q", response.Snapshot)
	}
	assertSessionBatchCalls(t, calls, []string{"run-code", "snapshot"})

	state, err := readSessionState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := readSessionActions(state.ActionLog)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 3 || actions[0].ExitCode != 0 || actions[1].ExitCode != 1 || strings.Join(actions[2].Args, " ") != "snapshot" {
		t.Fatalf("failure transcript = %#v", actions)
	}
}

func TestSessionBatchFastPathCapturesAssertionsAndNamedEvidence(t *testing.T) {
	payload := sessionBatchFastPayload{Version: 1, Steps: []sessionBatchFastStepPayload{
		{Index: 1, Status: "passed"},
		{Index: 2, Status: "passed", Snapshot: "- main:\n  - button \"Switch to light theme\""},
		{Index: 3, Status: "passed"},
		{Index: 4, Status: "passed", Evidence: json.RawMessage(`{"theme":"dark","stored":"dark"}`)},
		{Index: 5, Status: "passed", Snapshot: "- main:\n  - button \"Switch to light theme\""},
		{Index: 6, Status: "passed"},
		{Index: 7, Status: "passed", Evidence: json.RawMessage(`{"theme":"dark","stored":"dark"}`)},
	}}
	root, statePath, calls := setupSessionBatchFastFixture(t, payload)
	response, code, stderr := runSessionBatchFixture(t, root, `{
  "version": 1,
  "steps": [
    {"command":"expect","args":["--role","button","--name","Save","--state","visible"]},
    {"command":"click","args":["e3"]},
    {"command":"expect","args":["--role","button","--name","Saved","--state","visible"]},
    {"command":"evidence","args":["theme.after-click","() => ({ theme: 'dark', stored: 'dark' })"]},
    {"command":"reload"},
    {"command":"expect","args":["--role","button","--name","Saved","--state","visible"]},
    {"command":"evidence","args":["theme.after-reload","() => ({ theme: 'dark', stored: 'dark' })"]}
  ]
}`)
	if code != 0 || response.Status != "passed" || response.Execution != "atomic" || response.Invocations != 2 || len(response.Steps) != 7 {
		t.Fatalf("assertion batch = %#v (exit %d, stderr %s)", response, code, stderr)
	}
	for _, name := range []string{"theme.after-click", "theme.after-reload"} {
		var got map[string]string
		if err := json.Unmarshal(response.Evidence[name], &got); err != nil || got["theme"] != "dark" || got["stored"] != "dark" {
			t.Fatalf("evidence %s = %#v, %v", name, got, err)
		}
	}
	if response.Steps[0].Output != "expectation passed" || response.Steps[3].Output != "captured named evidence theme.after-click" {
		t.Fatalf("compact step output = %#v", response.Steps)
	}
	assertSessionBatchCalls(t, calls, []string{"run-code", "snapshot"})

	state, err := readSessionState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	actions, err := readSessionActions(state.ActionLog)
	if err != nil {
		t.Fatal(err)
	}
	if len(actions) != 8 || strings.Join(actions[0].Args[:2], " ") != "expect --role" || strings.Join(actions[3].Args[:2], " ") != "evidence theme.after-click" {
		t.Fatalf("assertion batch transcript = %#v", actions)
	}
	generated := sessionTest(state, actions)
	for _, expected := range []string{"toBeVisible", "await page.reload()"} {
		if !strings.Contains(generated, expected) {
			t.Fatalf("generated test omitted %q:\n%s", expected, generated)
		}
	}
	if strings.Contains(generated, "named evidence") || strings.Contains(generated, "TODO") {
		t.Fatalf("generated test retained exploratory evidence:\n%s", generated)
	}
}

func TestSessionBatchUnsupportedStepFallsBackForWholeBatch(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	root := t.TempDir()
	runDir := filepath.Join(root, defaultArtifactDir, "sessions", "batch", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(runDir, "retained.snapshot.yml")
	if err := os.WriteFile(retained, []byte(batchFastRetainedSnapshot), 0o644); err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "runner-calls.log")
	runner := filepath.Join(root, "fake-playwright-cli")
	script := `#!/bin/sh
kind=other
for arg in "$@"; do
  case "$arg" in
    run-code|click|eval|snapshot) kind="$arg" ;;
  esac
done
printf '%s\n' "$kind" >> ` + shellSingleQuote(calls) + `
case "$kind" in
  run-code) printf '%s\n' 'fast path must not run' >&2; exit 9 ;;
  click) printf '%s\n' 'await page.getByRole("button", { name: "Save" }).click();' '- [Snapshot](` + filepath.ToSlash(retained) + `)' ;;
  eval) printf '%s\n' '42' ;;
  snapshot) printf '%s\n' '- main [ref=f1]:' '  - button "Save" [ref=f3]' ;;
esac
`
	if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := writeSessionBatchFixtureState(t, root, runDir, retained, runner)
	response, code, stderr := runSessionBatchFixture(t, root, `{"version":1,"steps":[{"command":"click","args":["e3"]},{"command":"eval","args":["() => 42"]}]}`)
	if code != 0 || response.Status != "passed" || response.Execution != "sequential" || response.Invocations != 2 || len(response.Steps) != 2 {
		t.Fatalf("sequential fallback = %#v (exit %d, stderr %s)", response, code, stderr)
	}
	assertSessionBatchCalls(t, calls, []string{"click", "eval"})
	state, err := readSessionState(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if state.ActionCount != 2 {
		t.Fatalf("sequential fallback action count = %d, want 2", state.ActionCount)
	}
}

func TestSessionBatchFastTranslationCoversKnownSafeActions(t *testing.T) {
	steps := []sessionBatchStep{
		{Command: "click", Args: []string{"e3"}},
		{Command: "fill", Args: []string{"e2", "Alice", "--submit"}},
		{Command: "press", Args: []string{"Enter"}},
		{Command: "press", Args: []string{"e3", "Enter"}},
		{Command: "type", Args: []string{"hello"}},
		{Command: "type", Args: []string{"e2", "hello"}},
		{Command: "reload"},
		{Command: "goto", Args: []string{"https://example.test/next"}},
		{Command: "go-back"},
		{Command: "go-forward"},
		{Command: "check", Args: []string{"e4"}},
		{Command: "uncheck", Args: []string{"e4"}},
		{Command: "hover", Args: []string{"e3"}},
		{Command: "mouse", Args: []string{"click", "12.5", "40"}},
		{Command: "wait", Args: []string{"--role", "button", "--name", "Save", "--state", "enabled", "--timeout", "2s"}},
		{Command: "expect", Args: []string{"--role", "button", "--name", "Save", "--state", "visible"}},
		{Command: "expect", Args: []string{"--text", "Saved", "--state", "visible"}},
		{Command: "expect", Args: []string{"--url", "https://example.test/next"}},
		{Command: "expect", Args: []string{"--target", "e2", "--value", "Alice"}},
		{Command: "evidence", Args: []string{"form.state", "() => ({ saved: true })"}},
	}
	for index, step := range steps {
		planned, ok := translateSessionBatchFastStep(index+1, step, batchFastRetainedSnapshot)
		if !ok || (planned.Code == "" && planned.EvidenceCode == "") {
			t.Fatalf("safe action was not translated: %#v", step)
		}
	}
	compact := compactSessionBatchArgs([]string{"type", "e2", "private text"})
	if strings.Join(compact, " ") != "type e2 <text:12 chars>" {
		t.Fatalf("targeted type redaction = %v", compact)
	}
}

func TestSessionBatchRejectsInvalidNamedEvidence(t *testing.T) {
	for _, document := range []string{
		`{"version":1,"steps":[{"command":"evidence","args":["bad name","() => 1"]}]}`,
		`{"version":1,"steps":[{"command":"evidence","args":["state"]}]}`,
		`{"version":1,"steps":[{"command":"evidence","args":["state","() => 1"]},{"command":"evidence","args":["state","() => 2"]}]}`,
	} {
		if _, _, err := readSessionBatch("-", strings.NewReader(document)); err == nil {
			t.Fatalf("invalid evidence batch was accepted: %s", document)
		}
	}
}

func TestInlineSessionBatchParsesKnownFlowWithoutAFile(t *testing.T) {
	options, err := parseSessionBatchOptions([]string{
		"--session", "qa", "--json", "--",
		"click", "e3", "--then",
		"expect", "--role", "button", "--name", "Saved", "--then",
		"evidence", "save.state", "() => ({ saved: true })",
	})
	if err != nil || options.Name != "qa" || !options.JSON {
		t.Fatalf("inline batch options = %#v, %v", options, err)
	}
	document, contents, err := readInlineSessionBatch(options.Inline)
	if err != nil || len(document.Steps) != 3 || document.Steps[1].Command != "expect" || document.Steps[2].Args[0] != "save.state" || !json.Valid(contents) {
		t.Fatalf("inline batch = %#v, %s, %v", document, contents, err)
	}
	for _, args := range [][]string{{"--", "--then", "click", "e3"}, {"--", "click", "e3", "--then"}, {"--file", "steps.json", "--", "click", "e3"}} {
		parsed, parseErr := parseSessionBatchOptions(args)
		if parseErr == nil {
			_, _, parseErr = readInlineSessionBatch(parsed.Inline)
		}
		if parseErr == nil {
			t.Fatalf("invalid inline batch was accepted: %v", args)
		}
	}
}

func TestSessionBatchInlineFormUsesAtomicPath(t *testing.T) {
	payload := sessionBatchFastPayload{Version: 1, Steps: []sessionBatchFastStepPayload{
		{Index: 1, Status: "passed", Snapshot: "- main:\n  - button \"Saved\""},
		{Index: 2, Status: "passed"},
	}}
	root, _, calls := setupSessionBatchFastFixture(t, payload)
	var out, errOut strings.Builder
	code := runSessionBatch(context.Background(), []string{
		"--dir", root, "--name", "batch", "--json", "--",
		"click", "e3", "--then", "expect", "--role", "button", "--name", "Saved",
	}, &out, &errOut)
	var response sessionBatchResponse
	if err := json.Unmarshal([]byte(out.String()), &response); err != nil {
		t.Fatal(err)
	}
	if code != 0 || response.Status != "passed" || response.Execution != "atomic" || response.Invocations != 2 || len(response.Steps) != 2 {
		t.Fatalf("inline atomic batch = %#v (exit %d, stderr %s)", response, code, errOut.String())
	}
	assertSessionBatchCalls(t, calls, []string{"run-code", "snapshot"})
}

func TestSessionBatchChangeWaitKeepsRaceSafeSequentialPath(t *testing.T) {
	document := sessionBatchDocument{Version: 1, Steps: []sessionBatchStep{{Command: "wait", Args: []string{"--change", "--timeout", "2s"}}}}
	state := SessionState{LastSnapshot: "retained.yml"}
	if _, ok := planSessionBatchFast(document, state, sessionBatchOptions{}); ok {
		t.Fatal("change wait used atomic path without the retained-snapshot precheck")
	}
}

const batchFastRetainedSnapshot = `- main [ref=e1]:
  - textbox "Name" [ref=e2]
  - button "Save" [ref=e3]
  - checkbox "Agree" [ref=e4]
`

func setupSessionBatchFastFixture(t *testing.T, payload sessionBatchFastPayload) (string, string, string) {
	t.Helper()
	if payload.Baseline == "" {
		payload.Baseline = "- main:\n  - textbox \"Name\"\n  - button \"Save\""
	}
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	root := t.TempDir()
	runDir := filepath.Join(root, defaultArtifactDir, "sessions", "batch", "run-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(runDir, "retained.snapshot.yml")
	if err := os.WriteFile(retained, []byte(batchFastRetainedSnapshot), 0o644); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	calls := filepath.Join(root, "runner-calls.log")
	runner := filepath.Join(root, "fake-playwright-cli")
	script := `#!/bin/sh
kind=other
for arg in "$@"; do
  case "$arg" in
    run-code|snapshot) kind="$arg" ;;
  esac
done
printf '%s\n' "$kind" >> ` + shellSingleQuote(calls) + `
case "$kind" in
  run-code) printf '%s\n' ` + shellSingleQuote(string(encoded)) + ` ;;
  snapshot) printf '%s\n' '- main [ref=f1]:' '  - textbox "Name" [ref=f2]' '  - button "Saved" [ref=f3]' ;;
  *) printf '%s\n' 'unexpected command' >&2; exit 9 ;;
esac
`
	if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	statePath := writeSessionBatchFixtureState(t, root, runDir, retained, runner)
	return root, statePath, calls
}

func writeSessionBatchFixtureState(t *testing.T, root, runDir, retained, runner string) string {
	t.Helper()
	state := SessionState{
		SchemaVersion: sessionStateVersion,
		Name:          "batch",
		RunID:         "run-1",
		Root:          root,
		Branch:        "main",
		SessionDir:    runDir,
		CLIConfig:     filepath.Join(runDir, "playwright-cli.json"),
		ActionLog:     filepath.Join(runDir, "actions.jsonl"),
		LastSnapshot:  retained,
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
	return statePath
}

func runSessionBatchFixture(t *testing.T, root, document string) (sessionBatchResponse, int, string) {
	t.Helper()
	batchFile := filepath.Join(root, "steps.json")
	if err := os.WriteFile(batchFile, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut strings.Builder
	code := runSessionBatch(context.Background(), []string{"--file", batchFile, "--dir", root, "--name", "batch", "--json"}, &out, &errOut)
	var response sessionBatchResponse
	if err := json.Unmarshal([]byte(out.String()), &response); err != nil {
		t.Fatalf("decode batch response: %v\nstdout: %s\nstderr: %s", err, out.String(), errOut.String())
	}
	return response, code, errOut.String()
}

func assertSessionBatchCalls(t *testing.T, path string, want []string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(string(contents))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Playwright processes = %v, want %v", got, want)
	}
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
