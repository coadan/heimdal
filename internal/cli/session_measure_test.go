package cli

import (
	"strings"
	"testing"
)

func TestParseLayoutMeasurementSupportsRawAndWrappedJSON(t *testing.T) {
	payload := `{"viewport":{"width":1280,"height":720,"pixel_ratio":2,"scroll_x":0,"scroll_y":10},"document":{"width":1300,"height":2000,"horizontal_overflow":20,"text_characters":400,"text_lines":20},"counts":{"elements":50,"visible":40,"interactive":8,"headings":3,"landmarks":2,"images":1},"small_controls":[{"element":"button \"Save\"","x":10,"y":20,"width":30,"height":30,"detail":"below 44px touch target"}]}`
	for _, output := range []string{payload, "### Result\n```json\n" + payload + "\n```\n### Ran Playwright code"} {
		measurement, err := parseLayoutMeasurement(output)
		if err != nil {
			t.Fatal(err)
		}
		if measurement.Viewport.Width != 1280 || measurement.Document.HorizontalOverflow != 20 || measurement.Counts.Interactive != 8 || len(measurement.SmallControls) != 1 {
			t.Fatalf("measurement = %#v", measurement)
		}
	}
}

func TestLayoutMeasurementScriptIsBoundedAndReadOnly(t *testing.T) {
	for _, required := range []string{"slice(0, 8)", "horizontal_overflow", "small_controls", "getComputedStyle(target)"} {
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
