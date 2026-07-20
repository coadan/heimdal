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

type runsOptions struct {
	Root     string
	JSON     bool
	FullJSON bool
	Status   string
	Since    time.Duration
	SinceSet bool
	Test     string
	Limit    int
	Remove   bool
}

type RunInventoryItem struct {
	RunID          string          `json:"run_id"`
	Status         string          `json:"status"`
	Branch         string          `json:"branch,omitempty"`
	StartedAt      time.Time       `json:"started_at"`
	FinishedAt     time.Time       `json:"finished_at,omitempty"`
	DurationMS     int64           `json:"duration_ms,omitempty"`
	SizeBytes      int64           `json:"size_bytes"`
	Pinned         bool            `json:"pinned"`
	Interrupted    bool            `json:"interrupted"`
	Invocation     RunInvocation   `json:"invocation"`
	Tests          *TestCounts     `json:"tests,omitempty"`
	PrimaryFailure *PrimaryFailure `json:"primary_failure,omitempty"`
}

type RunFailureGroup struct {
	Fingerprint string    `json:"fingerprint"`
	Message     string    `json:"message"`
	Count       int       `json:"count"`
	LatestAt    time.Time `json:"latest_at"`
	LatestRunID string    `json:"latest_run_id"`
}

type RunsListResult struct {
	SchemaVersion int                `json:"schema_version"`
	ArtifactRoot  string             `json:"artifact_root"`
	Matched       int                `json:"matched"`
	Runs          []RunInventoryItem `json:"runs"`
	FailureGroups []RunFailureGroup  `json:"failure_groups,omitempty"`
}

type RunComparison struct {
	SchemaVersion        int              `json:"schema_version"`
	Old                  RunInventoryItem `json:"old"`
	New                  RunInventoryItem `json:"new"`
	StatusChanged        bool             `json:"status_changed"`
	DurationDeltaMS      int64            `json:"duration_delta_ms"`
	SizeDeltaBytes       int64            `json:"size_delta_bytes"`
	SameFailure          bool             `json:"same_failure"`
	ExecutedTestsDelta   int              `json:"executed_tests_delta"`
	UnexpectedTestsDelta int              `json:"unexpected_tests_delta"`
}

type RunPinResult struct {
	SchemaVersion int    `json:"schema_version"`
	RunID         string `json:"run_id"`
	Pinned        bool   `json:"pinned"`
	Path          string `json:"path"`
}

func runRuns(args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(out, runsUsage)
		return 0
	}
	command := args[0]
	options, positional, err := parseRunsOptions(args[1:])
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	project, err := Discover(options.Root)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	root := artifactRoot(project, "")
	switch command {
	case "list":
		if len(positional) != 0 {
			return reportError(options.JSON, errors.New("runs list does not accept positional arguments"), out, errOut)
		}
		result, err := listRunInventory(root, options, time.Now().UTC())
		if err != nil {
			return reportError(options.JSON, err, out, errOut)
		}
		if options.JSON {
			_ = writeJSONTo(out, result)
		} else {
			printRunInventory(out, result)
		}
		return 0
	case "show":
		if len(positional) > 1 {
			return reportError(options.JSON, errors.New("runs show accepts one selector"), out, errOut)
		}
		selector := "latest"
		if len(positional) == 1 {
			selector = positional[0]
		}
		runDir, err := findReportRunDirectory(root, selector)
		if err != nil {
			return reportError(options.JSON, err, out, errOut)
		}
		report, exitCode, err := readRunReportDetailed(runDir, !options.JSON || options.FullJSON)
		if err != nil {
			return reportError(options.JSON, err, out, errOut)
		}
		if options.JSON {
			if !options.FullJSON {
				report = compactRunReport(report)
			}
			_ = writeJSONTo(out, report)
		} else {
			printRunReportSummary(out, report)
		}
		return exitCode
	case "compare":
		if len(positional) != 2 {
			return reportError(options.JSON, errors.New("runs compare requires OLD and NEW selectors"), out, errOut)
		}
		comparison, err := compareRuns(root, positional[0], positional[1])
		if err != nil {
			return reportError(options.JSON, err, out, errOut)
		}
		if options.JSON {
			_ = writeJSONTo(out, comparison)
		} else {
			fmt.Fprintf(out, "%s (%s) -> %s (%s): duration %s%dms, size %s%d bytes\n", comparison.Old.RunID, comparison.Old.Status, comparison.New.RunID, comparison.New.Status, signedPrefix(comparison.DurationDeltaMS), comparison.DurationDeltaMS, signedPrefix(comparison.SizeDeltaBytes), comparison.SizeDeltaBytes)
		}
		return 0
	case "pin":
		if len(positional) != 1 {
			return reportError(options.JSON, errors.New("runs pin requires one selector"), out, errOut)
		}
		result, err := pinRun(root, positional[0], !options.Remove)
		if err != nil {
			return reportError(options.JSON, err, out, errOut)
		}
		if options.JSON {
			_ = writeJSONTo(out, result)
		} else {
			state := "unpinned"
			if result.Pinned {
				state = "pinned"
			}
			fmt.Fprintf(out, "%s %s\n", result.RunID, state)
		}
		return 0
	default:
		return reportError(options.JSON, fmt.Errorf("unknown runs command %q", command), out, errOut)
	}
}

func parseRunsOptions(args []string) (runsOptions, []string, error) {
	options := runsOptions{Limit: 50}
	var positional []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--dir", "--root":
			flag := args[index]
			value, next, err := nextValue(args, index, flag)
			if err != nil {
				return options, nil, err
			}
			index = next
			if err := setDirectoryOption(&options.Root, value, flag); err != nil {
				return options, nil, err
			}
		case "--status":
			value, next, err := nextValue(args, index, "--status")
			if err != nil {
				return options, nil, err
			}
			status := strings.ToLower(value)
			if !validRunStatus(status) {
				return options, nil, fmt.Errorf("--status must be passed, failed, skipped, cancelled, running, or interrupted (got %q)", value)
			}
			index, options.Status = next, status
		case "--since":
			value, next, err := nextValue(args, index, "--since")
			if err != nil {
				return options, nil, err
			}
			duration, err := parseRetentionAge(value)
			if err != nil {
				return options, nil, errors.New(strings.NewReplacer("--older-than", "--since").Replace(err.Error()))
			}
			index, options.Since, options.SinceSet = next, duration, true
		case "--test":
			value, next, err := nextValue(args, index, "--test")
			if err != nil {
				return options, nil, err
			}
			index, options.Test = next, strings.ToLower(value)
		case "--limit":
			value, next, err := nextValue(args, index, "--limit")
			if err != nil {
				return options, nil, err
			}
			limit, err := strconv.Atoi(value)
			if err != nil || limit < 1 || limit > 1000 {
				return options, nil, fmt.Errorf("--limit must be between 1 and 1000 (got %q)", value)
			}
			index, options.Limit = next, limit
		case "--remove":
			options.Remove = true
		case "--json":
			options.JSON = true
		case "--json=full":
			options.JSON, options.FullJSON = true, true
		case "--help", "-h":
			return options, nil, errors.New("use `heimdal runs --help` for usage")
		default:
			if strings.HasPrefix(args[index], "-") {
				return options, nil, fmt.Errorf("unknown option %q", args[index])
			}
			positional = append(positional, args[index])
		}
	}
	return options, positional, nil
}

func validRunStatus(status string) bool {
	switch status {
	case "passed", "failed", "skipped", "cancelled", "running", "interrupted":
		return true
	default:
		return false
	}
}

func listRunInventory(root string, options runsOptions, now time.Time) (RunsListResult, error) {
	result := RunsListResult{SchemaVersion: 1, ArtifactRoot: root, Runs: []RunInventoryItem{}}
	runs, err := inspectArtifactRuns(root, now)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for _, run := range runs {
		item, err := inventoryItem(run)
		if err != nil {
			continue
		}
		if options.Status != "" && item.Status != options.Status {
			continue
		}
		if options.SinceSet && item.StartedAt.Before(now.Add(-options.Since)) {
			continue
		}
		if options.Test != "" && !inventoryMatchesTest(item, options.Test) {
			continue
		}
		result.Runs = append(result.Runs, item)
	}
	sort.Slice(result.Runs, func(left, right int) bool { return result.Runs[left].StartedAt.After(result.Runs[right].StartedAt) })
	result.Matched = len(result.Runs)
	result.FailureGroups = groupRunFailures(result.Runs)
	if len(result.Runs) > options.Limit {
		result.Runs = result.Runs[:options.Limit]
	}
	return result, nil
}

func inventoryItem(run artifactRun) (RunInventoryItem, error) {
	item := RunInventoryItem{RunID: run.ID, Status: run.Status, StartedAt: run.StartedAt, SizeBytes: run.SizeBytes, Pinned: run.Pinned, Interrupted: run.Status == "interrupted"}
	if result, err := readResult(filepath.Join(run.Path, "result.json")); err == nil {
		if result.Tests == nil || (result.Status == "failed" && result.PrimaryFailure == nil) {
			enrichRunResult(&result)
		}
		item.Branch, item.FinishedAt, item.DurationMS = result.Branch, result.FinishedAt, result.DurationMS
		item.Invocation, item.Tests, item.PrimaryFailure = result.Invocation, result.Tests, result.PrimaryFailure
		return item, nil
	}
	manifest, err := readRunManifest(filepath.Join(run.Path, "run.json"))
	if err != nil {
		return item, err
	}
	item.Branch, item.Invocation = manifest.Branch, manifest.Invocation
	return item, nil
}

func inventoryMatchesTest(item RunInventoryItem, query string) bool {
	values := append([]string(nil), item.Invocation.TestFiles...)
	values = append(values, item.Invocation.Grep)
	if item.PrimaryFailure != nil {
		values = append(values, item.PrimaryFailure.Test, item.PrimaryFailure.Location)
	}
	return strings.Contains(strings.ToLower(strings.Join(values, "\n")), query)
}

func groupRunFailures(items []RunInventoryItem) []RunFailureGroup {
	groups := map[string]RunFailureGroup{}
	for _, item := range items {
		if item.PrimaryFailure == nil || item.PrimaryFailure.Fingerprint == "" {
			continue
		}
		group := groups[item.PrimaryFailure.Fingerprint]
		group.Fingerprint, group.Message, group.Count = item.PrimaryFailure.Fingerprint, item.PrimaryFailure.Message, group.Count+1
		if item.StartedAt.After(group.LatestAt) {
			group.LatestAt, group.LatestRunID = item.StartedAt, item.RunID
		}
		groups[group.Fingerprint] = group
	}
	result := make([]RunFailureGroup, 0, len(groups))
	for _, group := range groups {
		if group.Count > 1 {
			result = append(result, group)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Count == result[right].Count {
			return result[left].LatestAt.After(result[right].LatestAt)
		}
		return result[left].Count > result[right].Count
	})
	return result
}

func compareRuns(root, oldSelector, newSelector string) (RunComparison, error) {
	oldItem, err := inventoryItemForSelector(root, oldSelector)
	if err != nil {
		return RunComparison{}, err
	}
	newItem, err := inventoryItemForSelector(root, newSelector)
	if err != nil {
		return RunComparison{}, err
	}
	comparison := RunComparison{SchemaVersion: 1, Old: oldItem, New: newItem, StatusChanged: oldItem.Status != newItem.Status, DurationDeltaMS: newItem.DurationMS - oldItem.DurationMS, SizeDeltaBytes: newItem.SizeBytes - oldItem.SizeBytes}
	comparison.ExecutedTestsDelta = testCount(newItem.Tests, func(counts *TestCounts) int { return counts.Executed }) - testCount(oldItem.Tests, func(counts *TestCounts) int { return counts.Executed })
	comparison.UnexpectedTestsDelta = testCount(newItem.Tests, func(counts *TestCounts) int { return counts.Unexpected }) - testCount(oldItem.Tests, func(counts *TestCounts) int { return counts.Unexpected })
	comparison.SameFailure = oldItem.PrimaryFailure != nil && newItem.PrimaryFailure != nil && oldItem.PrimaryFailure.Fingerprint != "" && oldItem.PrimaryFailure.Fingerprint == newItem.PrimaryFailure.Fingerprint
	return comparison, nil
}

func inventoryItemForSelector(root, selector string) (RunInventoryItem, error) {
	directory, err := findReportRunDirectory(root, selector)
	if err != nil {
		return RunInventoryItem{}, err
	}
	runs, err := inspectArtifactRuns(root, time.Now().UTC())
	if err != nil {
		return RunInventoryItem{}, err
	}
	for _, run := range runs {
		if run.Path == directory {
			return inventoryItem(run)
		}
	}
	return RunInventoryItem{}, fmt.Errorf("run %s is not a recognized Heimdal run", filepath.Base(directory))
}

func pinRun(root, selector string, pinned bool) (RunPinResult, error) {
	directory, err := findReportRunDirectory(root, selector)
	if err != nil {
		return RunPinResult{}, err
	}
	path := filepath.Join(directory, ".pin")
	if pinned {
		if err := os.WriteFile(path, []byte("pinned\n"), 0o644); err != nil {
			return RunPinResult{}, fmt.Errorf("pin run: %w", err)
		}
	} else if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return RunPinResult{}, fmt.Errorf("unpin run: %w", err)
	}
	return RunPinResult{SchemaVersion: 1, RunID: filepath.Base(directory), Pinned: pinned, Path: path}, nil
}

func printRunInventory(out io.Writer, result RunsListResult) {
	if len(result.Runs) == 0 {
		fmt.Fprintln(out, "No matching Heimdal runs.")
		return
	}
	for _, run := range result.Runs {
		pin := ""
		if run.Pinned {
			pin = " pinned"
		}
		fmt.Fprintf(out, "%s  %-11s  %s  %dms  %d bytes%s\n", run.StartedAt.Format(time.RFC3339), run.Status, run.RunID, run.DurationMS, run.SizeBytes, pin)
	}
	for _, group := range result.FailureGroups {
		fmt.Fprintf(out, "Repeated failure %s: %d runs; latest %s\n", group.Fingerprint, group.Count, group.LatestRunID)
	}
}

func printRunReportSummary(out io.Writer, report any) {
	switch value := report.(type) {
	case RunResult:
		printResult(out, value)
	case RunManifest:
		fmt.Fprintf(out, "Result: %s\nArtifacts: %s\n", value.Status, value.Artifacts.RunDir)
		if value.Progress != nil {
			fmt.Fprintf(out, "Progress: %dms, %d log bytes", value.Progress.ElapsedMS, value.Progress.StdoutBytes+value.Progress.StderrBytes)
			if value.Progress.LastOutput != "" {
				fmt.Fprintf(out, ", %s", value.Progress.LastOutput)
			}
			fmt.Fprintln(out)
		}
	}
}

func testCount(counts *TestCounts, value func(*TestCounts) int) int {
	if counts == nil {
		return 0
	}
	return value(counts)
}

func signedPrefix(value int64) string {
	if value > 0 {
		return "+"
	}
	return ""
}
