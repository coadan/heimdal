package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunInventoryFiltersGroupsComparesAndPins(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	createInventoryRun(t, root, "failed-old", "failed", now.Add(-3*time.Hour), 3000, "same-failure", "tests/browser/design.spec.ts")
	createInventoryRun(t, root, "passed-middle", "passed", now.Add(-2*time.Hour), 2000, "", "tests/browser/other.spec.ts")
	createInventoryRun(t, root, "failed-new", "failed", now.Add(-time.Hour), 1000, "same-failure", "tests/browser/design.spec.ts")
	createInterruptedRun(t, root, "interrupted-newest", now.Add(-30*time.Minute))

	listed, err := listRunInventory(root, runsOptions{Status: "failed", Test: "design", Limit: 50}, now)
	if err != nil {
		t.Fatal(err)
	}
	if listed.Matched != 2 || len(listed.Runs) != 2 || listed.Runs[0].RunID != "failed-new" || len(listed.FailureGroups) != 1 || listed.FailureGroups[0].Count != 2 {
		t.Fatalf("inventory = %#v", listed)
	}

	latestFailed, err := findReportRunDirectory(root, "latest-failed")
	if err != nil || filepath.Base(latestFailed) != "failed-new" {
		t.Fatalf("latest failed = %q, %v", latestFailed, err)
	}
	comparison, err := compareRuns(root, "failed-old", "failed-new")
	if err != nil || !comparison.SameFailure || !comparison.SameSemanticFailure || comparison.SameExactFailure || comparison.DurationDeltaMS != -2000 {
		t.Fatalf("comparison = %#v, %v", comparison, err)
	}
	pin, err := pinRun(root, "failed-new", true)
	if err != nil || !pin.Pinned {
		t.Fatalf("pin = %#v, %v", pin, err)
	}
	if _, err := os.Stat(filepath.Join(root, "failed-new", ".pin")); err != nil {
		t.Fatal(err)
	}
	if _, err := pinRun(root, "failed-new", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "failed-new", ".pin")); !os.IsNotExist(err) {
		t.Fatalf("pin marker still exists: %v", err)
	}
}

func TestRunInventoryLimitsResultsButReportsMatchedTotal(t *testing.T) {
	root := t.TempDir()
	now := time.Now().UTC()
	createInventoryRun(t, root, "run-one", "passed", now.Add(-time.Minute), 100, "", "one.spec.ts")
	createInventoryRun(t, root, "run-two", "passed", now, 100, "", "two.spec.ts")
	listed, err := listRunInventory(root, runsOptions{Limit: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if listed.Matched != 2 || len(listed.Runs) != 1 || listed.Runs[0].RunID != "run-two" {
		t.Fatalf("limited inventory = %#v", listed)
	}
}

func createInventoryRun(t *testing.T, root, id, status string, started time.Time, duration int64, fingerprint, testFile string) {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	result := RunResult{
		SchemaVersion: 1,
		RunID:         id,
		Status:        status,
		StartedAt:     started,
		FinishedAt:    started.Add(time.Duration(duration) * time.Millisecond),
		DurationMS:    duration,
		Invocation:    RunInvocation{TestFiles: []string{testFile}},
		Tests:         &TestCounts{Total: 1, Executed: 1, Passed: 1},
		Artifacts:     Artifacts{RunDir: directory},
	}
	if fingerprint != "" {
		result.Tests.Passed, result.Tests.Failed, result.Tests.Unexpected = 0, 1, 1
		result.PrimaryFailure = &PrimaryFailure{Message: "same failure", Fingerprint: fingerprint}
	}
	if err := writeJSON(filepath.Join(directory, "result.json"), result); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "evidence.bin"), make([]byte, 64), 0o644); err != nil {
		t.Fatal(err)
	}
}
