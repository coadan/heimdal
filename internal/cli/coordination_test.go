package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCoordinationRejectsTraversalSelectors(t *testing.T) {
	root, _ := coordinationTestRun(t, "run-1")
	payloadPath := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(payloadPath, []byte(`{"secret":"value"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	code, output, _ := invokeCoordination(t, runMetadata, []string{
		"publish", "../escape", "--root", root, "--file", payloadPath, "--json",
	})
	if code == 0 {
		t.Fatal("metadata traversal selector was accepted")
	}
	if strings.Contains(output, "secret") || strings.Contains(output, "value") {
		t.Fatalf("metadata payload was echoed in an error: %s", output)
	}

	code, _, _ = invokeCoordination(t, runSignal, []string{
		"send", "../../escape", "--root", root, "--run", "run-1", "--json",
	})
	if code == 0 {
		t.Fatal("signal traversal selector was accepted")
	}
	code, _, _ = invokeCoordination(t, runSignal, []string{
		"send", "ready", "--root", root, "--run", "../run-1", "--json",
	})
	if code == 0 {
		t.Fatal("run traversal selector was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "escape")); !os.IsNotExist(err) {
		t.Fatalf("traversal created an unexpected path: %v", err)
	}
}

func TestCoordinationRejectsConflictingDirectoryAliases(t *testing.T) {
	root, _ := coordinationTestRun(t, "run-1")
	t.Setenv("HEIMDAL_RUN_DIR", "")
	code, output, _ := invokeCoordination(t, runSignal, []string{
		"send", "ready", "--dir", root, "--root", filepath.Join(root, "other"), "--json",
	})
	if code == 0 {
		t.Fatal("conflicting directory aliases were accepted")
	}
	var response coordinationErrorResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatalf("error response was not JSON: %v; output=%s", err, output)
	}
	if response.SchemaVersion != coordinationSchemaVersion || response.Status != "error" {
		t.Fatalf("unexpected error response: %#v", response)
	}
	if !strings.Contains(response.Error, "cannot specify different") {
		t.Fatalf("error did not identify the conflicting aliases: %q", response.Error)
	}
}

func TestCoordinationUsesEnvironmentRunDirectoryDirectly(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run-from-env")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEIMDAL_RUN_DIR", runDir)

	code, output, errOutput := invokeCoordination(t, runSignal, []string{"send", "ready", "--json"})
	if code != 0 {
		t.Fatalf("signal send failed: code=%d output=%s error=%s", code, output, errOutput)
	}
	var response signalResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatalf("signal response was not JSON: %v; output=%s", err, output)
	}
	if response.RunID != "run-from-env" || response.Status != "sent" {
		t.Fatalf("unexpected environment-selected run response: %#v", response)
	}
	if _, err := os.Stat(filepath.Join(runDir, "signals", "ready")); err != nil {
		t.Fatalf("signal was not written to HEIMDAL_RUN_DIR: %v", err)
	}
}

func TestCoordinationLatestSelectsActiveRunMarker(t *testing.T) {
	root, oldRun := coordinationTestRun(t, "old-run")
	newRun := filepath.Join(root, defaultArtifactDir, "new-run")
	if err := os.MkdirAll(newRun, 0o755); err != nil {
		t.Fatal(err)
	}
	activeMarker := filepath.Join(newRun, "run.json")
	oldTime := time.Now().Add(-2 * time.Hour).UTC()
	newTime := time.Now().Add(-time.Hour).UTC()
	if err := os.Remove(filepath.Join(oldRun, "result.json")); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(oldRun, "result.json"), RunResult{
		SchemaVersion: 1,
		RunID:         "old-run",
		Status:        "passed",
		StartedAt:     oldTime,
		FinishedAt:    time.Now().UTC(),
		Artifacts:     Artifacts{RunDir: oldRun},
	}); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(activeMarker, RunManifest{
		SchemaVersion: 1,
		RunID:         "new-run",
		Status:        "running",
		StartedAt:     newTime,
		Artifacts:     Artifacts{RunDir: newRun},
	}); err != nil {
		t.Fatal(err)
	}
	oldMarker := filepath.Join(oldRun, "result.json")
	if err := os.Chtimes(oldMarker, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(activeMarker, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HEIMDAL_RUN_DIR", "")

	code, output, errOutput := invokeCoordination(t, runSignal, []string{"send", "ready", "--root", root, "--json"})
	if code != 0 {
		t.Fatalf("latest signal send failed: code=%d output=%s error=%s", code, output, errOutput)
	}
	var response signalResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatal(err)
	}
	if response.RunID != "new-run" {
		t.Fatalf("latest selected run %q, want new-run", response.RunID)
	}
	if _, err := os.Stat(filepath.Join(newRun, "signals", "ready")); err != nil {
		t.Fatalf("active run did not receive signal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(oldRun, "signals", "ready")); !os.IsNotExist(err) {
		t.Fatalf("older run unexpectedly received signal: %v", err)
	}
}

func TestCoordinationMetadataRejectsOversizeAndInvalidJSON(t *testing.T) {
	root, _ := coordinationTestRun(t, "run-1")
	t.Setenv("HEIMDAL_RUN_DIR", "")
	oversizePath := filepath.Join(t.TempDir(), "oversize.json")
	if err := os.WriteFile(oversizePath, bytes.Repeat([]byte("x"), coordinationMaxMetadataBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, _ := invokeCoordination(t, runMetadata, []string{
		"publish", "state", "--root", root, "--run", "run-1", "--file", oversizePath, "--json",
	})
	if code == 0 {
		t.Fatal("oversize metadata payload was accepted")
	}
	if _, err := os.Stat(filepath.Join(root, defaultArtifactDir, "run-1", "metadata")); !os.IsNotExist(err) {
		t.Fatalf("oversize publication created metadata state: %v", err)
	}

	invalidPath := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(invalidPath, []byte(`{"secret":`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, output, _ := invokeCoordination(t, runMetadata, []string{
		"publish", "state", "--root", root, "--run", "run-1", "--file", invalidPath, "--json",
	})
	if code == 0 {
		t.Fatal("invalid JSON metadata payload was accepted")
	}
	if strings.Contains(output, "secret") {
		t.Fatalf("invalid metadata was echoed in error response: %s", output)
	}
}

func TestCoordinationMetadataUsesNewestImmutablePublication(t *testing.T) {
	root, _ := coordinationTestRun(t, "run-1")
	t.Setenv("HEIMDAL_RUN_DIR", "")
	first := filepath.Join(t.TempDir(), "first.json")
	second := filepath.Join(t.TempDir(), "second.json")
	if err := os.WriteFile(first, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{first, second} {
		code, output, errOutput := invokeCoordination(t, runMetadata, []string{
			"publish", "state", "--root", root, "--run", "run-1", "--file", file, "--json",
		})
		if code != 0 {
			t.Fatalf("metadata publish failed: code=%d output=%s error=%s", code, output, errOutput)
		}
	}
	code, output, errOutput := invokeCoordination(t, runMetadata, []string{
		"get", "state", "--root", root, "--run", "run-1", "--json",
	})
	if code != 0 {
		t.Fatalf("metadata get failed: code=%d output=%s error=%s", code, output, errOutput)
	}
	var response struct {
		Metadata json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(response.Metadata, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Version != 2 {
		t.Fatalf("metadata get selected version %d, want 2", payload.Version)
	}

	entries, err := os.ReadDir(filepath.Join(root, defaultArtifactDir, "run-1", "metadata", "state"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("publication count = %d, want 2", len(entries))
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("publication %s mode = %o, want 600", entry.Name(), info.Mode().Perm())
		}
	}
	oldest := filepath.Join(root, defaultArtifactDir, "run-1", "metadata", "state", entries[0].Name())
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(oldest, future, future); err != nil {
		t.Fatal(err)
	}
	publication, err := latestCoordinationMetadata(filepath.Join(root, defaultArtifactDir, "run-1"), "state")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(publication.Payload, []byte(`"version":2`)) {
		t.Fatalf("metadata selection depended on mutable file time: %s", publication.Payload)
	}
}

func TestCoordinationRejectsUnversionedMetadataFiles(t *testing.T) {
	_, runDir := coordinationTestRun(t, "run-1")
	if _, err := publishCoordinationMetadata(runDir, "state", []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runDir, "metadata", "state", "latest.json")
	if err := os.WriteFile(path, []byte(`{"spoofed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := latestCoordinationMetadata(runDir, "state"); err == nil || !strings.Contains(err.Error(), "invalid metadata publication name") {
		t.Fatalf("unversioned metadata should fail clearly, got %v", err)
	}
	report, _, err := readRunReport(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if report.(RunResult).MetadataError == "" {
		t.Fatal("report did not surface corrupt metadata")
	}
}

func TestCoordinationMetadataDefaultsToStdin(t *testing.T) {
	root, _ := coordinationTestRun(t, "run-1")
	t.Setenv("HEIMDAL_RUN_DIR", "")
	oldStdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	defer func() {
		os.Stdin = oldStdin
		_ = reader.Close()
	}()
	go func() {
		_, _ = writer.Write([]byte(`{"from":"stdin"}`))
		_ = writer.Close()
	}()

	code, output, errOutput := invokeCoordination(t, runMetadata, []string{
		"publish", "state", "--root", root, "--run", "run-1", "--json",
	})
	if code != 0 {
		t.Fatalf("stdin metadata publish failed: code=%d output=%s error=%s", code, output, errOutput)
	}
}

func TestCoordinationSignalSendIsIdempotentAndWaitAcceptsPreSent(t *testing.T) {
	root, _ := coordinationTestRun(t, "run-1")
	t.Setenv("HEIMDAL_RUN_DIR", "")
	args := []string{"send", "ready", "--root", root, "--run", "run-1", "--json"}
	code, output, errOutput := invokeCoordination(t, runSignal, args)
	if code != 0 {
		t.Fatalf("first signal send failed: code=%d output=%s error=%s", code, output, errOutput)
	}
	var first signalResponse
	if err := json.Unmarshal([]byte(output), &first); err != nil {
		t.Fatal(err)
	}
	if first.Status != "sent" || first.AlreadySent {
		t.Fatalf("unexpected first send response: %#v", first)
	}

	code, output, errOutput = invokeCoordination(t, runSignal, args)
	if code != 0 {
		t.Fatalf("second signal send failed: code=%d output=%s error=%s", code, output, errOutput)
	}
	var second signalResponse
	if err := json.Unmarshal([]byte(output), &second); err != nil {
		t.Fatal(err)
	}
	if second.Status != "already_sent" || !second.AlreadySent {
		t.Fatalf("unexpected idempotent send response: %#v", second)
	}

	code, output, errOutput = invokeCoordination(t, runSignal, []string{
		"wait", "ready", "--root", root, "--run", "run-1", "--timeout", "1ms", "--json",
	})
	if code != 0 {
		t.Fatalf("pre-sent signal wait failed: code=%d output=%s error=%s", code, output, errOutput)
	}
	var waited signalResponse
	if err := json.Unmarshal([]byte(output), &waited); err != nil {
		t.Fatal(err)
	}
	if waited.Status != "received" {
		t.Fatalf("unexpected pre-sent wait response: %#v", waited)
	}

	info, err := os.Stat(filepath.Join(root, defaultArtifactDir, "run-1", "signals", "ready"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("signal mode = %o, want 600", info.Mode().Perm())
	}
}

func TestCoordinationSignalWaitTimesOut(t *testing.T) {
	root, _ := coordinationTestRun(t, "run-1")
	t.Setenv("HEIMDAL_RUN_DIR", "")
	started := time.Now()
	code, output, errOutput := invokeCoordination(t, runSignal, []string{
		"wait", "missing", "--root", root, "--run", "run-1", "--timeout", "15ms", "--json",
	})
	if code == 0 {
		t.Fatal("missing signal wait unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("bounded signal wait took too long: %s", elapsed)
	}
	var response coordinationErrorResponse
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		t.Fatalf("timeout response was not JSON: %v; output=%s error=%s", err, output, errOutput)
	}
	if response.SchemaVersion != coordinationSchemaVersion || response.Status != "error" {
		t.Fatalf("unexpected timeout response: %#v", response)
	}
}

func TestCoordinationConcurrentSignalSendCreatesOneMarker(t *testing.T) {
	root, runDir := coordinationTestRun(t, "run-1")
	const senders = 32
	results := make(chan struct {
		created bool
		err     error
	}, senders)
	var group sync.WaitGroup
	group.Add(senders)
	for i := 0; i < senders; i++ {
		go func() {
			defer group.Done()
			created, err := sendCoordinationSignal(runDir, "concurrent")
			results <- struct {
				created bool
				err     error
			}{created: created, err: err}
		}()
	}
	group.Wait()
	close(results)

	createdCount := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("concurrent sends reported %d creators, want 1", createdCount)
	}
	entries, err := os.ReadDir(filepath.Join(runDir, "signals"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "concurrent" {
		t.Fatalf("signal directory entries = %#v, want one concurrent marker", entries)
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("concurrent signal mode = %o, want 600", info.Mode().Perm())
	}
	rootInfo, err := os.Stat(filepath.Join(root, defaultArtifactDir, "run-1", "signals"))
	if err != nil {
		t.Fatal(err)
	}
	if rootInfo.Mode().Perm() != 0o700 {
		t.Fatalf("signal directory mode = %o, want 700", rootInfo.Mode().Perm())
	}
}

func coordinationTestRun(t *testing.T, runID string) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(root, defaultArtifactDir, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(filepath.Join(runDir, "result.json"), RunResult{
		SchemaVersion: 1,
		RunID:         runID,
		Status:        "passed",
		StartedAt:     time.Now().UTC(),
		FinishedAt:    time.Now().UTC(),
		Artifacts:     Artifacts{RunDir: runDir},
	}); err != nil {
		t.Fatal(err)
	}
	return root, runDir
}

func invokeCoordination(t *testing.T, handler func(context.Context, []string, io.Writer, io.Writer) int, args []string) (int, string, string) {
	t.Helper()
	var output strings.Builder
	var errOutput strings.Builder
	code := handler(context.Background(), args, &output, &errOutput)
	return code, output.String(), errOutput.String()
}
