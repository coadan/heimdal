package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultArtifactsUseDedicatedRootAndRetention(t *testing.T) {
	config := defaultConfig("")
	if config.Artifacts.Directory != ".heimdal" {
		t.Fatalf("default artifact directory = %q", config.Artifacts.Directory)
	}
	if !config.Artifacts.Retention.Enabled || config.Artifacts.Retention.MaxAgeDays != 14 || config.Artifacts.Retention.KeepFailures != 20 {
		t.Fatalf("default retention = %#v", config.Artifacts.Retention)
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
