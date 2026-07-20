package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestSessionTimelineDefaultsStayBoundedAndSupportPagination(t *testing.T) {
	started := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	entries := make([]SessionTimelineEntry, 0, 706)
	for sequence := 1; sequence <= 706; sequence++ {
		category, command := "interaction", []string{"click", "e2"}
		summary := "clicked Continue"
		if sequence <= 390 {
			category, command = "evidence", []string{"snapshot"}
			summary = strings.Repeat("large semantic snapshot body ", 40)
		}
		if sequence == 400 {
			category, command, summary = "navigation", []string{"goto", "/settings"}, "opened settings"
		}
		if sequence == 500 {
			category, command, summary = "checkpoint", []string{"checkpoint", "configured workspace"}, "configured workspace"
		}
		status := "passed"
		if sequence == 600 {
			status, summary = "failed", "TimeoutError: save did not complete"
		}
		entries = append(entries, SessionTimelineEntry{Sequence: sequence, StartedAt: started.Add(time.Duration(sequence) * time.Second), DurationMS: 10, Category: category, Command: command, Status: status, Summary: summary, Snapshot: "/very/long/snapshot/path.yml"})
	}
	timeline := SessionTimeline{SchemaVersion: 1, Actions: len(entries), Failures: 1, Snapshots: 390, Checkpoints: 1, Entries: entries}
	timeline.Phases = summarizeSessionPhases(entries)
	timeline.PhaseCount = len(timeline.Phases)
	view := applySessionTimelineView(timeline, sessionViewOptions{})
	if !view.Truncated || view.Returned > 50 || view.Returned == 0 || view.Matched != 706 {
		t.Fatalf("compact timeline counts = %#v", view)
	}
	for _, entry := range view.Entries {
		if entry.Category == "evidence" || entry.Snapshot != "" || len(entry.Summary) > 243 {
			t.Fatalf("compact timeline leaked noisy evidence = %#v", entry)
		}
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 30000 {
		t.Fatalf("compact timeline is %d bytes", len(encoded))
	}
	state := SessionState{Name: "long", RunID: "long-run", SessionDir: "/tmp/long", ActionLog: "/tmp/long/actions.jsonl", StartedAt: started}
	report := summarizeSessionTimeline(state, timeline)
	reportJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Recent) > 12 || len(report.FailureEntries) != 1 || len(reportJSON) > 20000 {
		t.Fatalf("compact report: recent=%d failures=%d bytes=%d", len(report.Recent), len(report.FailureEntries), len(reportJSON))
	}
	full := applySessionTimelineView(timeline, sessionViewOptions{SessionOptions: SessionOptions{FullJSON: true}})
	if full.Returned != 706 || full.Truncated {
		t.Fatalf("full timeline = %#v", full)
	}
	paged := applySessionTimelineView(timeline, sessionViewOptions{Category: "evidence", Limit: 25, Explicit: true})
	if paged.Matched != 390 || paged.Returned != 25 || paged.NextFrom != 26 || paged.Filter == nil || paged.Filter.Category != "evidence" {
		t.Fatalf("paged timeline = %#v", paged)
	}
}

func TestSessionReportDoesNotTreatZeroErrorCheckAsIssue(t *testing.T) {
	started := time.Now().UTC().Add(-time.Minute)
	stopped := time.Now().UTC()
	state := SessionState{Name: "qa", RunID: "run-1", SessionDir: "/tmp/session", ActionLog: "/tmp/session/actions.jsonl", StartedAt: started, StoppedAt: &stopped}
	timeline := SessionTimeline{Actions: 2, Entries: []SessionTimelineEntry{
		{Sequence: 1, Category: "console", Command: []string{"console", "error"}, Status: "passed", Summary: "Total messages: 0 (Errors: 0, Warnings: 0)"},
		{Sequence: 2, Category: "interaction", Command: []string{"click", "e2"}, Status: "passed", Summary: "clicked Save"},
	}}
	report := summarizeSessionTimeline(state, timeline)
	if report.Status != "stopped" || len(report.Issues) != 0 || len(report.Recent) != 1 || report.Recent[0].Sequence != 2 {
		t.Fatalf("zero-error report = %#v", report)
	}
	timeline.Entries[0].Summary = "Total messages: 2 (Errors: 2, Warnings: 0)\n[ERROR] request failed"
	report = summarizeSessionTimeline(state, timeline)
	if report.Status != "issues" || len(report.Issues) != 1 {
		t.Fatalf("real-error report = %#v", report)
	}
}

func TestSessionViewParsesTimelineFilters(t *testing.T) {
	options, err := parseSessionViewOptions([]string{"qa", "--failures", "--from", "20", "--to", "40", "--category", "assertion", "--limit", "10", "--json"})
	if err != nil || options.Name != "qa" || !options.JSON || !options.Explicit || !options.FailuresOnly || options.From != 20 || options.To != 40 || options.Category != "assertion" || options.Limit != 10 {
		t.Fatalf("filtered options = %#v, %v", options, err)
	}
	if _, err := parseSessionViewOptions([]string{"--category", "screenshots"}); err == nil {
		t.Fatal("unknown category accepted")
	}
	if _, err := parseSessionViewOptions([]string{"--from", "40", "--to", "20"}); err == nil {
		t.Fatal("reversed sequence range accepted")
	}
}

func TestTimelineActionSummaryCompactsUpstreamHelp(t *testing.T) {
	action := SessionActionRecord{Args: []string{"press", "e2", "Enter"}, ExitCode: 1, Stderr: `playwright-cli press <key>

Press a key on the keyboard

Arguments:
  <key> name of key
Options:
  --help
error: too many arguments: expected 1, received 2`}
	summary := timelineActionSummary(action)
	if summary != "error: too many arguments: expected 1, received 2" || strings.Contains(summary, "Arguments:") {
		t.Fatalf("compacted help = %q", summary)
	}
}
