package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRunProgressKeepsCallerOnForegroundCommand(t *testing.T) {
	var output bytes.Buffer
	stop := startRunProgress(
		RunManifest{RunID: "focused-run", StartedAt: time.Now().UTC()},
		t.TempDir(),
		&output,
		time.Hour,
	)
	stop()

	message := output.String()
	if !strings.Contains(message, "keep waiting on this command") {
		t.Fatalf("progress guidance omitted foreground wait: %q", message)
	}
	if strings.Contains(message, "poll") || strings.Contains(message, "report") {
		t.Fatalf("progress guidance encouraged a second inspection loop: %q", message)
	}
}
