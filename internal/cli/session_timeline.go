package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SessionTimelineEntry struct {
	Sequence           int       `json:"sequence"`
	Actor              string    `json:"actor,omitempty"`
	ActorSequence      int       `json:"actor_sequence,omitempty"`
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

type SessionPhaseSummary struct {
	Label      string `json:"label"`
	From       int    `json:"from"`
	To         int    `json:"to"`
	Actions    int    `json:"actions"`
	Failures   int    `json:"failures"`
	Evidence   int    `json:"evidence"`
	DurationMS int64  `json:"duration_ms"`
}

type SessionTimelineFilter struct {
	Failures bool   `json:"failures,omitempty"`
	From     int    `json:"from,omitempty"`
	To       int    `json:"to,omitempty"`
	Category string `json:"category,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type SessionTimeline struct {
	SchemaVersion int                    `json:"schema_version"`
	Session       string                 `json:"session"`
	Group         string                 `json:"group,omitempty"`
	Actors        []string               `json:"actors,omitempty"`
	RunID         string                 `json:"run_id"`
	Root          string                 `json:"root"`
	URL           string                 `json:"url,omitempty"`
	StartedAt     time.Time              `json:"started_at"`
	StoppedAt     *time.Time             `json:"stopped_at,omitempty"`
	Actions       int                    `json:"actions"`
	Failures      int                    `json:"failures"`
	Snapshots     int                    `json:"snapshots"`
	Checkpoints   int                    `json:"checkpoints"`
	PhaseCount    int                    `json:"phase_count"`
	Phases        []SessionPhaseSummary  `json:"phases,omitempty"`
	Matched       int                    `json:"matched"`
	Returned      int                    `json:"returned"`
	Truncated     bool                   `json:"truncated,omitempty"`
	NextFrom      int                    `json:"next_from,omitempty"`
	Filter        *SessionTimelineFilter `json:"filter,omitempty"`
	Entries       []SessionTimelineEntry `json:"entries"`
}

type SessionReport struct {
	SchemaVersion  int                    `json:"schema_version"`
	Session        string                 `json:"session"`
	Group          string                 `json:"group,omitempty"`
	Actors         []string               `json:"actors,omitempty"`
	RunID          string                 `json:"run_id"`
	Status         string                 `json:"status"`
	DurationMS     int64                  `json:"duration_ms"`
	Actions        int                    `json:"actions"`
	Failures       int                    `json:"failures"`
	Snapshots      int                    `json:"snapshots"`
	Checkpoints    int                    `json:"checkpoints"`
	PhaseCount     int                    `json:"phase_count"`
	Phases         []SessionPhaseSummary  `json:"phases,omitempty"`
	Categories     map[string]int         `json:"categories"`
	Issues         []string               `json:"issues,omitempty"`
	FailureEntries []SessionTimelineEntry `json:"failure_entries,omitempty"`
	Recent         []SessionTimelineEntry `json:"recent"`
	Artifacts      map[string]string      `json:"artifacts"`
}

type sessionViewOptions struct {
	SessionOptions
	FailuresOnly bool
	From         int
	To           int
	Category     string
	Limit        int
	Explicit     bool
}

func runSessionTimeline(args []string, out, errOut io.Writer) int {
	options, err := parseSessionViewOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	_, state, _, err := discoverSession(options.SessionOptions)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	timeline, err := buildSessionTimeline(state)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	view := applySessionTimelineView(timeline, options)
	if options.JSON {
		_ = writeJSONTo(out, view)
	} else {
		printSessionTimeline(out, view)
	}
	return 0
}

func runSessionReport(args []string, out, errOut io.Writer) int {
	options, err := parseSessionViewOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	_, state, _, err := discoverSession(options.SessionOptions)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	timeline, err := buildSessionTimeline(state)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	report := summarizeSessionTimelineView(state, timeline, options)
	if options.JSON {
		_ = writeJSONTo(out, report)
	} else {
		fmt.Fprintf(out, "Heimdal session %s: %s; %d actions, %d failures, %d snapshots, %d checkpoints\n", report.Session, report.Status, report.Actions, report.Failures, report.Snapshots, report.Checkpoints)
		for _, phase := range report.Phases {
			fmt.Fprintf(out, "  phase %d-%d: %s (%d actions, %d failures)\n", phase.From, phase.To, phase.Label, phase.Actions, phase.Failures)
		}
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

func parseSessionViewOptions(args []string) (sessionViewOptions, error) {
	options := sessionViewOptions{}
	var common []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		value := func() (string, error) {
			if index+1 >= len(args) {
				return "", fmt.Errorf("%s requires a value", arg)
			}
			index++
			return args[index], nil
		}
		switch arg {
		case "--failures":
			options.FailuresOnly, options.Explicit = true, true
		case "--from", "--to", "--limit", "--category":
			raw, err := value()
			if err != nil {
				return options, err
			}
			options.Explicit = true
			switch arg {
			case "--category":
				options.Category = raw
			case "--from":
				options.From, err = parsePositiveSessionSequence(arg, raw)
			case "--to":
				options.To, err = parsePositiveSessionSequence(arg, raw)
			case "--limit":
				options.Limit, err = parsePositiveSessionSequence(arg, raw)
				if options.Limit > 200 {
					err = errors.New("--limit must not exceed 200")
				}
			}
			if err != nil {
				return options, err
			}
		default:
			common = append(common, arg)
		}
	}
	parsed, err := parseSessionOptions(common)
	options.SessionOptions = parsed
	if err != nil {
		return options, err
	}
	if options.From > 0 && options.To > 0 && options.From > options.To {
		return options, errors.New("--from must not exceed --to")
	}
	if options.Category != "" && !validSessionTimelineCategory(options.Category) {
		return options, fmt.Errorf("unknown timeline category %q", options.Category)
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

func parsePositiveSessionSequence(flag, raw string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("%s must be a positive integer (got %q)", flag, raw)
	}
	return value, nil
}

func validSessionTimelineCategory(category string) bool {
	switch category {
	case "navigation", "interaction", "wait", "evidence", "assertion", "console", "checkpoint", "other":
		return true
	default:
		return false
	}
}

func buildSessionTimeline(state SessionState) (SessionTimeline, error) {
	actions, err := readSessionActions(state.ActionLog)
	if err != nil {
		return SessionTimeline{}, err
	}
	timeline := SessionTimeline{SchemaVersion: 1, Session: state.Name, Group: state.Group, RunID: state.RunID, Root: state.Root, URL: state.URL, StartedAt: state.StartedAt, StoppedAt: state.StoppedAt, Actions: len(actions), Entries: make([]SessionTimelineEntry, 0, len(actions))}
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
	timeline.Phases = summarizeSessionPhases(timeline.Entries)
	timeline.PhaseCount = len(timeline.Phases)
	timeline.Matched = len(timeline.Entries)
	timeline.Returned = len(timeline.Entries)
	return timeline, nil
}

func summarizeSessionTimeline(state SessionState, timeline SessionTimeline) SessionReport {
	return summarizeSessionTimelineView(state, timeline, sessionViewOptions{})
}

func summarizeSessionTimelineView(state SessionState, timeline SessionTimeline, options sessionViewOptions) SessionReport {
	finished := time.Now().UTC()
	status := "active"
	if state.StoppedAt != nil {
		finished, status = *state.StoppedAt, "stopped"
	}
	if timeline.Failures > 0 {
		status = "issues"
	}
	report := SessionReport{SchemaVersion: 1, Session: state.Name, Group: state.Group, RunID: state.RunID, Status: status, DurationMS: finished.Sub(state.StartedAt).Milliseconds(), Actions: timeline.Actions, Failures: timeline.Failures, Snapshots: timeline.Snapshots, Checkpoints: timeline.Checkpoints, PhaseCount: len(timeline.Phases), Categories: map[string]int{}, Artifacts: map[string]string{"directory": state.SessionDir, "actions": state.ActionLog}}
	for _, entry := range timeline.Entries {
		report.Categories[entry.Category]++
		if entry.Status == "failed" && len(report.Issues) < 12 {
			actor := ""
			if entry.Actor != "" {
				actor = " actor " + entry.Actor
			}
			report.Issues = append(report.Issues, truncateTraceValue(fmt.Sprintf("action %d%s (%s): %s", entry.Sequence, actor, commandString(entry.Command), entry.Summary), 240))
		}
		if entry.Status != "failed" && entry.Category == "console" && len(report.Issues) < 12 {
			for _, issue := range timelineDiagnosticIssues(entry) {
				report.Issues = appendUnique(report.Issues, truncateTraceValue(fmt.Sprintf("console action %d: %s", entry.Sequence, issue), 240))
			}
		}
	}
	if len(report.Issues) > 0 {
		report.Status = "issues"
	}
	report.Phases = compactSessionPhases(timeline.Phases, 12)
	for index := len(timeline.Entries) - 1; index >= 0 && len(report.FailureEntries) < 12; index-- {
		if timeline.Entries[index].Status == "failed" {
			report.FailureEntries = append(report.FailureEntries, compactSessionTimelineEntry(timeline.Entries[index], options.FullJSON))
		}
	}
	reverseTimelineEntries(report.FailureEntries)
	limit := options.Limit
	if limit == 0 || limit > 20 {
		limit = 8
	}
	viewOptions := options
	viewOptions.Limit = limit
	if options.Explicit {
		report.Recent = applySessionTimelineView(timeline, viewOptions).Entries
	} else {
		report.Recent = recentMeaningfulSessionEntries(timeline.Entries, limit, options.FullJSON)
	}
	return report
}

func applySessionTimelineView(timeline SessionTimeline, options sessionViewOptions) SessionTimeline {
	view := timeline
	view.Entries = nil
	view.NextFrom = 0
	view.Filter = nil
	view.Phases = compactSessionPhases(timeline.Phases, 12)
	if options.FullJSON && !options.Explicit {
		view.Entries = append([]SessionTimelineEntry(nil), timeline.Entries...)
		view.Phases = append([]SessionPhaseSummary(nil), timeline.Phases...)
		view.Matched, view.Returned, view.Truncated = len(view.Entries), len(view.Entries), false
		return view
	}
	if !options.Explicit {
		view.Entries = defaultSessionTimelineEntries(timeline.Entries, options.FullJSON)
		view.Matched, view.Returned = len(timeline.Entries), len(view.Entries)
		view.Truncated = view.Returned < view.Matched
		return view
	}
	filter := &SessionTimelineFilter{Failures: options.FailuresOnly, From: options.From, To: options.To, Category: options.Category, Limit: options.Limit}
	limit := options.Limit
	if limit == 0 {
		limit = 50
		filter.Limit = limit
	}
	var matched []SessionTimelineEntry
	for _, entry := range timeline.Entries {
		if options.FailuresOnly && entry.Status != "failed" {
			continue
		}
		if options.From > 0 && entry.Sequence < options.From {
			continue
		}
		if options.To > 0 && entry.Sequence > options.To {
			continue
		}
		if options.Category != "" && entry.Category != options.Category {
			continue
		}
		matched = append(matched, entry)
	}
	view.Matched = len(matched)
	if len(matched) > limit {
		view.NextFrom = matched[limit].Sequence
		matched = matched[:limit]
	}
	view.Entries = make([]SessionTimelineEntry, 0, len(matched))
	for _, entry := range matched {
		view.Entries = append(view.Entries, compactSessionTimelineEntry(entry, options.FullJSON))
	}
	view.Returned = len(view.Entries)
	view.Truncated = view.Returned < view.Matched
	view.Filter = filter
	if options.From > 0 || options.To > 0 {
		view.Phases = sessionPhasesInRange(timeline.Phases, options.From, options.To, 20)
	}
	return view
}

func defaultSessionTimelineEntries(entries []SessionTimelineEntry, full bool) []SessionTimelineEntry {
	selected := map[int]SessionTimelineEntry{}
	collectRecent := func(limit int, include func(SessionTimelineEntry) bool) {
		for index := len(entries) - 1; index >= 0 && limit > 0; index-- {
			entry := entries[index]
			if !include(entry) {
				continue
			}
			if _, exists := selected[entry.Sequence]; !exists {
				selected[entry.Sequence] = compactSessionTimelineEntry(entry, full)
				limit--
			}
		}
	}
	collectRecent(20, func(entry SessionTimelineEntry) bool { return entry.Status == "failed" })
	collectRecent(12, meaningfulSessionTimelineEntry)
	if len(selected) == 0 && len(entries) > 0 {
		entry := entries[len(entries)-1]
		selected[entry.Sequence] = compactSessionTimelineEntry(entry, full)
	}
	result := make([]SessionTimelineEntry, 0, len(selected))
	for _, entry := range selected {
		result = append(result, entry)
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Sequence < result[right].Sequence })
	return result
}

func recentMeaningfulSessionEntries(entries []SessionTimelineEntry, limit int, full bool) []SessionTimelineEntry {
	var result []SessionTimelineEntry
	for index := len(entries) - 1; index >= 0 && len(result) < limit; index-- {
		if entries[index].Status != "failed" && meaningfulSessionTimelineEntry(entries[index]) {
			result = append(result, compactSessionTimelineEntry(entries[index], full))
		}
	}
	reverseTimelineEntries(result)
	return result
}

func meaningfulSessionTimelineEntry(entry SessionTimelineEntry) bool {
	if entry.Status == "failed" {
		return true
	}
	switch entry.Category {
	case "navigation", "interaction", "wait", "assertion", "checkpoint":
		return true
	case "console":
		return len(timelineDiagnosticIssues(entry)) > 0
	default:
		return false
	}
}

func timelineDiagnosticIssues(entry SessionTimelineEntry) []string {
	issues := diagnosticIssues(entry.Summary)
	lower := strings.ToLower(entry.Summary)
	if len(issues) == 0 && !strings.Contains(lower, "none") && (strings.Contains(entry.Summary, "[ERROR]") || strings.Contains(lower, "console error:") && strings.Contains(lower, " entries")) {
		issues = append(issues, "console errors detected")
	}
	return issues
}

func compactSessionTimelineEntry(entry SessionTimelineEntry, full bool) SessionTimelineEntry {
	if full {
		return entry
	}
	entry.Summary = truncateTraceValue(entry.Summary, 200)
	entry.Locator = truncateTraceValue(entry.Locator, 160)
	entry.Snapshot = ""
	if len(entry.Command) > 8 {
		entry.Command = append(append([]string(nil), entry.Command[:8]...), "…")
	} else {
		entry.Command = append([]string(nil), entry.Command...)
	}
	for index := range entry.Command {
		entry.Command[index] = truncateTraceValue(entry.Command[index], 120)
	}
	return entry
}

func reverseTimelineEntries(entries []SessionTimelineEntry) {
	for left, right := 0, len(entries)-1; left < right; left, right = left+1, right-1 {
		entries[left], entries[right] = entries[right], entries[left]
	}
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
	case "snapshot", "screenshot", "measure":
		return "evidence"
	case "expect":
		return "assertion"
	case "console", "requests", "diagnose":
		return "console"
	case "checkpoint":
		return "checkpoint"
	default:
		return "other"
	}
}

func summarizeSessionPhases(entries []SessionTimelineEntry) []SessionPhaseSummary {
	if len(entries) == 0 {
		return nil
	}
	var phases []SessionPhaseSummary
	start := 0
	for index := 1; index <= len(entries); index++ {
		boundary := index == len(entries)
		if !boundary {
			category := entries[index].Category
			boundary = category == "checkpoint" || category == "navigation"
		}
		if !boundary {
			continue
		}
		phaseEntries := entries[start:index]
		if len(phaseEntries) > 0 {
			first, last := phaseEntries[0], phaseEntries[len(phaseEntries)-1]
			label := first.Summary
			if label == "" {
				label = commandString(first.Command)
			}
			phase := SessionPhaseSummary{Label: truncateTraceValue(label, 120), From: first.Sequence, To: last.Sequence, Actions: len(phaseEntries)}
			phase.DurationMS = last.StartedAt.Add(time.Duration(last.DurationMS) * time.Millisecond).Sub(first.StartedAt).Milliseconds()
			for _, entry := range phaseEntries {
				if entry.Status == "failed" {
					phase.Failures++
				}
				if entry.Category == "evidence" {
					phase.Evidence++
				}
			}
			phases = append(phases, phase)
		}
		start = index
	}
	return phases
}

func compactSessionPhases(phases []SessionPhaseSummary, limit int) []SessionPhaseSummary {
	if len(phases) <= limit {
		return append([]SessionPhaseSummary(nil), phases...)
	}
	return append([]SessionPhaseSummary(nil), phases[len(phases)-limit:]...)
}

func sessionPhasesInRange(phases []SessionPhaseSummary, from, to, limit int) []SessionPhaseSummary {
	var selected []SessionPhaseSummary
	for _, phase := range phases {
		if from > 0 && phase.To < from {
			continue
		}
		if to > 0 && phase.From > to {
			continue
		}
		selected = append(selected, phase)
	}
	return compactSessionPhases(selected, limit)
}

func timelineActionSummary(action SessionActionRecord) string {
	if len(action.Args) > 1 && action.Args[0] == "checkpoint" {
		return action.Args[1]
	}
	raw := joinOutputs(action.Stdout, action.Stderr)
	if action.ExitCode != 0 && (strings.Contains(raw, "Usage: playwright-cli") || strings.Contains(raw, "Unknown command") || strings.Contains(raw, "Unknown option") || strings.Contains(raw, "playwright-cli ")) {
		return compactSessionGrammarOutput(raw)
	}
	output := compactCLIOutput(raw)
	if output == "" {
		return commandString(compactSessionArgs(action.Args))
	}
	if strings.Contains(output, "Usage: playwright-cli") || strings.Contains(output, "Unknown command") {
		return compactSessionGrammarOutput(output)
	}
	return truncateTraceValue(output, 500)
}

func printSessionTimeline(out io.Writer, timeline SessionTimeline) {
	for _, phase := range timeline.Phases {
		fmt.Fprintf(out, "phase %d-%d: %s (%d actions, %d failures)\n", phase.From, phase.To, phase.Label, phase.Actions, phase.Failures)
	}
	for _, entry := range timeline.Entries {
		actor := ""
		if entry.Actor != "" {
			actor = "[" + entry.Actor + "] "
		}
		fmt.Fprintf(out, "%d. %s%-11s %-10s %s", entry.Sequence, actor, entry.Status, entry.Category, commandString(entry.Command))
		if entry.Summary != "" && entry.Summary != commandString(entry.Command) {
			fmt.Fprintf(out, " — %s", entry.Summary)
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "%d/%d actions shown, %d failures, %d snapshots, %d checkpoints", timeline.Returned, timeline.Actions, timeline.Failures, timeline.Snapshots, timeline.Checkpoints)
	if timeline.NextFrom > 0 {
		fmt.Fprintf(out, "; continue with --from %d", timeline.NextFrom)
	}
	fmt.Fprintln(out)
}
