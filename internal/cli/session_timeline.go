package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SessionTimelineEntry struct {
	Sequence           int       `json:"sequence"`
	StartedAt          time.Time `json:"started_at"`
	DurationMS         int64     `json:"duration_ms"`
	Category           string    `json:"category"`
	Command            []string  `json:"command"`
	Status             string    `json:"status"`
	Locator            string    `json:"locator,omitempty"`
	Summary            string    `json:"summary,omitempty"`
	Snapshot           string    `json:"snapshot,omitempty"`
	GeneratedTestLines []int     `json:"generated_test_lines,omitempty"`
}

type SessionTimeline struct {
	SchemaVersion int                    `json:"schema_version"`
	Session       string                 `json:"session"`
	RunID         string                 `json:"run_id"`
	Root          string                 `json:"root"`
	URL           string                 `json:"url,omitempty"`
	StartedAt     time.Time              `json:"started_at"`
	StoppedAt     *time.Time             `json:"stopped_at,omitempty"`
	Actions       int                    `json:"actions"`
	Failures      int                    `json:"failures"`
	Snapshots     int                    `json:"snapshots"`
	Checkpoints   int                    `json:"checkpoints"`
	Entries       []SessionTimelineEntry `json:"entries"`
}

type SessionReport struct {
	SchemaVersion int                    `json:"schema_version"`
	Session       string                 `json:"session"`
	RunID         string                 `json:"run_id"`
	Status        string                 `json:"status"`
	DurationMS    int64                  `json:"duration_ms"`
	Actions       int                    `json:"actions"`
	Failures      int                    `json:"failures"`
	Snapshots     int                    `json:"snapshots"`
	Checkpoints   int                    `json:"checkpoints"`
	Categories    map[string]int         `json:"categories"`
	Issues        []string               `json:"issues,omitempty"`
	Recent        []SessionTimelineEntry `json:"recent"`
	Artifacts     map[string]string      `json:"artifacts"`
}

func runSessionTimeline(args []string, out, errOut io.Writer) int {
	options, err := parseSessionViewOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	_, state, _, err := discoverSession(options)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	timeline, err := buildSessionTimeline(state)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if options.JSON {
		_ = writeJSONTo(out, timeline)
	} else {
		printSessionTimeline(out, timeline)
	}
	return 0
}

func runSessionReport(args []string, out, errOut io.Writer) int {
	options, err := parseSessionViewOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	_, state, _, err := discoverSession(options)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	timeline, err := buildSessionTimeline(state)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	report := summarizeSessionTimeline(state, timeline)
	if options.JSON {
		_ = writeJSONTo(out, report)
	} else {
		fmt.Fprintf(out, "Heimdal session %s: %s; %d actions, %d failures, %d snapshots, %d checkpoints\n", report.Session, report.Status, report.Actions, report.Failures, report.Snapshots, report.Checkpoints)
		for _, issue := range report.Issues {
			fmt.Fprintf(out, "  issue: %s\n", issue)
		}
	}
	return boolToCode(report.Status == "issues")
}

func runSessionCheckpoint(args []string, out, errOut io.Writer) int {
	options, err := parseSessionOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if len(options.Forwarded) != 1 {
		return reportError(options.JSON, errors.New("session checkpoint requires one quoted label"), out, errOut)
	}
	label := strings.TrimSpace(options.Forwarded[0])
	if label == "" || len(label) > 200 {
		return reportError(options.JSON, errors.New("checkpoint label must contain 1 to 200 characters"), out, errOut)
	}
	_, state, statePath, err := discoverSession(options)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	now := time.Now().UTC()
	state.ActionCount++
	record := SessionActionRecord{Sequence: state.ActionCount, StartedAt: now, FinishedAt: now, Args: []string{"checkpoint", label}}
	if err := appendSessionAction(state.ActionLog, record); err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if err := writeSessionState(statePath, state); err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	artifact := filepath.Join(state.SessionDir, fmt.Sprintf("checkpoint-%04d.json", record.Sequence))
	if err := writeJSON(artifact, map[string]any{"schema_version": 1, "sequence": record.Sequence, "label": label, "created_at": now}); err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	response := sessionResponse(state, sessionCommandResult{Args: record.Args, Sequence: record.Sequence}, nil)
	response.Status, response.Output, response.CompactJSON = "passed", "checkpointed "+label, !options.FullJSON
	response.Artifacts["checkpoint"] = artifact
	return printSessionResponse(out, errOut, response, options.JSON)
}

func parseSessionViewOptions(args []string) (SessionOptions, error) {
	options, err := parseSessionOptions(args)
	if err != nil {
		return options, err
	}
	if len(options.Forwarded) > 1 {
		return options, errors.New("session timeline/report accepts at most one session name")
	}
	if len(options.Forwarded) == 1 {
		if options.Name != "" {
			return options, errors.New("select the session positionally or with --name, not both")
		}
		options.Name = options.Forwarded[0]
		options.Forwarded = nil
	}
	return options, nil
}

func buildSessionTimeline(state SessionState) (SessionTimeline, error) {
	actions, err := readSessionActions(state.ActionLog)
	if err != nil {
		return SessionTimeline{}, err
	}
	timeline := SessionTimeline{SchemaVersion: 1, Session: state.Name, RunID: state.RunID, Root: state.Root, URL: state.URL, StartedAt: state.StartedAt, StoppedAt: state.StoppedAt, Actions: len(actions), Entries: make([]SessionTimelineEntry, 0, len(actions))}
	testLine := 4
	if state.URL != "" {
		testLine++
	}
	for _, action := range actions {
		entry := SessionTimelineEntry{Sequence: action.Sequence, StartedAt: action.StartedAt, DurationMS: action.FinishedAt.Sub(action.StartedAt).Milliseconds(), Category: sessionActionCategory(action.Args), Command: compactSessionArgs(action.Args), Status: "passed", Locator: action.Locator}
		if action.ExitCode != 0 {
			entry.Status = "failed"
			timeline.Failures++
		}
		entry.Summary = timelineActionSummary(action)
		snapshot := filepath.Join(state.SessionDir, fmt.Sprintf("action-%04d.snapshot.yml", action.Sequence))
		if info, err := os.Stat(snapshot); err == nil && !info.IsDir() {
			entry.Snapshot = snapshot
			timeline.Snapshots++
		}
		if len(action.Args) > 0 && action.Args[0] == "checkpoint" {
			timeline.Checkpoints++
		}
		generated := sessionActionTestLines(action)
		for range generated {
			entry.GeneratedTestLines = append(entry.GeneratedTestLines, testLine)
			testLine++
		}
		timeline.Entries = append(timeline.Entries, entry)
	}
	return timeline, nil
}

func summarizeSessionTimeline(state SessionState, timeline SessionTimeline) SessionReport {
	finished := time.Now().UTC()
	status := "active"
	if state.StoppedAt != nil {
		finished, status = *state.StoppedAt, "stopped"
	}
	if timeline.Failures > 0 {
		status = "issues"
	}
	report := SessionReport{SchemaVersion: 1, Session: state.Name, RunID: state.RunID, Status: status, DurationMS: finished.Sub(state.StartedAt).Milliseconds(), Actions: timeline.Actions, Failures: timeline.Failures, Snapshots: timeline.Snapshots, Checkpoints: timeline.Checkpoints, Categories: map[string]int{}, Artifacts: map[string]string{"directory": state.SessionDir, "actions": state.ActionLog}}
	for _, entry := range timeline.Entries {
		report.Categories[entry.Category]++
		if entry.Status == "failed" && len(report.Issues) < 20 {
			report.Issues = append(report.Issues, fmt.Sprintf("action %d (%s): %s", entry.Sequence, commandString(entry.Command), entry.Summary))
		}
		if entry.Category == "console" && strings.Contains(strings.ToLower(entry.Summary), "error") && len(report.Issues) < 20 {
			report.Issues = append(report.Issues, fmt.Sprintf("console action %d: %s", entry.Sequence, entry.Summary))
		}
	}
	start := len(timeline.Entries) - 20
	if start < 0 {
		start = 0
	}
	report.Recent = timeline.Entries[start:]
	return report
}

func sessionActionCategory(args []string) string {
	if len(args) == 0 {
		return "other"
	}
	switch args[0] {
	case "open", "goto", "reload", "go-back", "go-forward", "tab-new", "tab-close", "tab-select":
		return "navigation"
	case "click", "dblclick", "fill", "type", "press", "select", "check", "uncheck", "hover", "drag", "mouse":
		return "interaction"
	case "wait":
		return "wait"
	case "snapshot", "screenshot":
		return "evidence"
	case "console", "requests", "diagnose":
		return "console"
	case "checkpoint":
		return "checkpoint"
	default:
		return "other"
	}
}

func timelineActionSummary(action SessionActionRecord) string {
	if len(action.Args) > 1 && action.Args[0] == "checkpoint" {
		return action.Args[1]
	}
	output := compactCLIOutput(joinOutputs(action.Stdout, action.Stderr))
	if output == "" {
		return commandString(compactSessionArgs(action.Args))
	}
	return truncateTraceValue(output, 500)
}

func printSessionTimeline(out io.Writer, timeline SessionTimeline) {
	for _, entry := range timeline.Entries {
		fmt.Fprintf(out, "%d. %-11s %-10s %s", entry.Sequence, entry.Status, entry.Category, commandString(entry.Command))
		if entry.Summary != "" && entry.Summary != commandString(entry.Command) {
			fmt.Fprintf(out, " — %s", entry.Summary)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "%d actions, %d failures, %d snapshots, %d checkpoints\n", timeline.Actions, timeline.Failures, timeline.Snapshots, timeline.Checkpoints)
}
