package cli

import (
	"archive/zip"
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
		TraceDiagnosis: &TraceSummary{
			FailingAction: &TraceActionSummary{Index: 3, Error: strings.Repeat("failure ", 100)},
			CaughtCount:   8,
			CaughtProbes:  []TraceActionSummary{{Index: 1}, {Index: 2}},
			NearbyActions: []TraceActionSummary{{Index: 1}, {Index: 2}, {Index: 3}, {Index: 4}, {Index: 5}},
			Snapshots:     []TraceSnapshotSummary{{Excerpt: strings.Repeat("DOM ", 400)}, {Excerpt: "second"}},
			TraceFiles:    []string{"test.trace", "0-trace.trace"},
		},
	}).(RunResult)
	if compact.StdoutTail != "" || compact.StderrTail != "" || compact.Artifacts.Files != nil || compact.Artifacts.RunDir != "/tmp/run" {
		t.Fatalf("compact report = %#v", compact)
	}
	if compact.TraceDiagnosis == nil || compact.TraceDiagnosis.CaughtCount != 8 || compact.TraceDiagnosis.CaughtProbes != nil || len(compact.TraceDiagnosis.NearbyActions) != 3 || len(compact.TraceDiagnosis.Snapshots) != 1 || len(compact.TraceDiagnosis.Snapshots[0].Excerpt) > 603 || compact.TraceDiagnosis.TraceFiles != nil {
		t.Fatalf("compact trace diagnosis = %#v", compact.TraceDiagnosis)
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

func TestRunEvidenceCollectsNamedStdoutAndJSONAttachments(t *testing.T) {
	runDir := t.TempDir()
	attachmentDir := filepath.Join(runDir, "test-results", "example")
	if err := os.MkdirAll(attachmentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	attachment := filepath.Join(attachmentDir, "latency.json")
	if err := os.WriteFile(attachment, []byte(`{"rounds":3,"latency_ms":42}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout := filepath.Join(runDir, "stdout.log")
	lines := "HEIMDAL_EVIDENCE design.metrics {\"iterations\":2}\n" +
		"    attachment #1: latency.timeline (application/json)\n" +
		"    " + attachment + "\n"
	if err := os.WriteFile(stdout, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr := filepath.Join(runDir, "stderr.log")
	if err := os.WriteFile(stderr, []byte("HEIMDAL_EVIDENCE invalid not-json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	evidence, issues := collectRunEvidence(stdout, stderr, runDir, runDir)
	if len(evidence) != 2 || string(evidence["design.metrics"]) != `{"iterations":2}` || !strings.Contains(string(evidence["latency.timeline"]), `"latency_ms":42`) {
		t.Fatalf("evidence = %#v", evidence)
	}
	if len(issues) != 1 || !strings.Contains(issues[0], "invalid") {
		t.Fatalf("evidence issues = %#v", issues)
	}
}

func TestRunEvidenceRejectsAttachmentOutsideRun(t *testing.T) {
	runDir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.json")
	if err := os.WriteFile(outside, []byte(`{"secret":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEvidenceAttachment(runDir, outside); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("outside attachment error = %v", err)
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

func TestFailedRunReportIncludesTraceDiagnosisWithoutFullFileIndex(t *testing.T) {
	runDir := t.TempDir()
	traceDir := filepath.Join(runDir, "test-results", "example")
	if err := os.MkdirAll(traceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tracePath := filepath.Join(traceDir, "trace.zip")
	file, err := os.Create(tracePath)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	events, err := archive.Create("test.trace")
	if err != nil {
		t.Fatal(err)
	}
	traceLines := strings.Join([]string{
		`{"type":"before","callId":"call-1","startTime":10,"apiName":"locator.click","params":{"selector":"internal:role=button[name=Save]"}}`,
		`{"type":"frame-snapshot","snapshot":{"callId":"call-1","snapshotName":"before@call-1","url":"http://127.0.0.1/settings","html":["HTML",{},["BODY",{},"Save changes"]]}}`,
		`{"type":"after","callId":"call-1","endTime":20,"error":{"message":"expected enabled, received disabled"}}`,
	}, "\n") + "\n"
	if _, err := io.WriteString(events, traceLines); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	result := RunResult{SchemaVersion: 1, RunID: "failed-run", Status: "failed", ExitCode: 1, StartedAt: time.Now().UTC(), Failure: "exit status 1", Artifacts: Artifacts{RunDir: runDir}}
	if err := writeJSON(filepath.Join(runDir, "result.json"), result); err != nil {
		t.Fatal(err)
	}
	report, _, err := readRunReportDetailed(runDir, false)
	if err != nil {
		t.Fatal(err)
	}
	failed := report.(RunResult)
	if failed.TraceDiagnosis == nil || failed.TraceDiagnosis.FailingAction == nil || !strings.Contains(failed.TraceDiagnosis.FailingAction.Error, "received disabled") || len(failed.TraceDiagnosis.Snapshots) != 1 {
		t.Fatalf("trace diagnosis = %#v", failed.TraceDiagnosis)
	}
	if failed.Artifacts.Files != nil {
		t.Fatalf("compact diagnosis scanned a public file index: %#v", failed.Artifacts.Files)
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
