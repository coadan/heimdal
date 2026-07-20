package cli

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestEveryTopLevelCommandHasSpecificSuccessfulHelp(t *testing.T) {
	commands := []string{"doctor", "init", "run", "list", "report", "runs", "trace", "gc", "metadata", "signal", "install", "skill", "session"}
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
