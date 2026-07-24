package cli

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

func planStableSessionAction(ctx context.Context, project Project, state *SessionState, statePath, action string, logicalArgs []string) ([]string, string, string, error) {
	args := logicalArgs[1:]
	switch action {
	case "press":
		if len(args) == 1 {
			return logicalArgs, "", "", nil
		}
		if len(args) != 2 {
			return nil, "", sessionActionCorrection(action), errors.New("press accepts KEY or TARGET KEY")
		}
		locator, err := resolveSessionLocator(ctx, project, state, statePath, args[0])
		if err != nil {
			return nil, "", sessionActionCorrection(action), err
		}
		code := fmt.Sprintf("async page => { await %s.press(%s); }", locator, jsonString(args[1]))
		return []string{"run-code", code}, locator, "", nil
	case "type":
		if len(args) == 1 {
			return logicalArgs, "", "", nil
		}
		if len(args) != 2 {
			return nil, "", sessionActionCorrection(action), errors.New("type accepts TEXT or TARGET TEXT")
		}
		locator, err := resolveSessionLocator(ctx, project, state, statePath, args[0])
		if err != nil {
			return nil, "", sessionActionCorrection(action), err
		}
		code := fmt.Sprintf("async page => { await %s.pressSequentially(%s); }", locator, jsonString(args[1]))
		return []string{"run-code", code}, locator, "", nil
	case "click":
		if len(args) == 4 && args[0] == "--within" && args[2] == "--at" {
			locator, err := resolveSessionLocator(ctx, project, state, statePath, args[1])
			if err != nil {
				return nil, "", sessionActionCorrection(action), err
			}
			x, y, err := parseRelativePoint(args[3])
			if err != nil {
				return nil, "", sessionActionCorrection(action), err
			}
			code := relativePointerCode(locator, x, y, nil)
			return []string{"run-code", code}, locator, "", nil
		}
		if len(args) == 2 && args[1] == "--force" {
			locator, err := resolveSessionLocator(ctx, project, state, statePath, args[0])
			if err != nil {
				return nil, "", sessionActionCorrection(action), err
			}
			code := fmt.Sprintf("async page => { await %s.click({ force: true }); }", locator)
			return []string{"run-code", code}, locator, "", nil
		}
		if len(args) < 1 || len(args) > 2 {
			return nil, "", sessionActionCorrection(action), errors.New("click accepts TARGET, optional left|right|middle, or --force")
		}
		return logicalArgs, "", "", nil
	case "fill":
		if len(args) == 2 || (len(args) == 3 && args[2] == "--submit") {
			return logicalArgs, "", "", nil
		}
		return nil, "", sessionActionCorrection(action), errors.New("fill accepts TARGET TEXT and optional --submit")
	case "mouse":
		if len(args) != 3 || (args[0] != "click" && args[0] != "move") {
			return nil, "", sessionActionCorrection(action), errors.New("mouse accepts click X Y or move X Y")
		}
		if _, _, err := parseAbsolutePoint(args[1], args[2]); err != nil {
			return nil, "", sessionActionCorrection(action), err
		}
		code := fmt.Sprintf("async page => { await page.mouse.%s(%s, %s); }", args[0], args[1], args[2])
		return []string{"run-code", code}, "", "", nil
	case "pointer":
		if len(args) == 5 && args[0] == "move" && args[1] == "--within" && args[3] == "--at" {
			x, y, err := parseRelativePoint(args[4])
			if err != nil {
				return nil, "", sessionActionCorrection(action), err
			}
			locator, err := resolveSessionLocator(ctx, project, state, statePath, args[2])
			if err != nil {
				return nil, "", sessionActionCorrection(action), err
			}
			return []string{"run-code", relativePointerMoveCode(locator, x, y)}, locator, "", nil
		}
		if len(args) != 7 || args[0] != "drag" || args[1] != "--within" || args[3] != "--from" || args[5] != "--to" {
			return nil, "", sessionActionCorrection(action), errors.New("pointer accepts move --within TARGET --at X%,Y% or drag --within TARGET --from X%,Y% --to X%,Y%")
		}
		fromX, fromY, err := parseRelativePoint(args[4])
		if err != nil {
			return nil, "", sessionActionCorrection(action), err
		}
		toX, toY, err := parseRelativePoint(args[6])
		if err != nil {
			return nil, "", sessionActionCorrection(action), err
		}
		locator, err := resolveSessionLocator(ctx, project, state, statePath, args[2])
		if err != nil {
			return nil, "", sessionActionCorrection(action), err
		}
		to := [2]float64{toX, toY}
		return []string{"run-code", relativePointerCode(locator, fromX, fromY, &to)}, locator, "", nil
	default:
		return logicalArgs, "", "", nil
	}
}

func parseAbsolutePoint(xText, yText string) (float64, float64, error) {
	x, err := strconv.ParseFloat(xText, 64)
	if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
		return 0, 0, fmt.Errorf("mouse X coordinate must be finite and numeric (got %q)", xText)
	}
	y, err := strconv.ParseFloat(yText, 64)
	if err != nil || math.IsNaN(y) || math.IsInf(y, 0) {
		return 0, 0, fmt.Errorf("mouse Y coordinate must be finite and numeric (got %q)", yText)
	}
	return x, y, nil
}

func parseRelativePoint(value string) (float64, float64, error) {
	xText, yText, ok := strings.Cut(value, ",")
	if !ok || !strings.HasSuffix(xText, "%") || !strings.HasSuffix(yText, "%") {
		return 0, 0, fmt.Errorf("relative point must be X%%,Y%% (got %q)", value)
	}
	parse := func(part string) (float64, error) {
		parsed, err := strconv.ParseFloat(strings.TrimSuffix(part, "%"), 64)
		if err != nil || parsed < 0 || parsed > 100 {
			return 0, fmt.Errorf("relative coordinates must be percentages from 0%% to 100%% (got %q)", value)
		}
		return parsed / 100, nil
	}
	x, err := parse(xText)
	if err != nil {
		return 0, 0, err
	}
	y, err := parse(yText)
	return x, y, err
}

func relativePointerCode(locator string, x, y float64, to *[2]float64) string {
	prefix := fmt.Sprintf(`async page => {
  const target = %s;
  const box = await target.boundingBox();
  if (!box) throw new Error('Target has no visible bounding box');
  const x = box.x + box.width * %s;
  const y = box.y + box.height * %s;`, locator, strconv.FormatFloat(x, 'f', -1, 64), strconv.FormatFloat(y, 'f', -1, 64))
	if to == nil {
		return prefix + "\n  await page.mouse.click(x, y);\n}"
	}
	return prefix + fmt.Sprintf(`
  const toX = box.x + box.width * %s;
  const toY = box.y + box.height * %s;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(toX, toY, { steps: 10 });
  await page.mouse.up();
}`, strconv.FormatFloat(to[0], 'f', -1, 64), strconv.FormatFloat(to[1], 'f', -1, 64))
}

func relativePointerMoveCode(locator string, x, y float64) string {
	return fmt.Sprintf(`async page => {
  const target = %s;
  const box = await target.boundingBox();
  if (!box) throw new Error('Target has no visible bounding box');
  await page.mouse.move(box.x + box.width * %s, box.y + box.height * %s);
}`, locator, strconv.FormatFloat(x, 'f', -1, 64), strconv.FormatFloat(y, 'f', -1, 64))
}

func relativePointerTestLines(locator, from, to string) []string {
	if locator == "" {
		return []string{"// TODO: replace the recorded element ref with a semantic locator"}
	}
	x, y, err := parseRelativePoint(from)
	if err != nil {
		return []string{"// TODO: replace malformed recorded relative pointer coordinates"}
	}
	lines := []string{
		"{",
		"  const target = " + locator + ";",
		"  const box = await target.boundingBox();",
		"  if (!box) throw new Error('Target has no visible bounding box');",
	}
	if to == "" {
		lines = append(lines, fmt.Sprintf("  await page.mouse.click(box.x + box.width * %s, box.y + box.height * %s);", strconv.FormatFloat(x, 'f', -1, 64), strconv.FormatFloat(y, 'f', -1, 64)))
		return append(lines, "}")
	}
	toX, toY, err := parseRelativePoint(to)
	if err != nil {
		return []string{"// TODO: replace malformed recorded relative pointer coordinates"}
	}
	lines = append(lines,
		fmt.Sprintf("  await page.mouse.move(box.x + box.width * %s, box.y + box.height * %s);", strconv.FormatFloat(x, 'f', -1, 64), strconv.FormatFloat(y, 'f', -1, 64)),
		"  await page.mouse.down();",
		fmt.Sprintf("  await page.mouse.move(box.x + box.width * %s, box.y + box.height * %s, { steps: 10 });", strconv.FormatFloat(toX, 'f', -1, 64), strconv.FormatFloat(toY, 'f', -1, 64)),
		"  await page.mouse.up();",
		"}",
	)
	return lines
}

func relativePointerMoveTestLines(locator, at string) []string {
	if locator == "" {
		return []string{"// TODO: replace the recorded element ref with a semantic locator"}
	}
	x, y, err := parseRelativePoint(at)
	if err != nil {
		return []string{"// TODO: replace malformed recorded relative pointer coordinates"}
	}
	return []string{
		"{",
		"  const target = " + locator + ";",
		"  const box = await target.boundingBox();",
		"  if (!box) throw new Error('Target has no visible bounding box');",
		fmt.Sprintf("  await page.mouse.move(box.x + box.width * %s, box.y + box.height * %s);", strconv.FormatFloat(x, 'f', -1, 64), strconv.FormatFloat(y, 'f', -1, 64)),
		"}",
	}
}

func resolveSessionLocator(ctx context.Context, project Project, state *SessionState, statePath, target string) (string, error) {
	if strings.HasPrefix(target, "e") && state.LastSnapshot != "" {
		if contents, err := os.ReadFile(state.LastSnapshot); err == nil {
			if locator := locatorFromSessionSnapshot(string(contents), target); locator != "" {
				return locator, nil
			}
		}
	}
	return generateSessionLocator(ctx, project, state, statePath, target)
}

func locatorFromSessionSnapshot(snapshot, target string) string {
	nodes := parseSnapshotTree(snapshot, true)
	targetIndex := -1
	role, name := "", ""
	for index, node := range nodes {
		if node.Ref != target {
			continue
		}
		targetIndex = index
		role, name = snapshotRoleAndName(node.Raw)
		break
	}
	if targetIndex < 0 || role == "" {
		return ""
	}

	total := 0
	for _, node := range nodes {
		candidateRole, candidateName := snapshotRoleAndName(node.Raw)
		if candidateRole != role || candidateName != name {
			continue
		}
		total++
	}
	// A semantic locator is only an exact replacement for Playwright's ref
	// lookup when it identifies one current node. Let generate-locator retain
	// ownership of ambiguous snapshots instead of guessing from DOM order.
	if total != 1 {
		return ""
	}

	locator := "page.getByRole(" + jsonString(role)
	if name != "" {
		locator += ", { name: " + jsonString(name) + ", exact: true }"
	}
	locator += ")"
	return locator
}

func snapshotRoleAndName(line string) (string, string) {
	body := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
	if body == "" {
		return "", ""
	}
	roleEnd := strings.IndexAny(body, " [:")
	if roleEnd < 0 {
		roleEnd = len(body)
	}
	role := body[:roleEnd]
	for _, char := range role {
		if (char < 'a' || char > 'z') && char != '-' {
			return "", ""
		}
	}
	if role == "" || role == "generic" || role == "text" || role == "none" || role == "presentation" {
		return "", ""
	}

	rest := strings.TrimSpace(body[roleEnd:])
	if !strings.HasPrefix(rest, `"`) {
		return role, ""
	}
	for end := 1; end < len(rest); end++ {
		if rest[end] != '"' || rest[end-1] == '\\' {
			continue
		}
		name, err := strconv.Unquote(rest[:end+1])
		if err != nil {
			return role, ""
		}
		return role, name
	}
	return role, ""
}

func generateSessionLocator(ctx context.Context, project Project, state *SessionState, statePath, target string) (string, error) {
	if target == "" {
		return "", errors.New("target must not be empty")
	}
	result, err := runSessionCommandMode(ctx, project, state, statePath, []string{"generate-locator", target}, "", true)
	if err != nil {
		return "", fmt.Errorf("resolve target %s: %w", target, err)
	}
	locator := parseGeneratedLocator(result.Stdout)
	if locator == "" {
		return "", fmt.Errorf("Playwright did not return a locator for target %s", target)
	}
	return locator, nil
}

func parseGeneratedLocator(output string) string {
	for _, line := range strings.Split(stripANSI(output), "\n") {
		line = strings.TrimSpace(strings.Trim(line, "`"))
		if strings.HasPrefix(line, "await ") {
			line = strings.TrimPrefix(line, "await ")
		}
		line = strings.TrimSuffix(line, ";")
		if strings.HasPrefix(line, "page.") {
			return line
		}
		if strings.HasPrefix(line, "getBy") || strings.HasPrefix(line, "locator(") {
			return "page." + line
		}
	}
	return ""
}

func compactSessionGrammarOutput(output string) string {
	lines := strings.Split(strings.TrimSpace(stripANSI(output)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "unknown command") || strings.HasPrefix(lower, "unknown option") || strings.HasPrefix(lower, "error:") {
			return truncateDisplay(line, 400)
		}
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "Usage:") && !strings.HasPrefix(line, "playwright-cli -") {
			return truncateDisplay(line, 400)
		}
	}
	return "Playwright CLI rejected the command shape"
}

func sessionActionCorrection(action string) string {
	switch action {
	case "press":
		return "use `heimdal session press <key>` or `heimdal session press <target> <key>`"
	case "type":
		return "use `heimdal session type <text>` or `heimdal session type <target> <text>`"
	case "fill":
		return "use `heimdal session fill <target> <text> [--submit]`"
	case "click":
		return "use `heimdal session click <target> [left|right|middle|--force]` or `click --within <target> --at <x%,y%>`"
	case "mouse":
		return "use `heimdal session mouse click <x> <y>` or `mouse move <x> <y>`"
	case "pointer":
		return "use `heimdal session pointer move --within <target> --at <x%,y%>` or `pointer drag --within <target> --from <x%,y%> --to <x%,y%>`"
	case "wait":
		return "use `heimdal session wait --role <role> [--name <name>]`, `--text <text>`, or `--change`"
	case "expect":
		return "use `heimdal session expect --role <role> [--name <name>]`, `--text <text>`, `--url <url>`, or `--target <ref> --value <value>`"
	case "reconnect":
		return "use `heimdal session reconnect [--request <URL substring>] [--offline-for 500ms] [--timeout 30s]`"
	case "evidence":
		return "use `heimdal session evidence <name> '<expression returning JSON>'` or include the same step in `heimdal session batch`"
	default:
		return "run `heimdal session --help` for canonical action forms"
	}
}
