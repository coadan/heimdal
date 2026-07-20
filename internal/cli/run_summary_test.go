package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunAnalysisExtractsCountsFailureWarningsAndFingerprint(t *testing.T) {
	output := `
(node:123) Warning: The NO_COLOR env is ignored.
(node:456) Warning: The NO_COLOR env is ignored.
  1) tests/browser/settings.spec.ts:41:3 › settings › saves changes

    Error: Timed out waiting for confirmation after 5000ms
    Locator: getByRole('status')
    Expected: "Saved"
    Received: "Saving"
        at waitForConfirmation (tests/browser/settings.spec.ts:20:9)

  1 failed
  2 passed (6.1s)
  3 skipped
`
	analysis := analyzeRunOutput(output, "")
	if analysis.Tests == nil || analysis.Tests.Total != 6 || analysis.Tests.Executed != 3 || analysis.Tests.Passed != 2 || analysis.Tests.Failed != 1 || analysis.Tests.Skipped != 3 {
		t.Fatalf("test counts = %#v", analysis.Tests)
	}
	if analysis.PrimaryFailure == nil || analysis.PrimaryFailure.Test != "settings › saves changes" || analysis.PrimaryFailure.Location != "tests/browser/settings.spec.ts:41:3" || analysis.PrimaryFailure.Step != "waitForConfirmation" {
		t.Fatalf("primary failure = %#v", analysis.PrimaryFailure)
	}
	if analysis.PrimaryFailure.Fingerprint == "" || len(analysis.PrimaryFailure.Fingerprint) != 16 {
		t.Fatalf("failure fingerprint = %q", analysis.PrimaryFailure.Fingerprint)
	}
	if len(analysis.Warnings) != 1 || analysis.Warnings[0].Source != "runner" || analysis.Warnings[0].Count != 2 {
		t.Fatalf("warnings = %#v", analysis.Warnings)
	}
}

func TestAllSkippedRunIsNotReportedAsPassed(t *testing.T) {
	result := RunResult{RunID: "run-1", Status: "passed", StdoutTail: "  4 skipped\n"}
	enrichRunResult(&result)
	if result.Status != "skipped" || result.ExitCode == 0 || result.Tests == nil || result.Tests.Executed != 0 {
		t.Fatalf("all-skipped result = %#v", result)
	}
}

func TestCompactRunReportOmitsRawEvidenceIndexes(t *testing.T) {
	compact := compactRunReport(RunResult{
		StdoutTail: "large stdout", StderrTail: "large stderr",
		Artifacts: Artifacts{RunDir: "/tmp/run", Files: []string{"/tmp/run/trace.zip"}},
	}).(RunResult)
	if compact.StdoutTail != "" || compact.StderrTail != "" || compact.Artifacts.Files != nil || compact.Artifacts.RunDir != "/tmp/run" {
		t.Fatalf("compact report = %#v", compact)
	}
	_, asJSON, fullJSON, runID, err := parseReportOptions([]string{"--run", "run-1", "--json=full"})
	if err != nil || !asJSON || !fullJSON || runID != "run-1" {
		t.Fatalf("full report options = json %v, full %v, run %q, err %v", asJSON, fullJSON, runID, err)
	}
}

func TestRunInvocationParsesAgentRelevantSelectors(t *testing.T) {
	invocation := parseRunInvocation([]string{"tests/browser/settings.spec.ts", "--grep", "saves", "--project=chromium", "--retries", "2"})
	if strings.Join(invocation.TestFiles, ",") != "tests/browser/settings.spec.ts" || invocation.Grep != "saves" || invocation.Project != "chromium" || invocation.Retries != "2" {
		t.Fatalf("invocation = %#v", invocation)
	}
}

func TestRunEnvironmentProvenanceRecordsNamesAndStateWithoutValues(t *testing.T) {
	variables := runEnvironmentProvenance(PlaywrightConfig{
		Env:           map[string]string{"CONFIGURED_FLAG": "private-value"},
		ProvenanceEnv: []string{"INHERITED_FLAG", "MISSING_FLAG"},
	}, []string{"CONFIGURED_FLAG=private-value", "INHERITED_FLAG=another-secret"})
	encoded, err := json.Marshal(variables)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "private-value") || strings.Contains(text, "another-secret") {
		t.Fatalf("environment provenance leaked a value: %s", text)
	}
	for _, expected := range []string{`"name":"CONFIGURED_FLAG","set":true`, `"name":"INHERITED_FLAG","set":true`, `"name":"MISSING_FLAG","set":false`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("environment provenance omitted %s: %s", expected, text)
		}
	}
}

func TestDirectoryBytesIgnoresDirectories(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "evidence.txt"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := directoryBytes(root); got != 5 {
		t.Fatalf("directory bytes = %d, want 5", got)
	}
}

func TestFailureContextAndLiveProgressStayBounded(t *testing.T) {
	runDir := t.TempDir()
	contextPath := filepath.Join(runDir, "error-context.md")
	context := "# Page snapshot\n\n" + strings.Repeat("visible accessible content ✓\n", 300)
	if err := os.WriteFile(contextPath, []byte(context), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "stdout.log"), []byte("starting\ncheckpoint reached\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "stderr.log"), []byte("warning\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	excerpt := failureContextExcerpt([]string{contextPath})
	if !strings.Contains(excerpt, "Page snapshot") || len(excerpt) > 4000 || strings.ToValidUTF8(excerpt, "") != excerpt {
		t.Fatalf("failure context length=%d, contents=%q", len(excerpt), excerpt)
	}
	started := time.Now().UTC().Add(-2 * time.Second)
	progress := liveRunProgress(RunManifest{StartedAt: started}, runDir, started.Add(2*time.Second))
	if progress.ElapsedMS != 2000 || progress.LastOutput != "warning" || progress.StdoutBytes == 0 || progress.StderrBytes == 0 {
		t.Fatalf("progress = %#v", progress)
	}
}

func TestVersionCommandsAreCompact(t *testing.T) {
	for _, command := range []string{"version", "--version"} {
		var out strings.Builder
		if code := Run(context.Background(), []string{command}, &out, io.Discard); code != 0 {
			t.Fatalf("%s exit = %d", command, code)
		}
		if !strings.HasPrefix(out.String(), "heimdal ") || strings.Contains(out.String(), "Usage:") {
			t.Fatalf("%s output = %q", command, out.String())
		}
	}
}
