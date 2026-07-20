package cli

import (
	"strings"
	"testing"
	"time"
)

func TestParseSessionWaitSupportsSemanticTargets(t *testing.T) {
	role, err := parseSessionWaitOptions([]string{"--role", "button", "--name", "Continue", "--state", "enabled", "--timeout", "30s", "--session", "guest", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if role.Role != "button" || role.Name != "Continue" || role.State != "enabled" || role.Timeout != 30*time.Second || role.SessionOptions.Name != "guest" || !role.JSON {
		t.Fatalf("role wait = %#v", role)
	}
	code := waitPlaywrightCode(role)
	for _, expected := range []string{`page.getByRole("button", { name: "Continue" })`, "isEnabled()", "30000"} {
		if !strings.Contains(code, expected) {
			t.Fatalf("wait code omitted %q:\n%s", expected, code)
		}
	}

	change, err := parseSessionWaitOptions([]string{"--change", "--timeout-ms", "2500"})
	if err != nil || !change.Change || change.Timeout != 2500*time.Millisecond || !strings.Contains(waitPlaywrightCode(change), "ariaSnapshot") {
		t.Fatalf("change wait = %#v, %v", change, err)
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
