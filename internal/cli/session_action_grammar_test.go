package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStableActionGrammarNormalizesCoordinateClickAndValidatesShapes(t *testing.T) {
	state := SessionState{}
	runtime, locator, correction, err := planStableSessionAction(context.Background(), Project{}, &state, "", "mouse", []string{"mouse", "click", "12.5", "40"})
	if err != nil || locator != "" || correction != "" || len(runtime) != 2 || runtime[0] != "run-code" || !strings.Contains(runtime[1], "page.mouse.click(12.5, 40)") {
		t.Fatalf("mouse plan = %v, %q, %q, %v", runtime, locator, correction, err)
	}
	for _, input := range []struct {
		action string
		args   []string
	}{
		{"mouse", []string{"mouse", "click", "left", "20"}},
		{"fill", []string{"fill", "e1"}},
		{"press", []string{"press"}},
		{"click", []string{"click", "e1", "--force", "extra"}},
	} {
		if _, _, correction, err := planStableSessionAction(context.Background(), Project{}, &state, "", input.action, input.args); err == nil || correction == "" {
			t.Fatalf("invalid plan accepted: %v; correction=%q err=%v", input.args, correction, err)
		}
	}
}

func TestStableActionGrammarUsesGeneratedLocatorForTargetedActions(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "session")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(root, "fake-playwright-cli")
	script := "#!/bin/sh\nprintf '%s\\n' 'page.getByRole(\"textbox\", { name: \"Message\" })'\n"
	if err := os.WriteFile(runner, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	state := SessionState{Name: "qa", SessionDir: runDir, ActionLog: filepath.Join(runDir, "actions.jsonl"), StartedAt: time.Now().UTC()}
	statePath := filepath.Join(runDir, "session.json")
	project := Project{Root: root, AgentRunner: []string{runner}, Config: defaultConfig("")}
	runtime, locator, correction, err := planStableSessionAction(context.Background(), project, &state, statePath, "press", []string{"press", "e12", "Enter"})
	if err != nil || correction != "" || locator != `page.getByRole("textbox", { name: "Message" })` || !strings.Contains(strings.Join(runtime, " "), `.press("Enter")`) {
		t.Fatalf("targeted press plan = %v, %q, %q, %v", runtime, locator, correction, err)
	}
	if state.ActionCount != 1 {
		t.Fatalf("locator generation was not recorded: %#v", state)
	}
}

func TestStableActionGrammarUsesRetainedSnapshotWithoutLocatorProcess(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "session")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(runDir, "latest.snapshot.yml")
	contents := `- main [ref=e1]:
  - textbox "Message" [ref=e12]
  - button "Save" [ref=e13]
  - button "Save" [ref=e14]
`
	if err := os.WriteFile(snapshot, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	state := SessionState{Name: "qa", SessionDir: runDir, LastSnapshot: snapshot, ActionLog: filepath.Join(runDir, "actions.jsonl"), StartedAt: time.Now().UTC()}
	project := Project{Root: root, AgentRunner: []string{filepath.Join(root, "missing-playwright-cli")}, Config: defaultConfig("")}

	runtime, locator, correction, err := planStableSessionAction(context.Background(), project, &state, filepath.Join(runDir, "session.json"), "press", []string{"press", "e12", "Enter"})
	if err != nil || correction != "" || locator != `page.getByRole("textbox", { name: "Message", exact: true })` || !strings.Contains(strings.Join(runtime, " "), `.press("Enter")`) {
		t.Fatalf("targeted press plan = %v, %q, %q, %v", runtime, locator, correction, err)
	}
	if state.ActionCount != 0 {
		t.Fatalf("cached locator launched Playwright: %#v", state)
	}
	if locator := locatorFromSessionSnapshot(contents, "e14"); locator != "" {
		t.Fatalf("ambiguous snapshot locator = %q, want upstream fallback", locator)
	}
}

func TestParseGeneratedLocatorHandlesRawAndCodeOutput(t *testing.T) {
	for _, output := range []string{
		`page.getByText("Continue")`,
		"### Result\n```js\ngetByRole('button', { name: 'Save' })\n```\n",
		"await page.locator('#search');\n",
	} {
		if locator := parseGeneratedLocator(output); !strings.HasPrefix(locator, "page.") {
			t.Fatalf("locator from %q = %q", output, locator)
		}
	}
}

func TestStableActionGrammarUsesElementRelativePointerCoordinates(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "session")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(runDir, "latest.snapshot.yml")
	if err := os.WriteFile(snapshot, []byte("- main [ref=e1]:\n  - button \"Canvas surface\" [ref=e2]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := SessionState{Name: "qa", SessionDir: runDir, LastSnapshot: snapshot}
	project := Project{Root: root, AgentRunner: []string{filepath.Join(root, "missing-playwright-cli")}, Config: defaultConfig("")}

	clickArgs := []string{"click", "--within", "e2", "--at", "62%,35%"}
	runtime, locator, correction, err := planStableSessionAction(context.Background(), project, &state, "", "click", clickArgs)
	if err != nil || correction != "" || !strings.Contains(locator, "Canvas surface") || !strings.Contains(strings.Join(runtime, " "), "box.width * 0.62") {
		t.Fatalf("relative click plan = %v, %q, %q, %v", runtime, locator, correction, err)
	}
	dragArgs := []string{"pointer", "drag", "--within", "e2", "--from", "10%,20%", "--to", "90%,80%"}
	runtime, locator, correction, err = planStableSessionAction(context.Background(), project, &state, "", "pointer", dragArgs)
	if err != nil || correction != "" || !strings.Contains(strings.Join(runtime, " "), "page.mouse.down") || !strings.Contains(strings.Join(runtime, " "), "box.width * 0.9") {
		t.Fatalf("relative drag plan = %v, %q, %q, %v", runtime, locator, correction, err)
	}

	lines := sessionActionTestLines(SessionActionRecord{Args: dragArgs, Locator: locator})
	generated := strings.Join(lines, "\n")
	if !strings.Contains(generated, "boundingBox") || !strings.Contains(generated, "page.mouse.up") {
		t.Fatalf("relative drag test lines:\n%s", generated)
	}
	for _, invalid := range []string{"62,35", "-1%,20%", "101%,20%"} {
		if _, _, err := parseRelativePoint(invalid); err == nil {
			t.Fatalf("relative point accepted: %q", invalid)
		}
	}
}
