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

const gcUsage = `Safely remove expired Heimdal run artifacts

Usage:
  heimdal gc [--dir PATH] [--artifacts PATH] [--dry-run] [options]

Options:
  --dir PATH          Discover the project from PATH
  --artifacts PATH    Override the configured artifact root
  --older-than AGE    Remove eligible runs older than AGE (for example 14d)
  --keep-failures N   Retain the newest full run for N failure fingerprints
  --max-bytes SIZE    Bound retained run artifacts (for example 5GB; 0 disables)
  --dry-run           Report candidates without deleting them
  --json              Print structured output
  --help              Print this help

Pinned runs, active runs, session evidence, and unrecognized directories are
never removed. GC finalizes stale session state and removes dead global indexes.
Pruned runs keep compact inventory summaries under .heimdal/.history.
Automatic retention uses artifacts.retention from .heimdal.json and runs at
most once per day.
`

type gcOptions struct {
	Root            string
	Artifacts       string
	DryRun          bool
	JSON            bool
	OlderThan       time.Duration
	OlderThanSet    bool
	KeepFailures    int
	KeepFailuresSet bool
	MaxBytes        int64
	MaxBytesSet     bool
	Help            bool
}

type GCItem struct {
	RunID     string    `json:"run_id"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	SizeBytes int64     `json:"size_bytes"`
	Reason    string    `json:"reason"`
}

type GCResult struct {
	SchemaVersion        int      `json:"schema_version"`
	Status               string   `json:"status"`
	ArtifactRoot         string   `json:"artifact_root"`
	DryRun               bool     `json:"dry_run"`
	Scanned              int      `json:"scanned"`
	Candidates           int      `json:"candidates"`
	Removed              int      `json:"removed"`
	Archived             int      `json:"archived"`
	ReclaimableBytes     int64    `json:"reclaimable_bytes"`
	Items                []GCItem `json:"items,omitempty"`
	Omitted              int      `json:"omitted,omitempty"`
	StaleSessions        int      `json:"stale_sessions,omitempty"`
	SessionIndexesPruned int      `json:"session_indexes_pruned,omitempty"`
	SessionEvidenceBytes int64    `json:"session_evidence_bytes,omitempty"`
}

type artifactRun struct {
	ID          string
	Path        string
	Status      string
	StartedAt   time.Time
	SizeBytes   int64
	Pinned      bool
	Active      bool
	Fingerprint string
}

type artifactGCCandidate struct {
	Run    artifactRun
	Reason string
}

func runGC(args []string, out, errOut io.Writer) int {
	options, err := parseGCOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if options.Help {
		fmt.Fprint(out, gcUsage)
		return 0
	}
	project, err := Discover(options.Root)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	retention := project.Config.Artifacts.Retention
	if options.OlderThanSet {
		retention.MaxAgeDays = durationDaysCeil(options.OlderThan)
	}
	if options.KeepFailuresSet {
		retention.KeepFailures = options.KeepFailures
	}
	if options.MaxBytesSet {
		retention.MaxBytes = options.MaxBytes
	}
	root := artifactRoot(project, options.Artifacts)
	result, err := collectArtifactGarbage(root, retention, options.DryRun, time.Now().UTC())
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	sessions, err := inspectSessionInventory(project.Root, "")
	if err != nil {
		return reportError(options.JSON, fmt.Errorf("inspect session lifecycle: %w", err), out, errOut)
	}
	if err := pruneSessionInventory(&sessions, options.DryRun, time.Now().UTC()); err != nil {
		return reportError(options.JSON, fmt.Errorf("prune stale session indexes: %w", err), out, errOut)
	}
	result.StaleSessions = sessions.Candidates
	result.SessionIndexesPruned = sessions.Pruned
	result.SessionEvidenceBytes = sessions.CandidateBytes
	if options.JSON {
		if err := writeJSONTo(out, result); err != nil {
			return reportError(true, err, out, errOut)
		}
		return 0
	}
	action := "removed"
	if options.DryRun {
		action = "would remove"
	}
	fmt.Fprintf(out, "Heimdal gc: %s %d runs (%d bytes) from %s\n", action, result.Candidates, result.ReclaimableBytes, result.ArtifactRoot)
	if result.StaleSessions > 0 {
		fmt.Fprintf(out, "  stale sessions: %d indexes, %d evidence bytes preserved\n", result.StaleSessions, result.SessionEvidenceBytes)
	}
	for _, item := range result.Items {
		fmt.Fprintf(out, "  %s: %s, %d bytes, %s\n", item.RunID, item.Status, item.SizeBytes, item.Reason)
	}
	if result.Omitted > 0 {
		fmt.Fprintf(out, "  … %d more candidates omitted; use --json for structured totals\n", result.Omitted)
	}
	return 0
}

func parseGCOptions(args []string) (gcOptions, error) {
	options := gcOptions{}
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
		case "--artifacts":
			value, next, err := nextValue(args, index, "--artifacts")
			if err != nil {
				return options, err
			}
			index, options.Artifacts = next, value
		case "--older-than":
			value, next, err := nextValue(args, index, "--older-than")
			if err != nil {
				return options, err
			}
			duration, err := parseRetentionAge(value)
			if err != nil {
				return options, err
			}
			index, options.OlderThan, options.OlderThanSet = next, duration, true
		case "--keep-failures":
			value, next, err := nextValue(args, index, "--keep-failures")
			if err != nil {
				return options, err
			}
			count, err := strconv.Atoi(value)
			if err != nil || count < 0 {
				return options, fmt.Errorf("--keep-failures must be a non-negative integer (got %q)", value)
			}
			index, options.KeepFailures, options.KeepFailuresSet = next, count, true
		case "--max-bytes":
			value, next, err := nextValue(args, index, "--max-bytes")
			if err != nil {
				return options, err
			}
			bytes, err := parseByteSize(value)
			if err != nil {
				return options, err
			}
			index, options.MaxBytes, options.MaxBytesSet = next, bytes, true
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

func parseByteSize(value string) (int64, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	multiplier := int64(1)
	for _, suffix := range []struct {
		name       string
		multiplier int64
	}{{"GB", 1024 * 1024 * 1024}, {"MB", 1024 * 1024}, {"KB", 1024}, {"B", 1}} {
		if strings.HasSuffix(normalized, suffix.name) {
			normalized = strings.TrimSpace(strings.TrimSuffix(normalized, suffix.name))
			multiplier = suffix.multiplier
			break
		}
	}
	amount, err := strconv.ParseInt(normalized, 10, 64)
	if err != nil || amount < 0 || (amount > 0 && amount > (1<<63-1)/multiplier) {
		return 0, fmt.Errorf("--max-bytes must be a non-negative size such as 5GB (got %q)", value)
	}
	return amount * multiplier, nil
}

func parseRetentionAge(value string) (time.Duration, error) {
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days < 0 {
			return 0, fmt.Errorf("--older-than must be a non-negative duration such as 14d (got %q)", value)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return 0, fmt.Errorf("--older-than must be a non-negative duration such as 14d (got %q)", value)
	}
	return duration, nil
}

func durationDaysCeil(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	return int((duration + 24*time.Hour - 1) / (24 * time.Hour))
}

func collectArtifactGarbage(root string, retention RetentionConfig, dryRun bool, now time.Time) (GCResult, error) {
	result := GCResult{SchemaVersion: 1, Status: "passed", ArtifactRoot: root, DryRun: dryRun}
	runs, err := inspectArtifactRuns(root, now)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	result.Scanned = len(runs)
	candidates := artifactGarbagePlan(runs, retention, now)
	result.Candidates = len(candidates)
	for _, candidate := range candidates {
		run := candidate.Run
		result.ReclaimableBytes += run.SizeBytes
		if len(result.Items) < 200 {
			result.Items = append(result.Items, GCItem{RunID: run.ID, Status: run.Status, StartedAt: run.StartedAt, SizeBytes: run.SizeBytes, Reason: candidate.Reason})
		} else {
			result.Omitted++
		}
		if dryRun {
			continue
		}
		if err := archiveRunSummary(root, run, candidate.Reason, now); err != nil {
			return result, err
		}
		result.Archived++
		if err := removeArtifactRun(root, run.Path); err != nil {
			return result, err
		}
		result.Removed++
	}
	return result, nil
}

func artifactGarbageCandidates(runs []artifactRun, retention RetentionConfig, now time.Time) []artifactRun {
	plan := artifactGarbagePlan(runs, retention, now)
	candidates := make([]artifactRun, 0, len(plan))
	for _, candidate := range plan {
		candidates = append(candidates, candidate.Run)
	}
	return candidates
}

func artifactGarbagePlan(runs []artifactRun, retention RetentionConfig, now time.Time) []artifactGCCandidate {
	failures := make([]artifactRun, 0)
	for _, run := range runs {
		if run.Status == "failed" {
			failures = append(failures, run)
		}
	}
	sort.Slice(failures, func(left, right int) bool { return failures[left].StartedAt.After(failures[right].StartedAt) })
	protectedFailures := make(map[string]bool)
	protectedFingerprints := make(map[string]bool)
	for _, failure := range failures {
		fingerprint := failure.Fingerprint
		if fingerprint == "" {
			fingerprint = "run:" + failure.ID
		}
		if protectedFingerprints[fingerprint] || len(protectedFingerprints) >= retention.KeepFailures {
			continue
		}
		protectedFingerprints[fingerprint] = true
		protectedFailures[failure.ID] = true
	}
	cutoff := now.Add(-time.Duration(retention.MaxAgeDays) * 24 * time.Hour)
	selected := make(map[string]bool)
	candidates := make([]artifactGCCandidate, 0)
	for _, run := range runs {
		if run.Pinned || run.Active || protectedFailures[run.ID] || run.StartedAt.After(cutoff) {
			continue
		}
		selected[run.ID] = true
		candidates = append(candidates, artifactGCCandidate{Run: run, Reason: fmt.Sprintf("older than %dd", retention.MaxAgeDays)})
	}
	if retention.MaxBytes > 0 {
		var retainedBytes int64
		for _, run := range runs {
			if !selected[run.ID] {
				retainedBytes += run.SizeBytes
			}
		}
		if retainedBytes > retention.MaxBytes {
			remaining := append([]artifactRun(nil), runs...)
			sort.Slice(remaining, func(left, right int) bool { return remaining[left].StartedAt.Before(remaining[right].StartedAt) })
			for _, run := range remaining {
				if retainedBytes <= retention.MaxBytes {
					break
				}
				if selected[run.ID] || run.Pinned || run.Active || protectedFailures[run.ID] {
					continue
				}
				selected[run.ID] = true
				retainedBytes -= run.SizeBytes
				candidates = append(candidates, artifactGCCandidate{Run: run, Reason: fmt.Sprintf("artifact budget exceeds %d bytes", retention.MaxBytes)})
			}
		}
	}
	sort.Slice(candidates, func(left, right int) bool {
		return candidates[left].Run.StartedAt.Before(candidates[right].Run.StartedAt)
	})
	return candidates
}

func inspectArtifactRuns(root string, now time.Time) ([]artifactRun, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	runs := make([]artifactRun, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == "sessions" || entry.Name() == ".history" {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		run := artifactRun{ID: entry.Name(), Path: path, StartedAt: info.ModTime()}
		if _, err := os.Stat(filepath.Join(path, ".pin")); err == nil {
			run.Pinned = true
		}
		if result, err := readResult(filepath.Join(path, "result.json")); err == nil {
			run.Status, run.StartedAt = result.Status, result.StartedAt
			if result.PrimaryFailure == nil && result.Status == "failed" {
				enrichRunResult(&result)
			}
			if result.PrimaryFailure != nil {
				run.Fingerprint = semanticFailureFingerprint(result.PrimaryFailure)
			}
			run.SizeBytes = result.Artifacts.RunBytes
			if run.SizeBytes == 0 {
				run.SizeBytes = directoryBytes(path)
			}
			runs = append(runs, run)
			continue
		}
		manifest, err := readRunManifest(filepath.Join(path, "run.json"))
		if err != nil {
			continue
		}
		run.StartedAt = manifest.StartedAt
		run.SizeBytes = directoryBytes(path)
		heartbeat, heartbeatErr := os.Stat(filepath.Join(path, ".heartbeat"))
		run.Active = heartbeatErr == nil && now.Sub(heartbeat.ModTime()) <= 15*time.Second
		if run.Active {
			run.Status = "running"
		} else {
			run.Status = "interrupted"
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func removeArtifactRun(root, path string) error {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || strings.Contains(relative, string(filepath.Separator)) {
		return fmt.Errorf("refuse to remove artifact path outside direct root: %s", path)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to remove non-directory artifact path: %s", path)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove expired run %s: %w", path, err)
	}
	return nil
}

func maybeCollectArtifacts(project Project) {
	retention := project.Config.Artifacts.Retention
	if !retention.Enabled {
		return
	}
	root := artifactRoot(project, "")
	stamp := filepath.Join(root, ".gc-stamp")
	if info, err := os.Stat(stamp); err == nil && time.Since(info.ModTime()) < 24*time.Hour {
		return
	}
	if _, err := collectArtifactGarbage(root, retention, false, time.Now().UTC()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return
	}
	_ = os.MkdirAll(root, 0o755)
	_ = os.WriteFile(stamp, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644)
}
