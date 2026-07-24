package cli

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestEveryTopLevelCommandHasSpecificSuccessfulHelp(t *testing.T) {
	commands := []string{"doctor", "init", "run", "list", "report", "runs", "trace", "gc", "metadata", "signal", "install", "skill", "session", "sessions"}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			var out strings.Builder
			if code := Run(context.Background(), []string{command, "--help"}, &out, io.Discard); code != 0 {
				t.Fatalf("help exit = %d", code)
			}
			if !strings.Contains(out.String(), "Usage:") || strings.Contains(out.String(), "unknown option") {
				t.Fatalf("help output:\n%s", out.String())
			}
		})
	}
}

func TestPlaywrightHelpAfterDelimiterIsNotConsumed(t *testing.T) {
	if _, ok := commandHelp([]string{"run", "--", "--help"}); ok {
		t.Fatal("Heimdal consumed Playwright help after the forwarding delimiter")
	}
}

func TestCanonicalSessionCommandsHaveSpecificHelp(t *testing.T) {
	commands := []string{"start", "stop", "status", "list", "prune", "observe", "screenshot", "diagnose", "wait", "expect", "reconnect", "timeline", "report", "checkpoint", "measure", "batch", "save", "group", "click", "fill", "press", "type", "mouse", "pointer"}
	for _, command := range commands {
		t.Run(command, func(t *testing.T) {
			usage, ok := commandHelp([]string{"session", command, "--help"})
			if !ok || !strings.Contains(usage, "Usage:") || !strings.Contains(usage, "session "+command) {
				t.Fatalf("specific help for %s:\n%s", command, usage)
			}
			prefixed, ok := commandHelp([]string{"help", "session", command})
			if !ok || prefixed != usage {
				t.Fatalf("help session %s differs:\n%s", command, prefixed)
			}
		})
	}
}

func TestNestedCommandsHaveSpecificHelp(t *testing.T) {
	commands := [][2]string{{"runs", "list"}, {"runs", "show"}, {"runs", "compare"}, {"runs", "pin"}, {"sessions", "list"}, {"sessions", "prune"}, {"metadata", "publish"}, {"metadata", "get"}, {"signal", "send"}, {"signal", "wait"}, {"skill", "path"}, {"skill", "install"}, {"trace", "inspect"}}
	for _, command := range commands {
		name := command[0] + " " + command[1]
		t.Run(name, func(t *testing.T) {
			usage, ok := commandHelp([]string{command[0], command[1], "--help"})
			if !ok || !strings.Contains(usage, "Usage:") || !strings.Contains(usage, name) {
				t.Fatalf("specific help for %s:\n%s", name, usage)
			}
			prefixed, ok := commandHelp([]string{"help", command[0], command[1]})
			if !ok || prefixed != usage {
				t.Fatalf("help %s differs:\n%s", name, prefixed)
			}
		})
	}
}

func TestSessionGroupCommandsHaveSpecificHelp(t *testing.T) {
	for _, command := range []string{"start", "status", "stop", "timeline", "report"} {
		t.Run(command, func(t *testing.T) {
			usage, ok := commandHelp([]string{"session", "group", command, "--help"})
			if !ok || !strings.Contains(usage, "Usage:") || !strings.Contains(usage, "session group "+command) {
				t.Fatalf("specific group help for %s:\n%s", command, usage)
			}
			prefixed, ok := commandHelp([]string{"help", "session", "group", command})
			if !ok || prefixed != usage {
				t.Fatalf("help session group %s differs:\n%s", command, prefixed)
			}
		})
	}
}
