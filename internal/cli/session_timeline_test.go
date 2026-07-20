package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionTimelineSummarizesActionsCheckpointsAndGeneratedLines(t *testing.T) {
	runDir := t.TempDir()
	started := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	state := SessionState{Name: "qa", RunID: "qa-run", Root: "/project", URL: "http://127.0.0.1:4173", SessionDir: runDir, ActionLog: filepath.Join(runDir, "actions.jsonl"), StartedAt: started}
	actions := []SessionActionRecord{
		{Sequence: 1, StartedAt: started, FinishedAt: started.Add(time.Second), Args: []string{"open", state.URL}},
		{Sequence: 2, StartedAt: started.Add(time.Second), FinishedAt: started.Add(2 * time.Second), Args: []string{"click", "e2"}, Locator: "page.getByRole('button', { name: 'Save' })"},
		{Sequence: 3, StartedAt: started.Add(2 * time.Second), FinishedAt: started.Add(2 * time.Second), Args: []string{"checkpoint", "saved settings"}},
		{Sequence: 4, StartedAt: started.Add(3 * time.Second), FinishedAt: started.Add(4 * time.Second), Args: []string{"console"}, Stderr: "Error: request failed", ExitCode: 1},
	}
	for _, action := range actions {
		if err := appendSessionAction(state.ActionLog, action); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(runDir, "action-0002.snapshot.yml"), []byte("- button Save\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	timeline, err := buildSessionTimeline(state)
	if err != nil {
		t.Fatal(err)
	}
	if timeline.Actions != 4 || timeline.Failures != 1 || timeline.Snapshots != 1 || timeline.Checkpoints != 1 {
		t.Fatalf("timeline = %#v", timeline)
	}
	if timeline.Entries[1].Category != "interaction" || len(timeline.Entries[1].GeneratedTestLines) != 1 || timeline.Entries[2].Summary != "saved settings" {
		t.Fatalf("timeline entries = %#v", timeline.Entries)
	}
	report := summarizeSessionTimeline(state, timeline)
	if report.Status != "issues" || len(report.Issues) == 0 || report.Categories["checkpoint"] != 1 {
		t.Fatalf("session report = %#v", report)
	}
}

func TestSessionViewAcceptsPositionalName(t *testing.T) {
	options, err := parseSessionViewOptions([]string{"qa", "--json"})
	if err != nil || options.Name != "qa" || !options.JSON || len(options.Forwarded) != 0 {
		t.Fatalf("view options = %#v, %v", options, err)
	}
	if _, err := parseSessionViewOptions([]string{"qa", "other"}); err == nil {
		t.Fatal("multiple session names were accepted")
	}
}
