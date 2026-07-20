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
