package cli

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const traceUsage = `Inspect or open a Playwright trace

Usage:
  heimdal trace [--dir PATH] [--run ID] [TRACE]
  heimdal trace [--dir PATH] [--run ID] [TRACE] --json
  heimdal trace inspect [--dir PATH] [--run ID] [TRACE] [--around-failure]

Options:
  --dir PATH  Discover the project from PATH
  --run ID    Resolve a retained trace from this run (default: latest)
  --json      Print bounded trace metadata without opening a viewer
  --around-failure
              Include two actions on either side of the failure (JSON only)
  --help      Print this help

Without --json, Heimdal opens Playwright's interactive trace viewer. With
--json, it reports the resolved path, run timing and failure, action counts,
the failing action with nearby actions, and snapshot/resource counts.
`

type traceOptions struct {
	Root          string
	RunID         string
	Trace         string
	JSON          bool
	Help          bool
	Inspect       bool
	AroundFailure int
}

type TraceActionSummary struct {
	Index       int     `json:"index"`
	APIName     string  `json:"api_name,omitempty"`
	Locator     string  `json:"locator,omitempty"`
	StartTimeMS float64 `json:"start_time_ms,omitempty"`
	EndTimeMS   float64 `json:"end_time_ms,omitempty"`
	DurationMS  float64 `json:"duration_ms,omitempty"`
	Error       string  `json:"error,omitempty"`
}

type TraceSummary struct {
	SchemaVersion int                    `json:"schema_version"`
	RunID         string                 `json:"run_id,omitempty"`
	TracePath     string                 `json:"trace_path"`
	SizeBytes     int64                  `json:"size_bytes"`
	RunStatus     string                 `json:"run_status,omitempty"`
	RunFailure    string                 `json:"run_failure,omitempty"`
	StartedAt     string                 `json:"started_at,omitempty"`
	FinishedAt    string                 `json:"finished_at,omitempty"`
	DurationMS    int64                  `json:"duration_ms,omitempty"`
	ActionCount   int                    `json:"action_count"`
	FailingAction *TraceActionSummary    `json:"failing_action,omitempty"`
	NearbyActions []TraceActionSummary   `json:"nearby_actions,omitempty"`
	Snapshots     []TraceSnapshotSummary `json:"snapshots,omitempty"`
	SnapshotCount int                    `json:"snapshot_count"`
	ResourceCount int                    `json:"resource_count"`
	TraceFiles    []string               `json:"trace_files,omitempty"`
	Artifacts     map[string]string      `json:"artifacts,omitempty"`
}

type TraceSnapshotSummary struct {
	Name    string `json:"name,omitempty"`
	CallID  string `json:"call_id,omitempty"`
	URL     string `json:"url,omitempty"`
	Excerpt string `json:"excerpt,omitempty"`
}

func resolveTrace(project Project, runID, tracePath string) (string, *RunResult, error) {
	if tracePath != "" {
		if !filepath.IsAbs(tracePath) {
			tracePath = filepath.Join(project.Root, tracePath)
		}
		resolved, err := filepath.Abs(tracePath)
		return filepath.Clean(resolved), nil, err
	}
	var result RunResult
	var err error
	if runID == "" {
		runID = "latest"
	}
	if runID == "latest" {
		result, err = findLatestResult(artifactRoot(project, ""))
	} else if runID == "latest-failed" {
		result, err = findLatestResultByStatus(artifactRoot(project, ""), "failed")
	} else {
		if !validArtifactID(runID) {
			return "", nil, errors.New("run id must contain only lowercase letters, numbers, and hyphens")
		}
		result, err = readResult(filepath.Join(artifactRoot(project, ""), runID, "result.json"))
	}
	if err != nil {
		return "", nil, err
	}
	tracePath, err = findTrace(result.Artifacts.RunDir)
	if err != nil {
		return "", nil, err
	}
	return tracePath, &result, nil
}

func summarizeTrace(tracePath string, result *RunResult, aroundFailure int) (TraceSummary, error) {
	info, err := os.Stat(tracePath)
	if err != nil {
		return TraceSummary{}, fmt.Errorf("inspect Playwright trace %s: %w", tracePath, err)
	}
	absolute, err := filepath.Abs(tracePath)
	if err != nil {
		return TraceSummary{}, err
	}
	summary := TraceSummary{SchemaVersion: 1, TracePath: filepath.Clean(absolute), SizeBytes: info.Size()}
	summary.Artifacts = map[string]string{"trace": summary.TracePath}
	if result != nil {
		summary.RunID = result.RunID
		summary.RunStatus = result.Status
		summary.RunFailure = result.Failure
		summary.StartedAt = result.StartedAt.Format("2006-01-02T15:04:05.999999999Z07:00")
		summary.FinishedAt = result.FinishedAt.Format("2006-01-02T15:04:05.999999999Z07:00")
		summary.DurationMS = result.DurationMS
		for name, path := range map[string]string{
			"stdout": result.Artifacts.Stdout, "stderr": result.Artifacts.Stderr,
			"result": result.Artifacts.Result, "test_output": result.Artifacts.TestOutput,
			"report": result.Artifacts.Report,
		} {
			if path != "" {
				summary.Artifacts[name] = path
			}
		}
	}
	collector := traceCollector{}
	if info.IsDir() {
		err = summarizeTraceDirectory(tracePath, &collector)
	} else if strings.HasSuffix(strings.ToLower(tracePath), ".zip") {
		err = summarizeTraceZip(tracePath, &collector)
	} else {
		var file *os.File
		file, err = os.Open(tracePath)
		if err == nil {
			defer file.Close()
			collector.traceFiles = append(collector.traceFiles, filepath.Base(tracePath))
			err = collector.readEvents(file)
		}
	}
	if err != nil {
		return TraceSummary{}, fmt.Errorf("summarize Playwright trace %s: %w", tracePath, err)
	}
	collector.finish(&summary, aroundFailure)
	return summary, nil
}

type traceCollector struct {
	actions       []collectedTraceAction
	byCallID      map[string]int
	snapshotCount int
	resourceCount int
	traceFiles    []string
	snapshots     []collectedTraceSnapshot
}

type collectedTraceAction struct {
	TraceActionSummary
	CallID string
}

type collectedTraceSnapshot struct {
	TraceSnapshotSummary
	Timestamp float64
}

func summarizeTraceZip(path string, collector *traceCollector) error {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer archive.Close()
	for _, entry := range archive.File {
		name := filepath.ToSlash(entry.Name)
		if entry.FileInfo().IsDir() {
			continue
		}
		if strings.HasPrefix(name, "resources/") || strings.Contains(name, "/resources/") {
			collector.resourceCount++
		}
		if !isTraceEventFile(name) {
			continue
		}
		collector.traceFiles = append(collector.traceFiles, name)
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		err = collector.readEvents(reader)
		_ = reader.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func summarizeTraceDirectory(path string, collector *traceCollector) error {
	return filepath.WalkDir(path, func(entryPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, _ := filepath.Rel(path, entryPath)
		name := filepath.ToSlash(relative)
		if strings.HasPrefix(name, "resources/") || strings.Contains(name, "/resources/") {
			collector.resourceCount++
		}
		if !isTraceEventFile(name) {
			return nil
		}
		collector.traceFiles = append(collector.traceFiles, name)
		file, err := os.Open(entryPath)
		if err != nil {
			return err
		}
		err = collector.readEvents(file)
		_ = file.Close()
		return err
	})
}

func isTraceEventFile(name string) bool {
	base := filepath.Base(name)
	return strings.HasSuffix(base, ".trace") || base == "trace"
}

func (collector *traceCollector) readEvents(reader io.Reader) error {
	if collector.byCallID == nil {
		collector.byCallID = make(map[string]int)
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var event traceEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		switch event.Type {
		case "frame-snapshot":
			collector.snapshotCount++
			collector.addSnapshot(event)
		case "before":
			collector.addAction(event)
		case "after":
			collector.finishAction(event)
		case "action":
			if len(event.Metadata) > 0 {
				var metadata traceEvent
				if json.Unmarshal(event.Metadata, &metadata) == nil {
					collector.addCompleteAction(metadata)
				}
			}
		}
	}
	return scanner.Err()
}

type traceEvent struct {
	Type      string          `json:"type"`
	CallID    string          `json:"callId"`
	ID        string          `json:"id"`
	APIName   string          `json:"apiName"`
	Class     string          `json:"class"`
	Method    string          `json:"method"`
	Title     string          `json:"title"`
	StartTime float64         `json:"startTime"`
	EndTime   float64         `json:"endTime"`
	Params    json.RawMessage `json:"params"`
	Error     json.RawMessage `json:"error"`
	Metadata  json.RawMessage `json:"metadata"`
	Snapshot  json.RawMessage `json:"snapshot"`
}

func (collector *traceCollector) addAction(event traceEvent) {
	callID := event.CallID
	if callID == "" {
		callID = event.ID
	}
	action := traceAction(event, len(collector.actions)+1)
	collector.actions = append(collector.actions, collectedTraceAction{TraceActionSummary: action, CallID: callID})
	if callID != "" {
		collector.byCallID[callID] = len(collector.actions) - 1
	}
}

func (collector *traceCollector) finishAction(event traceEvent) {
	index, ok := collector.byCallID[event.CallID]
	if !ok {
		return
	}
	action := &collector.actions[index].TraceActionSummary
	action.EndTimeMS = event.EndTime
	if action.StartTimeMS > 0 && action.EndTimeMS >= action.StartTimeMS {
		action.DurationMS = action.EndTimeMS - action.StartTimeMS
	}
	action.Error = traceError(event.Error)
}

func (collector *traceCollector) addCompleteAction(event traceEvent) {
	action := traceAction(event, len(collector.actions)+1)
	action.EndTimeMS = event.EndTime
	if action.StartTimeMS > 0 && action.EndTimeMS >= action.StartTimeMS {
		action.DurationMS = action.EndTimeMS - action.StartTimeMS
	}
	action.Error = traceError(event.Error)
	collector.actions = append(collector.actions, collectedTraceAction{TraceActionSummary: action, CallID: event.CallID})
}

func (collector *traceCollector) addSnapshot(event traceEvent) {
	var snapshot struct {
		CallID       string          `json:"callId"`
		SnapshotName string          `json:"snapshotName"`
		URL          string          `json:"url"`
		FrameURL     string          `json:"frameUrl"`
		Timestamp    float64         `json:"timestamp"`
		HTML         json.RawMessage `json:"html"`
	}
	if json.Unmarshal(event.Snapshot, &snapshot) != nil {
		return
	}
	url := snapshot.URL
	if url == "" {
		url = snapshot.FrameURL
	}
	collector.snapshots = append(collector.snapshots, collectedTraceSnapshot{
		TraceSnapshotSummary: TraceSnapshotSummary{
			Name:    snapshot.SnapshotName,
			CallID:  snapshot.CallID,
			URL:     url,
			Excerpt: traceSnapshotExcerpt(snapshot.HTML),
		},
		Timestamp: snapshot.Timestamp,
	})
}

func traceSnapshotExcerpt(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	seen := make(map[string]bool)
	parts := make([]string, 0, 16)
	length := 0
	var collect func(any)
	collect = func(current any) {
		if length >= 1200 {
			return
		}
		switch item := current.(type) {
		case string:
			item = strings.Join(strings.Fields(item), " ")
			if traceSnapshotNoise(item) || seen[item] {
				return
			}
			seen[item] = true
			if remaining := 1200 - length; len(item) > remaining {
				item = truncateTraceValue(item, remaining)
			}
			parts = append(parts, item)
			length += len(item) + 2
		case []any:
			for _, child := range item {
				collect(child)
			}
		case map[string]any:
			for _, child := range item {
				collect(child)
			}
		}
	}
	collect(value)
	return strings.Join(parts, " | ")
}

func traceSnapshotNoise(value string) bool {
	if value == "" || len(value) == 1 || strings.HasPrefix(value, "about:") || strings.HasPrefix(value, "__playwright_") {
		return true
	}
	letters := false
	for _, char := range value {
		if char >= 'a' && char <= 'z' {
			return false
		}
		if char >= 'A' && char <= 'Z' {
			letters = true
		}
	}
	return letters && len(value) <= 24
}

func traceAction(event traceEvent, index int) TraceActionSummary {
	name := event.APIName
	if name == "" {
		name = event.Title
	}
	if name == "" && event.Class != "" {
		name = event.Class
		if event.Method != "" {
			name += "." + event.Method
		}
	}
	return TraceActionSummary{
		Index:       index,
		APIName:     name,
		Locator:     traceLocator(event.Params),
		StartTimeMS: event.StartTime,
		Error:       traceError(event.Error),
	}
}

func traceLocator(raw json.RawMessage) string {
	var params map[string]json.RawMessage
	if json.Unmarshal(raw, &params) != nil {
		return ""
	}
	for _, key := range []string{"selector", "locator", "target"} {
		var value string
		if json.Unmarshal(params[key], &value) == nil && value != "" {
			return truncateTraceValue(value, 300)
		}
	}
	return ""
}

func traceError(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return truncateTraceValue(stripANSI(value), 500)
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		for _, key := range []string{"message", "value", "name"} {
			if json.Unmarshal(object[key], &value) == nil && value != "" {
				return truncateTraceValue(stripANSI(value), 500)
			}
		}
	}
	return truncateTraceValue(string(raw), 500)
}

func stripANSI(value string) string {
	var clean strings.Builder
	clean.Grow(len(value))
	for index := 0; index < len(value); {
		if value[index] != 0x1b || index+1 >= len(value) || value[index+1] != '[' {
			clean.WriteByte(value[index])
			index++
			continue
		}
		index += 2
		for index < len(value) {
			last := value[index]
			index++
			if last >= 0x40 && last <= 0x7e {
				break
			}
		}
	}
	return clean.String()
}

func truncateTraceValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

func (collector *traceCollector) finish(summary *TraceSummary, aroundFailure int) {
	sort.Strings(collector.traceFiles)
	sort.SliceStable(collector.actions, func(left, right int) bool {
		return collector.actions[left].StartTimeMS < collector.actions[right].StartTimeMS
	})
	for index := range collector.actions {
		collector.actions[index].Index = index + 1
	}
	summary.ActionCount = len(collector.actions)
	summary.SnapshotCount = collector.snapshotCount
	summary.ResourceCount = collector.resourceCount
	summary.TraceFiles = collector.traceFiles
	failure := -1
	for index := range collector.actions {
		if collector.actions[index].Error != "" && collector.actions[index].Locator != "" {
			failure = index
			break
		}
	}
	if failure < 0 {
		for index := range collector.actions {
			if collector.actions[index].Error != "" {
				failure = index
				break
			}
		}
	}
	if failure < 0 {
		return
	}
	failing := collector.actions[failure]
	for _, action := range collector.actions {
		overlaps := action.StartTimeMS <= failing.EndTimeMS+10 && action.EndTimeMS >= failing.StartTimeMS-10
		if overlaps && len(action.Error) > len(failing.Error) {
			failing.Error = action.Error
		}
	}
	summary.FailingAction = &failing.TraceActionSummary
	if aroundFailure < 0 {
		aroundFailure = 0
	}
	start := failure - aroundFailure
	if start < 0 {
		start = 0
	}
	end := failure + aroundFailure + 1
	if end > len(collector.actions) {
		end = len(collector.actions)
	}
	for _, action := range collector.actions[start:end] {
		summary.NearbyActions = append(summary.NearbyActions, action.TraceActionSummary)
	}
	failingCallID := failing.CallID
	for _, snapshot := range collector.snapshots {
		matchesCall := failingCallID != "" && (snapshot.CallID == failingCallID || strings.Contains(snapshot.Name, failingCallID))
		nearTime := snapshot.Timestamp >= failing.StartTimeMS-100 && snapshot.Timestamp <= failing.EndTimeMS+100
		if (!matchesCall && !nearTime) || snapshot.Excerpt == "" {
			continue
		}
		summary.Snapshots = append(summary.Snapshots, snapshot.TraceSnapshotSummary)
		if len(summary.Snapshots) == 3 {
			break
		}
	}
	if len(summary.Snapshots) < 3 {
		for _, snapshot := range collector.snapshots {
			if snapshot.Excerpt == "" || traceSnapshotIncluded(summary.Snapshots, snapshot.TraceSnapshotSummary) {
				continue
			}
			summary.Snapshots = append(summary.Snapshots, snapshot.TraceSnapshotSummary)
			if len(summary.Snapshots) == 3 {
				break
			}
		}
	}
}

func traceSnapshotIncluded(snapshots []TraceSnapshotSummary, candidate TraceSnapshotSummary) bool {
	for _, snapshot := range snapshots {
		if snapshot.Name == candidate.Name && snapshot.CallID == candidate.CallID {
			return true
		}
	}
	return false
}
