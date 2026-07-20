package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type LayoutViewport struct {
	Width      float64 `json:"width"`
	Height     float64 `json:"height"`
	PixelRatio float64 `json:"pixel_ratio"`
	ScrollX    float64 `json:"scroll_x"`
	ScrollY    float64 `json:"scroll_y"`
}

type LayoutDocument struct {
	Width              float64 `json:"width"`
	Height             float64 `json:"height"`
	HorizontalOverflow float64 `json:"horizontal_overflow"`
	TextCharacters     int     `json:"text_characters"`
	TextLines          int     `json:"text_lines"`
}

type LayoutCounts struct {
	Elements    int `json:"elements"`
	Visible     int `json:"visible"`
	Interactive int `json:"interactive"`
	Headings    int `json:"headings"`
	Landmarks   int `json:"landmarks"`
	Images      int `json:"images"`
}

type LayoutElementSample struct {
	Element string  `json:"element"`
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	Width   float64 `json:"width"`
	Height  float64 `json:"height"`
	Detail  string  `json:"detail,omitempty"`
}

type LayoutRegion struct {
	LayoutElementSample
	Selector      string `json:"selector,omitempty"`
	Display       string `json:"display,omitempty"`
	GridColumns   string `json:"grid_columns,omitempty"`
	FlexDirection string `json:"flex_direction,omitempty"`
	FlexWrap      string `json:"flex_wrap,omitempty"`
	AlignItems    string `json:"align_items,omitempty"`
	Padding       string `json:"padding,omitempty"`
	Gap           string `json:"gap,omitempty"`
	OverflowX     string `json:"overflow_x,omitempty"`
	OverflowY     string `json:"overflow_y,omitempty"`
	ChildCount    int    `json:"child_count"`
}

type LayoutTarget struct {
	LayoutElementSample
	Display      string `json:"display,omitempty"`
	Position     string `json:"position,omitempty"`
	FontFamily   string `json:"font_family,omitempty"`
	FontSize     string `json:"font_size,omitempty"`
	FontWeight   string `json:"font_weight,omitempty"`
	LineHeight   string `json:"line_height,omitempty"`
	Color        string `json:"color,omitempty"`
	Background   string `json:"background,omitempty"`
	Padding      string `json:"padding,omitempty"`
	Gap          string `json:"gap,omitempty"`
	BorderRadius string `json:"border_radius,omitempty"`
}

type LayoutMeasurement struct {
	Viewport      LayoutViewport        `json:"viewport"`
	Document      LayoutDocument        `json:"document"`
	Counts        LayoutCounts          `json:"counts"`
	Regions       []LayoutRegion        `json:"regions,omitempty"`
	Controls      []LayoutElementSample `json:"controls,omitempty"`
	Content       []LayoutElementSample `json:"content,omitempty"`
	Overflowing   []LayoutElementSample `json:"overflowing,omitempty"`
	Clipped       []LayoutElementSample `json:"clipped,omitempty"`
	SmallControls []LayoutElementSample `json:"small_controls,omitempty"`
	Target        *LayoutTarget         `json:"target,omitempty"`
}

func runSessionMeasure(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseSessionOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if len(options.Forwarded) > 1 {
		return reportError(options.JSON, errors.New("session measure accepts at most one target"), out, errOut)
	}
	project, state, statePath, err := discoverSession(options)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	logicalArgs := []string{"measure"}
	runtimeArgs := []string{"eval", layoutMeasurementScript}
	if len(options.Forwarded) == 1 {
		logicalArgs = append(logicalArgs, options.Forwarded[0])
		runtimeArgs = append(runtimeArgs, options.Forwarded[0])
	}
	result, commandErr := runSessionCommandModeArgs(ctx, project, &state, statePath, logicalArgs, runtimeArgs, "", true)
	response := sessionResponse(state, result, commandErr)
	response.CompactJSON = !options.FullJSON
	response.Command = logicalArgs
	if commandErr == nil {
		measurement, err := parseLayoutMeasurement(result.Stdout)
		if err != nil {
			commandErr = err
		} else {
			response.Status = "passed"
			response.Output = "measured layout"
			response.Measurement = &measurement
		}
	}
	if commandErr != nil {
		response.Status = "failed"
		response.Error = commandErr.Error()
		if detail := compactCLIOutput(joinOutputs(result.Stdout, result.Stderr)); detail != "" {
			response.Error = truncateDisplay(detail, 800)
		}
	}
	return printSessionResponse(out, errOut, response, options.JSON)
}

func parseLayoutMeasurement(output string) (LayoutMeasurement, error) {
	clean := strings.TrimSpace(stripANSI(output))
	if start := strings.Index(clean, "### Result"); start >= 0 {
		clean = strings.TrimSpace(clean[start+len("### Result"):])
	}
	if end := strings.Index(clean, "\n### "); end >= 0 {
		clean = strings.TrimSpace(clean[:end])
	}
	clean = strings.TrimSpace(strings.Trim(clean, "`"))
	if strings.HasPrefix(clean, "json\n") {
		clean = strings.TrimSpace(strings.TrimPrefix(clean, "json\n"))
	}
	var measurement LayoutMeasurement
	if err := json.Unmarshal([]byte(clean), &measurement); err != nil {
		return LayoutMeasurement{}, fmt.Errorf("parse Playwright layout measurement: %w", err)
	}
	return measurement, nil
}

const layoutMeasurementScript = `target => {
  const round = value => Math.round(value * 10) / 10;
  const selector = element => {
    if (element.id) return '#' + CSS.escape(element.id);
    const testID = element.getAttribute('data-testid');
    if (testID) return '[data-testid="' + CSS.escape(testID) + '"]';
    const role = element.getAttribute('role');
    if (role) return element.tagName.toLowerCase() + '[role=' + role + ']';
    const classes = [...element.classList].slice(0, 2).map(name => '.' + CSS.escape(name)).join('');
    return element.tagName.toLowerCase() + classes;
  };
  const describe = element => {
    const role = element.getAttribute('role');
    const label = element.getAttribute('aria-label');
    const text = (label || element.innerText || element.getAttribute('alt') || '').replace(/\s+/g, ' ').trim().slice(0, 80);
    return element.tagName.toLowerCase() + (role ? '[role=' + role + ']' : '') + (text ? ' "' + text + '"' : '');
  };
  const sample = (element, detail) => {
    const rect = element.getBoundingClientRect();
    return { element: describe(element), x: round(rect.x), y: round(rect.y), width: round(rect.width), height: round(rect.height), ...(detail ? { detail } : {}) };
  };
  const elements = [...document.querySelectorAll('body *')];
  const visible = elements.filter(element => {
    const style = getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    return style.display !== 'none' && style.visibility !== 'hidden' && Number(style.opacity) !== 0 && rect.width > 0 && rect.height > 0;
  });
  const interactiveSelector = 'a[href],button,input,select,textarea,summary,[role=button],[role=link],[role=checkbox],[role=radio],[role=tab],[tabindex]';
  const interactive = visible.filter(element => element.matches(interactiveSelector));
  const regionSelector = 'main,nav,header,footer,aside,section,article,form,[role=main],[role=navigation],[role=banner],[role=contentinfo],[role=complementary],[role=region]';
  const regionCandidates = visible.filter(element => {
    const style = getComputedStyle(element);
    const layout = style.display === 'grid' || style.display === 'flex';
    const padded = [style.paddingTop, style.paddingRight, style.paddingBottom, style.paddingLeft].some(value => parseFloat(value) > 0);
    const scrolling = ['auto', 'scroll'].includes(style.overflowX) || ['auto', 'scroll'].includes(style.overflowY);
    const structural = element.classList.length > 0 && (padded || scrolling) && element.getBoundingClientRect().width * element.getBoundingClientRect().height > 1000;
    return element.matches(regionSelector) || (layout && element.children.length > 1) || structural;
  }).map(element => {
    const style = getComputedStyle(element);
    const rect = element.getBoundingClientRect();
    const semantic = element.matches(regionSelector) ? 4 : 0;
    const layout = style.display === 'grid' || style.display === 'flex' ? 2 : 0;
    const structural = element.classList.length > 0 ? 1 : 0;
    return { element, style, score: semantic + layout + structural, area: rect.width * rect.height };
  }).sort((a, b) => b.score - a.score || b.area - a.area).slice(0, 16);
  const regions = regionCandidates.map(({ element, style }) => ({
    ...sample(element), selector: selector(element), display: style.display,
    ...(style.display === 'grid' ? { grid_columns: style.gridTemplateColumns } : {}),
    ...(style.display === 'flex' ? { flex_direction: style.flexDirection, flex_wrap: style.flexWrap, align_items: style.alignItems } : {}),
    padding: style.padding, gap: style.gap, overflow_x: style.overflowX, overflow_y: style.overflowY,
    child_count: element.children.length
  }));
  const controls = interactive.slice(0, 12).map(element => sample(element));
  const content = visible.filter(element => {
    const text = (element.innerText || '').trim();
    const semanticText = element.matches('h1,h2,h3,h4,h5,h6,p,li,dt,dd,blockquote,figcaption,[role=heading],[role=status],[role=alert]');
    const leafText = element.children.length === 0 && !element.matches(interactiveSelector) && text.length > 0 && text.length <= 240;
    return text && (semanticText || leafText);
  })
    .sort((a, b) => a.getBoundingClientRect().top - b.getBoundingClientRect().top || a.getBoundingClientRect().left - b.getBoundingClientRect().left)
    .slice(0, 12).map(element => sample(element));
  const overflowing = visible.filter(element => {
    const rect = element.getBoundingClientRect();
    return rect.left < -1 || rect.right > innerWidth + 1;
  }).slice(0, 8).map(element => sample(element, 'outside viewport'));
  const clipped = visible.filter(element => element.scrollWidth > element.clientWidth + 1 || element.scrollHeight > element.clientHeight + 1).slice(0, 8).map(element => sample(element, 'content exceeds client box'));
  const smallControls = interactive.filter(element => {
    const rect = element.getBoundingClientRect();
    return rect.width < 44 || rect.height < 44;
  }).slice(0, 8).map(element => sample(element, 'below 44px touch target'));
  const root = document.documentElement;
  const bodyText = (document.body?.innerText || '').trim();
  const result = {
    viewport: { width: innerWidth, height: innerHeight, pixel_ratio: devicePixelRatio, scroll_x: round(scrollX), scroll_y: round(scrollY) },
    document: { width: root.scrollWidth, height: root.scrollHeight, horizontal_overflow: Math.max(0, root.scrollWidth - innerWidth), text_characters: bodyText.length, text_lines: bodyText ? bodyText.split(/\n+/).length : 0 },
    counts: { elements: elements.length, visible: visible.length, interactive: interactive.length, headings: document.querySelectorAll('h1,h2,h3,h4,h5,h6,[role=heading]').length, landmarks: document.querySelectorAll('main,nav,header,footer,aside,[role=main],[role=navigation],[role=banner],[role=contentinfo],[role=complementary]').length, images: document.images.length },
    regions,
    controls,
    content,
    overflowing,
    clipped,
    small_controls: smallControls
  };
  if (target) {
    const style = getComputedStyle(target);
    result.target = { ...sample(target), display: style.display, position: style.position, font_family: style.fontFamily, font_size: style.fontSize, font_weight: style.fontWeight, line_height: style.lineHeight, color: style.color, background: style.backgroundColor, padding: style.padding, gap: style.gap, border_radius: style.borderRadius };
  }
  return result;
}`
