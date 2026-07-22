package cli

import (
	"strings"
	"testing"
	"time"
)

func TestParseSessionExpectSupportsStableAssertions(t *testing.T) {
	role, err := parseSessionExpectOptions([]string{"--role", "button", "--name", "Continue", "--state", "enabled", "--timeout", "2s", "--session", "guest", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if role.Role != "button" || role.Name != "Continue" || role.State != "enabled" || role.Timeout != 2*time.Second || role.SessionOptions.Name != "guest" || !role.JSON {
		t.Fatalf("role expectation = %#v", role)
	}
	locator, err := expectLocator(nil, Project{}, &SessionState{}, "", role)
	if err != nil || locator != `page.getByRole("button", { name: "Continue", exact: true }).first()` {
		t.Fatalf("role locator = %q, %v", locator, err)
	}
	if code := expectPlaywrightCode(role, locator); !strings.Contains(code, "isEnabled()") || !strings.Contains(code, "2000") {
		t.Fatalf("expect code:\n%s", code)
	}

	url, err := parseSessionExpectOptions([]string{"--url", "https://example.test/done"})
	if err != nil || !strings.Contains(expectPlaywrightCode(url, ""), "page.url()") {
		t.Fatalf("URL expectation = %#v, %v", url, err)
	}
}

func TestParseSessionExpectRejectsAmbiguousShapes(t *testing.T) {
	for _, args := range [][]string{
		{},
		{"--role", "button", "--text", "Continue"},
		{"--text", "Done", "--state", "checked"},
		{"--target", "e4"},
		{"--value", "ready"},
		{"--url", "/done", "--state", "hidden"},
	} {
		if _, err := parseSessionExpectOptions(args); err == nil {
			t.Fatalf("expect args accepted: %v", args)
		}
	}
}

func TestSessionGraduationRequiresPortableAssertion(t *testing.T) {
	actions := []SessionActionRecord{
		{Args: []string{"click", "e2"}, Locator: `page.getByRole("button", { name: "Save" })`},
		{Args: []string{"expect", "--text", "Saved", "--state", "visible", "--timeout", "5s"}, Locator: `page.getByText("Saved", { exact: true }).first()`},
	}
	audit := auditSessionGraduation(actions)
	if !audit.Ready || audit.Assertions != 1 || audit.PortableActions != 2 {
		t.Fatalf("ready graduation = %#v", audit)
	}
	code := sessionTest(SessionState{Name: "qa", URL: "https://example.test"}, actions)
	if !strings.Contains(code, "import { expect, test }") || !strings.Contains(code, ".toBeVisible({ timeout: 5000 })") {
		t.Fatalf("generated expectation test:\n%s", code)
	}

	unsafe := auditSessionGraduation([]SessionActionRecord{
		{Args: []string{"mouse", "click", "12", "20"}},
		{Args: []string{"click", "e9"}},
		{Args: []string{"run-code", "async page => {}"}},
	})
	if unsafe.Ready || unsafe.Assertions != 0 || unsafe.CoordinateActions != 1 || unsafe.StaleReferences != 1 || unsafe.Unsupported < 1 || len(unsafe.Issues) < 4 {
		t.Fatalf("unsafe graduation = %#v", unsafe)
	}
}
