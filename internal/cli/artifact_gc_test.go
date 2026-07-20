package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultArtifactsUseDedicatedRootAndRetention(t *testing.T) {
	config := defaultConfig("")
	if config.Artifacts.Directory != ".heimdal" {
		t.Fatalf("default artifact directory = %q", config.Artifacts.Directory)
	}
	if !config.Artifacts.Retention.Enabled || config.Artifacts.Retention.MaxAgeDays != 14 || config.Artifacts.Retention.KeepFailures != 20 || config.Artifacts.Retention.MaxBytes != 5*1024*1024*1024 || !config.Artifacts.Retention.ThinRepeatedFailures {
		t.Fatalf("default retention = %#v", config.Artifacts.Retention)
	}
}

func TestGCReportsAndPrunesStaleSessionIndexesWithoutRemovingEvidence(t *testing.T) {
	t.Setenv("HEIMDAL_STATE_DIR", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := filepath.Join(root, "fake-playwright-cli")
	if err := os.WriteFile(runner, []byte("#!/bin/sh\nprintf '%s\\n' '{\"browsers\":[]}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	state := SessionState{Name: "stale", RunID: "stale-run", Root: root, StartedAt: time.Now().UTC().Add(-time.Hour)}
	state.SessionDir = filepath.Join(root, defaultArtifactDir, "sessions", state.Name, state.RunID)
	state.ActionLog = filepath.Join(state.SessionDir, "actions.jsonl")
	state.ProjectCache = &SessionProjectCache{AgentRunner: []string{runner}}
	if err := os.MkdirAll(state.SessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(state.SessionDir, "evidence.txt")
	if err := os.WriteFile(evidence, []byte("retained"), 0o644); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(filepath.Dir(state.SessionDir), "session.json")
	if err := writeSessionState(statePath, state); err != nil {
		t.Fatal(err)
	}
	if err := writeSessionIndex(state); err != nil {
		t.Fatal(err)
	}
	indexPath, _ := sessionIndexPath(state)

	var out, errOut strings.Builder
	if code := runGC([]string{"--dir", root, "--dry-run", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("gc dry-run exit = %d: %s", code, errOut.String())
	}
	var dry GCResult
	if err := json.Unmarshal([]byte(out.String()), &dry); err != nil {
		t.Fatal(err)
	}
	if dry.StaleSessions != 1 || dry.SessionIndexesPruned != 0 || dry.SessionEvidenceBytes == 0 {
		t.Fatalf("gc dry-run = %#v", dry)
	}
	out.Reset()
	if code := runGC([]string{"--dir", root, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("gc exit = %d: %s", code, errOut.String())
	}
	var applied GCResult
	if err := json.Unmarshal([]byte(out.String()), &applied); err != nil {
		t.Fatal(err)
	}
	if applied.StaleSessions != 1 || applied.SessionIndexesPruned != 1 {
		t.Fatalf("gc applied = %#v", applied)
	}
	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Fatalf("stale index remains: %v", err)
	}
	if _, err := os.Stat(evidence); err != nil {
		t.Fatalf("gc removed session evidence: %v", err)
	}
	finalized, err := readSessionState(statePath)
	if err != nil || finalized.StoppedAt == nil {
		t.Fatalf("gc did not finalize state: %#v, %v", finalized, err)
	}
}

func TestParseArtifactByteBudget(t *testing.T) {
	for input, expected := range map[string]int64{"0": 0, "512KB": 512 * 1024, "5GB": 5 * 1024 * 1024 * 1024} {
		actual, err := parseByteSize(input)
		if err != nil || actual != expected {
			t.Fatalf("parse %s = %d, %v; want %d", input, actual, err, expected)
		}
	}
	if _, err := parseByteSize("large"); err == nil {
		t.Fatal("invalid byte budget was accepted")
	}
}

func TestExistingRetentionConfigInheritsDefaultByteBudget(t *testing.T) {
	root := t.TempDir()
	contents := []byte(`{"version":1,"artifacts":{"directory":".heimdal","retention":{"enabled":true,"max_age_days":30,"keep_failures":5}}}`)
	if err := os.WriteFile(filepath.Join(root, configFileName), contents, 0o644); err != nil {
		t.Fatal(err)
	}
	config, _, err := loadConfig(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if config.Artifacts.Retention.MaxBytes != 5*1024*1024*1024 || config.Artifacts.Retention.MaxAgeDays != 30 || !config.Artifacts.Retention.ThinRepeatedFailures {
		t.Fatalf("loaded retention = %#v", config.Artifacts.Retention)
	}
}

func TestRetentionThinsRecentRepeatedFailuresWithoutLosingNewestEvidence(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	createFingerprintRun(t, root, "same-oldest", now.Add(-3*time.Hour), "same")
	createFingerprintRun(t, root, "unique", now.Add(-2*time.Hour), "unique")
	createFingerprintRun(t, root, "same-newest", now.Add(-time.Hour), "same")

	retention := RetentionConfig{Enabled: true, MaxAgeDays: 14, KeepFailures: 20, ThinRepeatedFailures: true}
	result, err := collectArtifactGarbage(root, retention, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Candidates != 1 || result.Items[0].RunID != "same-oldest" || !strings.Contains(result.Items[0].Reason, "repeated semantic failure") {
		t.Fatalf("repeated failure plan = %#v", result)
	}
}

func TestRunArtifactDeduplicationHardLinksExactLargeEvidence(t *testing.T) {
	runDir := t.TempDir()
	reportDir := filepath.Join(runDir, "report", "data")
	resultDir := filepath.Join(runDir, "test-results", "case")
	for _, directory := range []string{reportDir, resultDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	payload := make([]byte, 128*1024)
	for index := range payload {
		payload[index] = byte(index % 251)
	}
	reportTrace := filepath.Join(reportDir, "hash.zip")
	resultTrace := filepath.Join(resultDir, "trace.zip")
	different := filepath.Join(resultDir, "different.zip")
	for _, path := range []string{reportTrace, resultTrace} {
		if err := os.WriteFile(path, payload, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	payload[len(payload)-1]++
	if err := os.WriteFile(different, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	files, bytes, issue := deduplicateRunArtifacts(runDir)
	if files != 1 || bytes != 128*1024 || issue != "" {
		t.Fatalf("deduplication = %d files, %d bytes, %q", files, bytes, issue)
	}
	reportInfo, err := os.Stat(reportTrace)
	if err != nil {
		t.Fatal(err)
	}
	resultInfo, err := os.Stat(resultTrace)
	if err != nil {
		t.Fatal(err)
	}
	differentInfo, err := os.Stat(different)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(reportInfo, resultInfo) || os.SameFile(reportInfo, differentInfo) {
		t.Fatalf("deduplicated inode identity is wrong")
	}
}

func TestParseRetentionAgeSupportsDays(t *testing.T) {
	age, err := parseRetentionAge("14d")
	if err != nil || age != 14*24*time.Hour {
		t.Fatalf("retention age = %v, %v", age, err)
	}
	if _, err := parseRetentionAge("-1d"); err == nil {
		t.Fatal("negative retention age was accepted")
	}
}

func TestArtifactGCIsBoundedAndPreservesPinnedActiveAndRecentRuns(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	createResultRun(t, root, "old-pass", "passed", now.Add(-30*24*time.Hour), false)
	createResultRun(t, root, "old-failure-kept", "failed", now.Add(-20*24*time.Hour), false)
	createResultRun(t, root, "older-failure", "failed", now.Add(-40*24*time.Hour), false)
	createResultRun(t, root, "recent-pass", "passed", now.Add(-2*24*time.Hour), false)
	createResultRun(t, root, "pinned-pass", "passed", now.Add(-50*24*time.Hour), true)
	createInterruptedRun(t, root, "interrupted", now.Add(-25*24*time.Hour))
	if err := os.Mkdir(filepath.Join(root, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}

	retention := RetentionConfig{Enabled: true, MaxAgeDays: 14, KeepFailures: 1}
	dryRun, err := collectArtifactGarbage(root, retention, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.Candidates != 3 || dryRun.Removed != 0 || dryRun.ReclaimableBytes == 0 {
		t.Fatalf("dry-run result = %#v", dryRun)
	}
	for _, kept := range []string{"old-failure-kept", "recent-pass", "pinned-pass", "sessions"} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Fatalf("dry run changed %s: %v", kept, err)
		}
	}

	removed, err := collectArtifactGarbage(root, retention, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Removed != 3 {
		t.Fatalf("removed result = %#v", removed)
	}
	for _, deleted := range []string{"old-pass", "older-failure", "interrupted"} {
		if _, err := os.Stat(filepath.Join(root, deleted)); !os.IsNotExist(err) {
			t.Fatalf("candidate %s was not removed: %v", deleted, err)
		}
	}
}

func TestArtifactBudgetKeepsOneFullRunPerFailureFingerprintAndArchivesDuplicates(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	createFingerprintRun(t, root, "duplicate-old", now.Add(-4*time.Hour), "same")
	createResultRun(t, root, "passed-old", "passed", now.Add(-3*time.Hour), false)
	createFingerprintRun(t, root, "unique-failure", now.Add(-2*time.Hour), "unique")
	createFingerprintRun(t, root, "duplicate-new", now.Add(-time.Hour), "same")

	retention := RetentionConfig{Enabled: true, MaxAgeDays: 14, KeepFailures: 2, MaxBytes: 1}
	dryRun, err := collectArtifactGarbage(root, retention, true, now)
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.Candidates != 2 || dryRun.Removed != 0 || dryRun.Archived != 0 {
		t.Fatalf("budget dry run = %#v", dryRun)
	}
	for _, item := range dryRun.Items {
		if !strings.Contains(item.Reason, "artifact budget") {
			t.Fatalf("budget reason = %#v", item)
		}
	}

	collected, err := collectArtifactGarbage(root, retention, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if collected.Removed != 2 || collected.Archived != 2 {
		t.Fatalf("budget collection = %#v", collected)
	}
	for _, kept := range []string{"duplicate-new", "unique-failure"} {
		if _, err := os.Stat(filepath.Join(root, kept)); err != nil {
			t.Fatalf("protected failure %s was removed: %v", kept, err)
		}
	}
	archived, err := readArchivedRun(root, "duplicate-old")
	if err != nil || !archived.Archived || archived.PrimaryFailure == nil || archived.PrimaryFailure.Fingerprint != "same" {
		t.Fatalf("archived duplicate = %#v, %v", archived, err)
	}
	listed, err := listRunInventory(root, runsOptions{Limit: 50}, now)
	if err != nil || listed.Matched != 4 {
		t.Fatalf("inventory with compact history = %#v, %v", listed, err)
	}
}

func createResultRun(t *testing.T, root, id, status string, started time.Time, pinned bool) {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	result := RunResult{SchemaVersion: 1, RunID: id, Status: status, StartedAt: started, FinishedAt: started.Add(time.Minute), Artifacts: Artifacts{RunDir: directory}}
	if err := writeJSON(filepath.Join(directory, "result.json"), result); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "evidence.bin"), make([]byte, 128), 0o644); err != nil {
		t.Fatal(err)
	}
	if pinned {
		if err := os.WriteFile(filepath.Join(directory, ".pin"), []byte("pinned\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func createInterruptedRun(t *testing.T, root, id string, started time.Time) {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := RunManifest{SchemaVersion: 1, RunID: id, Status: "running", StartedAt: started, Artifacts: Artifacts{RunDir: directory}}
	if err := writeJSON(filepath.Join(directory, "run.json"), manifest); err != nil {
		t.Fatal(err)
	}
}

func createFingerprintRun(t *testing.T, root, id string, started time.Time, fingerprint string) {
	t.Helper()
	createResultRun(t, root, id, "failed", started, false)
	path := filepath.Join(root, id, "result.json")
	result, err := readResult(path)
	if err != nil {
		t.Fatal(err)
	}
	result.PrimaryFailure = &PrimaryFailure{Message: "representative failure", Fingerprint: fingerprint}
	if err := writeJSON(path, result); err != nil {
		t.Fatal(err)
	}
}
