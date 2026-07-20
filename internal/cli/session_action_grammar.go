package cli

import (
	"context"
	"errors"
	"fmt"
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
		if len(args) != 3 || args[0] != "click" {
			return nil, "", sessionActionCorrection(action), errors.New("mouse accepts click X Y")
		}
		if _, err := strconv.ParseFloat(args[1], 64); err != nil {
			return nil, "", sessionActionCorrection(action), fmt.Errorf("mouse X coordinate must be numeric (got %q)", args[1])
		}
		if _, err := strconv.ParseFloat(args[2], 64); err != nil {
			return nil, "", sessionActionCorrection(action), fmt.Errorf("mouse Y coordinate must be numeric (got %q)", args[2])
		}
		code := fmt.Sprintf("async page => { await page.mouse.click(%s, %s); }", args[1], args[2])
		return []string{"run-code", code}, "", "", nil
	default:
		return logicalArgs, "", "", nil
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
		if strings.HasPrefix(line, "Unknown command") || strings.HasPrefix(line, "Error:") {
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
		return "use `heimdal session click <target> [left|right|middle|--force]`"
	case "mouse":
		return "use `heimdal session mouse click <x> <y>`"
	case "wait":
		return "use `heimdal session wait --role <role> [--name <name>]`, `--text <text>`, or `--change`"
	case "expect":
		return "use `heimdal session expect --role <role> [--name <name>]`, `--text <text>`, `--url <url>`, or `--target <ref> --value <value>`"
	default:
		return "run `heimdal session --help` for canonical action forms"
	}
}
