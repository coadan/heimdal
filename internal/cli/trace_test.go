package cli

import (
	"archive/zip"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTraceHelpIsSpecificAndDoesNotDiscoverProject(t *testing.T) {
	var out, errOut strings.Builder
	if code := runTrace(context.Background(), []string{"--help"}, &out, &errOut); code != 0 {
		t.Fatalf("trace help exit = %d, stderr = %s", code, errOut.String())
	}
	for _, expected := range []string{"Inspect or open a Playwright trace", "--json", "interactive trace viewer"} {
		if !strings.Contains(out.String(), expected) {
			t.Fatalf("trace help omitted %q:\n%s", expected, out.String())
		}
	}
}

func TestParseTraceOptionsSupportsJSONWithoutAmbiguity(t *testing.T) {
	options, err := parseTraceOptions([]string{"--dir", "/tmp/project", "--run", "run-1", "--json"})
	if err != nil {
		t.Fatal(err)
	}
	if options.Root != "/tmp/project" || options.RunID != "run-1" || !options.JSON {
		t.Fatalf("trace options = %#v", options)
	}
	inspect, err := parseTraceOptions([]string{"inspect", "--run", "run-1", "--around-failure"})
	if err != nil || !inspect.JSON || !inspect.Inspect || inspect.AroundFailure != 2 {
		t.Fatalf("trace inspect options = %#v, %v", inspect, err)
	}
	if _, err := parseTraceOptions([]string{"--run", "run-1", "trace.zip"}); err == nil {
		t.Fatal("trace parser accepted both --run and a direct trace path")
	}
}

func TestSummarizeTraceReportsFailureContextWithoutExtractingArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	events, err := archive.Create("test.trace")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{
		`{"type":"before","callId":"call-1","startTime":10,"apiName":"page.goto","params":{"url":"http://127.0.0.1"}}`,
		`{"type":"after","callId":"call-1","endTime":20}`,
		`{"type":"before","callId":"call-2","startTime":25,"apiName":"locator.click","params":{"selector":"internal:role=button[name=Save]"}}`,
		`{"type":"frame-snapshot","snapshot":{"callId":"call-2","snapshotName":"before@call-2","url":"http://127.0.0.1/settings","html":["HTML",{},["BODY",{},"Settings","Save changes"]]}}`,
		`{"type":"after","callId":"call-2","endTime":40,"error":{"message":"element was detached"}}`,
	} {
		if _, err := io.WriteString(events, line+"\n"); err != nil {
			t.Fatal(err)
		}
	}
	resource, err := archive.Create("resources/sha1-body")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(resource, "body"); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	started := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	result := RunResult{
		RunID:      "run-1",
		Status:     "failed",
		Failure:    "test failed",
		StartedAt:  started,
		FinishedAt: started.Add(2 * time.Second),
		DurationMS: 2000,
	}
	summary, err := summarizeTrace(path, &result, 2)
	if err != nil {
		t.Fatal(err)
	}
	if summary.RunID != "run-1" || summary.ActionCount != 2 || summary.SnapshotCount != 1 || summary.ResourceCount != 1 {
		t.Fatalf("trace summary = %#v", summary)
	}
	if summary.FailingAction == nil || summary.FailingAction.Index != 2 || summary.FailingAction.Locator != "internal:role=button[name=Save]" || !strings.Contains(summary.FailingAction.Error, "detached") {
		t.Fatalf("failing action = %#v", summary.FailingAction)
	}
	if len(summary.NearbyActions) != 2 || summary.NearbyActions[0].APIName != "page.goto" {
		t.Fatalf("nearby actions = %#v", summary.NearbyActions)
	}
	if len(summary.Snapshots) != 1 || !strings.Contains(summary.Snapshots[0].Excerpt, "Save changes") {
		t.Fatalf("trace snapshots = %#v", summary.Snapshots)
	}
	if summary.Artifacts["trace"] != path || summary.Artifacts["stdout"] != "" {
		t.Fatalf("trace artifacts = %#v", summary.Artifacts)
	}
}
