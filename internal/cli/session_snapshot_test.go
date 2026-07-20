package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSemanticSnapshotPreservesDeepControlsAndCollapsesWrappers(t *testing.T) {
	snapshot := `- main [ref=e1]:
  - generic [ref=e2]:
    - generic [ref=e3]:
      - generic [ref=e4]:
        - generic [ref=e5]:
          - generic [ref=e6]:
            - generic [ref=e7]:
              - button "Save" [ref=e8]
              - heading "Editor" [level=2] [ref=e9]`
	view := semanticSnapshot(snapshot)
	if !strings.Contains(view.Text, `button "Save" [ref=e8]`) || !strings.Contains(view.Text, `heading "Editor"`) {
		t.Fatalf("semantic snapshot omitted deep controls:\n%s", view.Text)
	}
	if count := strings.Count(view.Text, "generic"); count > 1 {
		t.Fatalf("semantic snapshot retained redundant wrappers (%d):\n%s", count, view.Text)
	}
}

func TestSemanticSnapshotDeltaReturnsChangedRefsAndContent(t *testing.T) {
	previous := snapshotFixture("e", "Switch to dark theme")
	current := snapshotFixture("f", "Switch to light theme")
	view := semanticSnapshotDelta(previous, current, "e2")
	if view.Mode != "delta" {
		t.Fatalf("snapshot mode = %q, want delta:\n%s", view.Mode, view.Text)
	}
	for _, expected := range []string{`button "Switch to light theme" [ref=f2]`, "Removed:", "Switch to dark theme"} {
		if !strings.Contains(view.Text, expected) {
			t.Fatalf("snapshot delta omitted %q:\n%s", expected, view.Text)
		}
	}

	refOnly := semanticSnapshotDelta(snapshotFixture("e", "Save"), snapshotFixture("f", "Save"), "e2")
	if refOnly.Mode != "delta" || !strings.Contains(refOnly.Text, `[ref=f2]`) {
		t.Fatalf("target ref refresh was omitted:\n%s", refOnly.Text)
	}

	var duplicatesBefore, duplicatesAfter strings.Builder
	duplicatesBefore.WriteString("- main [ref=e1]:\n")
	duplicatesAfter.WriteString("- main [ref=f1]:\n")
	for index := 2; index <= 12; index++ {
		fmt.Fprintf(&duplicatesBefore, "  - button \"Save\" [ref=e%d]\n", index)
		fmt.Fprintf(&duplicatesAfter, "  - button \"Save\" [ref=f%d]\n", index)
	}
	duplicateTarget := semanticSnapshotDelta(duplicatesBefore.String(), duplicatesAfter.String(), "e8")
	if !strings.Contains(duplicateTarget.Text, `[ref=f8]`) || strings.Contains(duplicateTarget.Text, `[ref=f2]`) {
		t.Fatalf("duplicate target mapped to the wrong fresh ref:\n%s", duplicateTarget.Text)
	}
}

func TestSemanticSnapshotBudgetAndExpandedOverride(t *testing.T) {
	snapshot := largeSnapshotFixture(3_000)
	compact := semanticSnapshot(snapshot)
	if compact.Omitted == 0 || !strings.Contains(compact.Text, "semantic nodes omitted") {
		t.Fatalf("large snapshot was not bounded: omitted=%d bytes=%d", compact.Omitted, len(compact.Text))
	}
	if len(compact.Text) > defaultSnapshotBudgetBytes+512 {
		t.Fatalf("bounded snapshot is too large: %d bytes", len(compact.Text))
	}
	expanded := expandedSemanticSnapshot(snapshot)
	if expanded.Omitted != 0 || !strings.Contains(expanded.Text, `button "Action 2999"`) {
		t.Fatalf("expanded snapshot remained truncated: omitted=%d", expanded.Omitted)
	}
}

func TestSemanticSnapshotKeepsNamedGeneric(t *testing.T) {
	view := semanticSnapshot("- main [ref=e1]:\n  - generic \"Loading status\" [ref=e2]\n")
	if !strings.Contains(view.Text, `generic "Loading status"`) {
		t.Fatalf("named generic was omitted:\n%s", view.Text)
	}
}

func TestSessionSnapshotPayloadExtractsVerboseYAML(t *testing.T) {
	output := "### Snapshot\n```yaml\n- button \"Save\" [ref=e2]\n```\n"
	snapshot, ok := sessionSnapshotPayload(Project{}, SessionState{}, output)
	if !ok || snapshot != `- button "Save" [ref=e2]` {
		t.Fatalf("snapshot payload = %q, %v", snapshot, ok)
	}
}

func TestStoreEmptySnapshotReplacesStaleState(t *testing.T) {
	directory := t.TempDir()
	previous := filepath.Join(directory, "previous.yml")
	if err := os.WriteFile(previous, []byte("- button \"Old\" [ref=e2]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := SessionState{SessionDir: directory, LastSnapshot: previous}
	statePath := filepath.Join(directory, "session.json")
	view, err := storeSessionSnapshot(&state, statePath, 2, "", false, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if view.Text != "No semantic content." || state.LastSnapshot == previous {
		t.Fatalf("empty snapshot did not replace stale state: %#v, %#v", view, state)
	}
}

func BenchmarkSemanticSnapshotLargeDOM(b *testing.B) {
	snapshot := largeSnapshotFixture(2_000)
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		_ = semanticSnapshot(snapshot)
	}
}

func BenchmarkSemanticSnapshotDeltaLargeDOM(b *testing.B) {
	previous := largeSnapshotFixture(2_000)
	current := strings.Replace(previous, `button "Action 1000"`, `button "Action changed"`, 1)
	b.ReportAllocs()
	for index := 0; index < b.N; index++ {
		_ = semanticSnapshotDelta(previous, current, "e1001")
	}
}

func snapshotFixture(prefix, button string) string {
	var output strings.Builder
	fmt.Fprintf(&output, "- main [ref=%s1]:\n", prefix)
	fmt.Fprintf(&output, "  - button %q [ref=%s2]\n", button, prefix)
	for index := 0; index < 10; index++ {
		fmt.Fprintf(&output, "  - paragraph [ref=%s%d]: Stable text %d\n", prefix, index+3, index)
	}
	return output.String()
}

func largeSnapshotFixture(nodes int) string {
	var output strings.Builder
	output.WriteString("- main [ref=e1]:\n")
	for index := 0; index < nodes; index++ {
		fmt.Fprintf(&output, "  - button \"Action %d\" [ref=e%d]\n", index, index+2)
	}
	return output.String()
}
