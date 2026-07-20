package cli

import (
	"strings"
	"testing"
)

func TestParseLayoutMeasurementSupportsRawAndWrappedJSON(t *testing.T) {
	payload := `{"viewport":{"width":1280,"height":720,"pixel_ratio":2,"scroll_x":0,"scroll_y":10},"document":{"width":1300,"height":2000,"horizontal_overflow":20,"text_characters":400,"text_lines":20},"counts":{"elements":50,"visible":40,"interactive":8,"headings":3,"landmarks":2,"images":1},"regions":[{"element":"main","selector":"main","x":8,"y":20,"width":1264,"height":600,"display":"grid","grid_columns":"400px 848px","padding":"16px","gap":"16px","overflow_x":"visible","overflow_y":"visible","child_count":2}],"controls":[{"element":"button \"Save\"","x":10,"y":20,"width":44,"height":44}],"content":[{"element":"h1 \"Dashboard\"","x":8,"y":24,"width":200,"height":40}],"small_controls":[{"element":"button \"Cancel\"","x":10,"y":70,"width":30,"height":30,"detail":"below 44px touch target"}]}`
	for _, output := range []string{payload, "### Result\n```json\n" + payload + "\n```\n### Ran Playwright code"} {
		measurement, err := parseLayoutMeasurement(output)
		if err != nil {
			t.Fatal(err)
		}
		if measurement.Viewport.Width != 1280 || measurement.Document.HorizontalOverflow != 20 || measurement.Counts.Interactive != 8 || len(measurement.SmallControls) != 1 {
			t.Fatalf("measurement = %#v", measurement)
		}
		if len(measurement.Regions) != 1 || measurement.Regions[0].GridColumns != "400px 848px" || measurement.Regions[0].ChildCount != 2 || len(measurement.Controls) != 1 || len(measurement.Content) != 1 {
			t.Fatalf("decision evidence = %#v", measurement)
		}
	}
}

func TestLayoutMeasurementScriptIsBoundedAndReadOnly(t *testing.T) {
	for _, required := range []string{"slice(0, 8)", "slice(0, 12)", "slice(0, 16)", "horizontal_overflow", "small_controls", "grid_columns", "flex_direction", "structural", "children.length === 0", "controls", "content", "getComputedStyle(target)"} {
		if !strings.Contains(layoutMeasurementScript, required) {
			t.Fatalf("measurement script omitted %q", required)
		}
	}
	for _, mutation := range []string{".click(", ".fill(", "dispatchEvent", "innerHTML ="} {
		if strings.Contains(layoutMeasurementScript, mutation) {
			t.Fatalf("measurement script mutates page via %q", mutation)
		}
	}
}
