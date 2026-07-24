package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const defaultSessionInventoryLimit = 8

const sessionsUsage = `Discover and clean up persistent Heimdal sessions

Usage:
  heimdal sessions list [--dir PATH] [--status STATUS] [--limit N] [--json]
  heimdal sessions prune [--dir PATH] [--dry-run] [--limit N] [--json]

List probes each indexed worktree's Playwright workspace and distinguishes
active, stopped, stale, unknown, and broken state. Prune finalizes stale state
and removes stale or broken global indexes while preserving session evidence.
Lists return 8 matches by default; filter or raise --limit for older rows.
The singular aliases heimdal session list and heimdal session prune are also
accepted.
`

type sessionsOptions struct {
	Root   string
	Status string
	DryRun bool
	JSON   bool
	Help   bool
	Limit  int
}

type sessionIndexRecord struct {
	Index sessionIndex
	Path  string
	Err   error
}

type SessionInventoryItem struct {
	Name          string     `json:"name"`
	Group         string     `json:"group,omitempty"`
	Actor         string     `json:"actor,omitempty"`
	RunID         string     `json:"run_id,omitempty"`
	Root          string     `json:"root,omitempty"`
	Status        string     `json:"status"`
	Reason        string     `json:"reason,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at,omitempty"`
	StartedAt     time.Time  `json:"started_at,omitempty"`
	StoppedAt     *time.Time `json:"stopped_at,omitempty"`
	ServerPID     int        `json:"server_pid,omitempty"`
	ActionCount   int        `json:"action_count,omitempty"`
	EvidenceBytes int64      `json:"evidence_bytes,omitempty"`
	StatePath     string     `json:"state_path,omitempty"`
	Artifacts     string     `json:"artifacts,omitempty"`
	indexPath     string
	state         *SessionState
}

type SessionsResult struct {
	SchemaVersion  int                    `json:"schema_version"`
	Status         string                 `json:"status"`
	DryRun         bool                   `json:"dry_run,omitempty"`
	Matched        int                    `json:"matched"`
	Returned       int                    `json:"returned"`
	Omitted        int                    `json:"omitted,omitempty"`
	Active         int                    `json:"active"`
	Stopped        int                    `json:"stopped"`
	Stale          int                    `json:"stale"`
	Unknown        int                    `json:"unknown"`
	Broken         int                    `json:"broken"`
	Candidates     int                    `json:"candidates,omitempty"`
	Pruned         int                    `json:"pruned,omitempty"`
	EvidenceBytes  int64                  `json:"evidence_bytes,omitempty"`
	CandidateBytes int64                  `json:"candidate_evidence_bytes,omitempty"`
	Sessions       []SessionInventoryItem `json:"sessions"`
}

type browserInventory struct {
	available bool
	names     map[string]bool
}

func runSessions(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Fprint(out, sessionsUsage)
		return 0
	}
	command := args[0]
	options, err := parseSessionsOptions(args[1:])
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if options.Help {
		fmt.Fprint(out, sessionsUsage)
		return 0
	}
	if command != "list" && command != "prune" {
		return reportError(options.JSON, fmt.Errorf("unknown sessions command %q", command), out, errOut)
	}
	if command == "list" && options.DryRun {
		return reportError(options.JSON, errors.New("sessions list does not accept --dry-run"), out, errOut)
	}
	if command == "prune" && options.Status != "" {
		return reportError(options.JSON, errors.New("sessions prune does not accept --status"), out, errOut)
	}
	root, err := resolveSessionInventoryRoot(options.Root)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	result, err := inspectSessionInventory(root, options.Status)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if command == "prune" {
		result.DryRun = options.DryRun
		if err := pruneSessionInventory(&result, options.DryRun, time.Now().UTC()); err != nil {
			return reportError(options.JSON, err, out, errOut)
		}
	}
	limitSessionInventory(&result, options.Limit, command == "prune")
	if options.JSON {
		if err := writeJSONTo(out, result); err != nil {
			return reportError(true, err, out, errOut)
		}
		return 0
	}
	printSessionInventory(out, result, command)
	return 0
}

func parseSessionsOptions(args []string) (sessionsOptions, error) {
	options := sessionsOptions{}
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--dir", "--root":
			flag := args[index]
			value, next, err := nextValue(args, index, flag)
			if err != nil {
				return options, err
			}
			index = next
			if err := setDirectoryOption(&options.Root, value, flag); err != nil {
				return options, err
			}
		case "--status":
			value, next, err := nextValue(args, index, "--status")
			if err != nil {
				return options, err
			}
			index, options.Status = next, strings.ToLower(value)
			if !validSessionInventoryStatus(options.Status) {
				return options, fmt.Errorf("unknown session status %q", value)
			}
		case "--limit":
			value, next, err := nextValue(args, index, "--limit")
			if err != nil {
				return options, err
			}
			index = next
			options.Limit, err = parsePositiveSessionSequence("--limit", value)
			if err != nil {
				return options, err
			}
			if options.Limit > 200 {
				return options, errors.New("--limit must not exceed 200")
			}
		case "--dry-run":
			options.DryRun = true
		case "--json":
			options.JSON = true
		case "--help", "-h":
			options.Help = true
		default:
			return options, fmt.Errorf("unknown option %q", args[index])
		}
	}
	return options, nil
}

func validSessionInventoryStatus(status string) bool {
	switch status {
	case "active", "stopped", "stale", "unknown", "broken":
		return true
	default:
		return false
	}
}

func resolveSessionInventoryRoot(root string) (string, error) {
	if root == "" {
		return "", nil
	}
	project, err := Discover(root)
	if err != nil {
		return "", err
	}
	return project.Root, nil
}

func readAllSessionIndexRecords() ([]sessionIndexRecord, error) {
	directory, err := sessionRegistryDirectory()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Heimdal session state directory: %w", err)
	}
	var records []sessionIndexRecord
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(directory, entry.Name())
		contents, readErr := os.ReadFile(path)
		record := sessionIndexRecord{Path: path}
		if readErr != nil {
			record.Err = readErr
		} else if err := json.Unmarshal(contents, &record.Index); err != nil || record.Index.SchemaVersion != 1 || record.Index.Name == "" || record.Index.Root == "" || record.Index.StatePath == "" {
			record.Err = errors.New("invalid session index")
		}
		records = append(records, record)
	}
	return records, nil
}

func inspectSessionInventory(root, status string) (SessionsResult, error) {
	result := SessionsResult{SchemaVersion: 1, Status: "passed", Sessions: []SessionInventoryItem{}}
	records, err := readAllSessionIndexRecords()
	if err != nil {
		return result, err
	}
	runtimes := map[string]browserInventory{}
	for _, record := range records {
		item := inspectSessionIndex(record, runtimes)
		if root != "" && filepath.Clean(item.Root) != filepath.Clean(root) {
			continue
		}
		if status != "" && item.Status != status {
			continue
		}
		result.Sessions = append(result.Sessions, item)
		result.EvidenceBytes += item.EvidenceBytes
		switch item.Status {
		case "active":
			result.Active++
		case "stopped":
			result.Stopped++
		case "stale":
			result.Stale++
		case "unknown":
			result.Unknown++
		case "broken":
			result.Broken++
		}
	}
	sort.Slice(result.Sessions, func(left, right int) bool {
		return result.Sessions[left].UpdatedAt.After(result.Sessions[right].UpdatedAt)
	})
	result.Matched = len(result.Sessions)
	result.Returned = result.Matched
	return result, nil
}

func limitSessionInventory(result *SessionsResult, limit int, candidatesOnly bool) {
	if candidatesOnly {
		filtered := result.Sessions[:0]
		for _, item := range result.Sessions {
			if item.Status == "stale" || item.Status == "broken" {
				filtered = append(filtered, item)
			}
		}
		result.Sessions = filtered
	}
	if limit == 0 {
		limit = defaultSessionInventoryLimit
	}
	result.Returned = len(result.Sessions)
	if len(result.Sessions) > limit {
		result.Omitted = len(result.Sessions) - limit
		result.Sessions = result.Sessions[:limit]
		result.Returned = limit
	}
}

func inspectSessionIndex(record sessionIndexRecord, runtimes map[string]browserInventory) SessionInventoryItem {
	item := SessionInventoryItem{Name: record.Index.Name, Root: record.Index.Root, UpdatedAt: record.Index.UpdatedAt, Status: "broken", indexPath: record.Path}
	if record.Err != nil {
		item.Name = strings.TrimSuffix(filepath.Base(record.Path), ".json")
		item.Reason = record.Err.Error()
		return item
	}
	item.StatePath = record.Index.StatePath
	state, err := readSessionState(record.Index.StatePath)
	if err != nil {
		item.Reason = err.Error()
		return item
	}
	item.state = &state
	item.Name, item.Group, item.Actor, item.RunID = state.Name, state.Group, state.Actor, state.RunID
	item.Root, item.StartedAt, item.StoppedAt = state.Root, state.StartedAt, state.StoppedAt
	item.ServerPID, item.ActionCount, item.Artifacts = state.ServerPID, state.ActionCount, state.SessionDir
	item.EvidenceBytes = directoryBytes(state.SessionDir)
	if state.StoppedAt != nil {
		item.Status = "stopped"
		return item
	}
	runner := sessionAgentRunner(state)
	key := state.Root + "\x00" + strings.Join(runner, "\x00")
	runtime, exists := runtimes[key]
	if !exists {
		runtime = inspectBrowserInventory(state, runner)
		runtimes[key] = runtime
	}
	item.Status, item.Reason = classifySessionRuntime(state, runtime)
	return item
}

func classifySessionRuntime(state SessionState, runtime browserInventory) (string, string) {
	if state.StoppedAt != nil {
		return "stopped", ""
	}
	if state.ServerPID > 0 && sessionServerStatus(state) != "running" {
		return "stale", "owned app process is not running"
	}
	if runtime.available {
		if runtime.names[state.Name] {
			return "active", ""
		}
		return "stale", "Playwright browser is not running in this worktree"
	}
	if state.ServerPID > 0 && sessionServerStatus(state) == "running" {
		return "active", "browser inventory unavailable; owned app is running"
	}
	return "unknown", "Playwright browser inventory is unavailable"
}

func sessionAgentRunner(state SessionState) []string {
	if state.ProjectCache != nil && len(state.ProjectCache.AgentRunner) > 0 {
		return append([]string(nil), state.ProjectCache.AgentRunner...)
	}
	project, err := Discover(state.Root)
	if err != nil {
		return nil
	}
	return append([]string(nil), project.AgentRunner...)
}

func inspectBrowserInventory(state SessionState, runner []string) browserInventory {
	if len(runner) == 0 {
		return browserInventory{}
	}
	command := append(append([]string(nil), runner...), "--json", "list")
	output, err := runCapture(state.Root, command, baseEnvironment())
	if err != nil {
		return browserInventory{}
	}
	var payload struct {
		Browsers []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"browsers"`
	}
	if json.Unmarshal([]byte(output), &payload) != nil {
		return browserInventory{}
	}
	result := browserInventory{available: true, names: map[string]bool{}}
	for _, browser := range payload.Browsers {
		if browser.Status == "open" {
			result.names[browser.Name] = true
		}
	}
	return result
}

func pruneSessionInventory(result *SessionsResult, dryRun bool, now time.Time) error {
	for index := range result.Sessions {
		item := &result.Sessions[index]
		if item.Status != "stale" && item.Status != "broken" {
			continue
		}
		result.Candidates++
		result.CandidateBytes += item.EvidenceBytes
		if dryRun {
			continue
		}
		if item.state != nil {
			state := *item.state
			state.StoppedAt = &now
			state.ServerPID = 0
			if err := writeSessionState(item.StatePath, state); err != nil {
				return fmt.Errorf("finalize stale session %q: %w", item.Name, err)
			}
			item.StoppedAt = &now
		}
		if err := os.Remove(item.indexPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale session index %s: %w", item.indexPath, err)
		}
		result.Pruned++
	}
	return nil
}

func printSessionInventory(out io.Writer, result SessionsResult, command string) {
	verb := "found"
	if command == "prune" {
		verb = "pruned"
		if result.DryRun {
			verb = "would prune"
		}
	}
	count := result.Pruned
	if command == "list" {
		count = result.Matched
	} else if result.DryRun {
		count = result.Candidates
	}
	fmt.Fprintf(out, "Heimdal sessions: %s %d of %d indexed sessions\n", verb, count, result.Matched)
	if command == "list" {
		fmt.Fprintf(out, "  active %d, stopped %d, stale %d, unknown %d, broken %d\n", result.Active, result.Stopped, result.Stale, result.Unknown, result.Broken)
	}
	for _, item := range result.Sessions {
		fmt.Fprintf(out, "  %s  %-8s  %s", item.UpdatedAt.Format(time.RFC3339), item.Status, item.Name)
		if item.Root != "" {
			fmt.Fprintf(out, "  %s", item.Root)
		}
		if item.Reason != "" {
			fmt.Fprintf(out, " — %s", item.Reason)
		}
		fmt.Fprintln(out)
	}
	if result.Omitted > 0 {
		fmt.Fprintf(out, "  … %d more sessions omitted; increase --limit or filter by status\n", result.Omitted)
	}
}
