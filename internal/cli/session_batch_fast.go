package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const sessionBatchFastSnapshotChars = defaultSnapshotBudgetBytes

type sessionBatchFastStep struct {
	Index        int
	LogicalArgs  []string
	Code         string
	Locator      string
	Target       string
	EvidenceName string
	EvidenceCode string
	Observe      bool
	Full         bool
	RefreshRefs  bool
}

type sessionBatchFastPayload struct {
	Version  int                           `json:"version"`
	Baseline string                        `json:"baseline"`
	Steps    []sessionBatchFastStepPayload `json:"steps"`
}

type sessionBatchFastStepPayload struct {
	Index      int             `json:"index"`
	Status     string          `json:"status"`
	Snapshot   string          `json:"snapshot"`
	Evidence   json.RawMessage `json:"evidence,omitempty"`
	Error      string          `json:"error,omitempty"`
	StartedAt  string          `json:"started_at,omitempty"`
	FinishedAt string          `json:"finished_at,omitempty"`
}

func planSessionBatchFast(document sessionBatchDocument, state SessionState, options sessionBatchOptions) ([]sessionBatchFastStep, bool) {
	// These modes change the shape or completeness of per-action output in ways
	// that a bounded in-process aria snapshot cannot reproduce exactly.
	if options.Boxes || options.Full || options.Verbose {
		return nil, false
	}
	var retainedSnapshot string
	if state.LastSnapshot != "" {
		if contents, err := os.ReadFile(state.LastSnapshot); err == nil {
			retainedSnapshot = string(contents)
		}
	}

	plan := make([]sessionBatchFastStep, 0, len(document.Steps))
	for index, step := range document.Steps {
		if step.Full {
			return nil, false
		}
		planned, ok := translateSessionBatchFastStep(index+1, step, retainedSnapshot)
		if !ok {
			return nil, false
		}
		plan = append(plan, planned)
	}
	return plan, true
}

func translateSessionBatchFastStep(index int, step sessionBatchStep, retainedSnapshot string) (sessionBatchFastStep, bool) {
	logicalArgs := append([]string{step.Command}, step.Args...)
	planned := sessionBatchFastStep{Index: index, LogicalArgs: logicalArgs, Observe: shouldObserveAfterSessionAction(step.Command), Full: step.Full, RefreshRefs: snapshotRefreshesReferences(step.Command)}
	semanticLocator := func(target string) (string, bool) {
		if target == "" || !strings.HasPrefix(target, "e") || retainedSnapshot == "" {
			return "", false
		}
		locator := locatorFromSessionSnapshot(retainedSnapshot, target)
		return locator, locator != ""
	}

	switch step.Command {
	case "click":
		if len(step.Args) < 1 || len(step.Args) > 2 {
			return sessionBatchFastStep{}, false
		}
		locator, ok := semanticLocator(step.Args[0])
		if !ok {
			return sessionBatchFastStep{}, false
		}
		planned.Locator, planned.Target = locator, step.Args[0]
		if len(step.Args) == 1 || step.Args[1] == "left" {
			planned.Code = "await " + locator + ".click();"
		} else if step.Args[1] == "right" || step.Args[1] == "middle" {
			planned.Code = "await " + locator + ".click({ button: " + jsonString(step.Args[1]) + " });"
		} else if step.Args[1] == "--force" {
			planned.Code = "await " + locator + ".click({ force: true });"
		} else {
			return sessionBatchFastStep{}, false
		}
	case "fill":
		if len(step.Args) != 2 && (len(step.Args) != 3 || step.Args[2] != "--submit") {
			return sessionBatchFastStep{}, false
		}
		locator, ok := semanticLocator(step.Args[0])
		if !ok {
			return sessionBatchFastStep{}, false
		}
		planned.Locator, planned.Target = locator, step.Args[0]
		planned.Code = "await " + locator + ".fill(" + jsonString(step.Args[1]) + ");"
		if len(step.Args) == 3 {
			planned.Code += " await " + locator + ".press(\"Enter\");"
		}
	case "press", "type":
		if len(step.Args) == 1 {
			method := "press"
			if step.Command == "type" {
				method = "type"
			}
			planned.Code = "await page.keyboard." + method + "(" + jsonString(step.Args[0]) + ");"
			break
		}
		if len(step.Args) != 2 {
			return sessionBatchFastStep{}, false
		}
		locator, ok := semanticLocator(step.Args[0])
		if !ok {
			return sessionBatchFastStep{}, false
		}
		planned.Locator, planned.Target = locator, step.Args[0]
		method := "press"
		if step.Command == "type" {
			method = "pressSequentially"
		}
		planned.Code = "await " + locator + "." + method + "(" + jsonString(step.Args[1]) + ");"
	case "check", "uncheck", "hover":
		if len(step.Args) != 1 {
			return sessionBatchFastStep{}, false
		}
		locator, ok := semanticLocator(step.Args[0])
		if !ok {
			return sessionBatchFastStep{}, false
		}
		planned.Locator, planned.Target = locator, step.Args[0]
		planned.Code = "await " + locator + "." + step.Command + "();"
	case "goto":
		if len(step.Args) != 1 || step.Args[0] == "" {
			return sessionBatchFastStep{}, false
		}
		planned.Code = "await page.goto(" + jsonString(step.Args[0]) + ");"
	case "reload":
		if len(step.Args) != 0 {
			return sessionBatchFastStep{}, false
		}
		planned.Code = "await page.reload();"
	case "go-back":
		if len(step.Args) != 0 {
			return sessionBatchFastStep{}, false
		}
		planned.Code = "await page.goBack();"
	case "go-forward":
		if len(step.Args) != 0 {
			return sessionBatchFastStep{}, false
		}
		planned.Code = "await page.goForward();"
	case "mouse":
		if len(step.Args) != 3 || (step.Args[0] != "click" && step.Args[0] != "move") {
			return sessionBatchFastStep{}, false
		}
		x, errX := strconv.ParseFloat(step.Args[1], 64)
		y, errY := strconv.ParseFloat(step.Args[2], 64)
		if errX != nil || errY != nil || math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) {
			return sessionBatchFastStep{}, false
		}
		planned.Code = fmt.Sprintf("await page.mouse.%s(%s, %s);", step.Args[0], step.Args[1], step.Args[2])
	case "wait":
		if !safeSessionBatchWaitArgs(step.Args) {
			return sessionBatchFastStep{}, false
		}
		waitOptions, err := parseSessionWaitOptions(step.Args)
		if err != nil {
			return sessionBatchFastStep{}, false
		}
		planned.LogicalArgs = waitLogicalArgs(waitOptions)
		planned.Code = "await (" + waitPlaywrightCode(waitOptions) + ")(page);"
	case "expect":
		expectOptions, err := parseSessionExpectOptions(step.Args)
		if err != nil {
			return sessionBatchFastStep{}, false
		}
		locator := ""
		switch {
		case expectOptions.Role != "":
			locator = "page.getByRole(" + jsonString(expectOptions.Role)
			if expectOptions.Name != "" {
				locator += ", { name: " + jsonString(expectOptions.Name) + ", exact: true }"
			}
			locator += ").first()"
		case expectOptions.Text != "":
			locator = "page.getByText(" + jsonString(expectOptions.Text) + ", { exact: true }).first()"
		case expectOptions.Target != "":
			var ok bool
			locator, ok = semanticLocator(expectOptions.Target)
			if !ok {
				return sessionBatchFastStep{}, false
			}
			planned.Target = expectOptions.Target
		}
		planned.LogicalArgs = expectLogicalArgs(expectOptions)
		planned.Locator = locator
		planned.Code = "await (" + expectPlaywrightCode(expectOptions, locator) + ")(page);"
	case "evidence":
		if len(step.Args) != 2 {
			return sessionBatchFastStep{}, false
		}
		planned.EvidenceName = step.Args[0]
		planned.EvidenceCode = step.Args[1]
	default:
		return sessionBatchFastStep{}, false
	}
	return planned, true
}

func safeSessionBatchWaitArgs(args []string) bool {
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--change":
			// The standalone wait preflights against the retained snapshot so it
			// cannot miss a change between agent calls. Preserve that guarantee by
			// using the sequential fallback until atomic batches can pass their
			// preceding snapshot into the wait itself.
			return false
		case "--role", "--name", "--accessible-name", "--text", "--state", "--timeout", "--timeout-ms", "--settle":
			if index+1 >= len(args) {
				return false
			}
			index++
		default:
			return false
		}
	}
	return true
}

func sessionBatchFastCode(plan []sessionBatchFastStep) string {
	var code strings.Builder
	code.WriteString(`async page => {
  const results = [];
  const errorText = error => String(error && error.message ? error.message : error).slice(0, 2000);
  const captureSnapshot = async () => {
    const value = await page.locator('body').ariaSnapshot({ timeout: 5000 });
    if (value.length <= `)
	code.WriteString(strconv.Itoa(sessionBatchFastSnapshotChars))
	code.WriteString(`) return value;
    return value.slice(0, `)
	code.WriteString(strconv.Itoa(sessionBatchFastSnapshotChars))
	code.WriteString(`) + '\n… semantic snapshot truncated by Heimdal batch fast path';
  };
`)
	code.WriteString("  const baseline = await captureSnapshot();\n")
	for _, step := range plan {
		snapshotCode := "const snapshot = '';"
		if step.Observe {
			snapshotCode = "const snapshot = await captureSnapshot();"
		}
		evidenceCode := ""
		evidenceField := ""
		if step.EvidenceName != "" {
			evidenceCode = "const evidence = await page.evaluate(" + step.EvidenceCode + ");"
			evidenceField = ", evidence"
		}
		fmt.Fprintf(&code, `  {
    const started_at = new Date().toISOString();
    try {
      %s
      %s
      %s
      results.push({ index: %d, status: 'passed', snapshot%s, started_at, finished_at: new Date().toISOString() });
    } catch (error) {
      let snapshot = '';
      let detail = errorText(error);
      try { snapshot = await captureSnapshot(); } catch (snapshotError) { detail += '; post-step snapshot failed: ' + errorText(snapshotError); }
      results.push({ index: %d, status: 'failed', snapshot, error: detail, started_at, finished_at: new Date().toISOString() });
      return { version: 1, baseline, steps: results };
    }
  }
`, step.Code, evidenceCode, snapshotCode, step.Index, evidenceField, step.Index)
	}
	code.WriteString("  return { version: 1, baseline, steps: results };\n}")
	return code.String()
}

func executeSessionBatchFast(ctx context.Context, project Project, state *SessionState, statePath string, plan []sessionBatchFastStep, options sessionBatchOptions, response sessionBatchResponse) sessionBatchResponse {
	response.Invocations = 1
	var retainedSnapshot string
	if state.LastSnapshot != "" {
		if contents, err := os.ReadFile(state.LastSnapshot); err == nil {
			retainedSnapshot = string(contents)
		}
	}
	started := time.Now().UTC()
	command := agentSessionCommand(project, *state, []string{"run-code", sessionBatchFastCode(plan)}, true)
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = project.Root
	cmd.Env = sessionEnvironment(project, *state)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	commandErr := cmd.Run()
	finished := time.Now().UTC()

	prefix := filepath.Join(state.SessionDir, fmt.Sprintf("batch-%04d.run-code", state.ActionCount+1))
	if err := os.WriteFile(prefix+".stdout.log", stdout.Bytes(), 0o644); err != nil && commandErr == nil {
		commandErr = fmt.Errorf("write batch run-code stdout: %w", err)
	}
	if err := os.WriteFile(prefix+".stderr.log", stderr.Bytes(), 0o644); err != nil && commandErr == nil {
		commandErr = fmt.Errorf("write batch run-code stderr: %w", err)
	}

	payload, parseErr := parseSessionBatchFastOutput(stdout.String(), len(plan))
	if commandErr != nil {
		response.Status = "failed"
		response.Error = fmt.Sprintf("Playwright CLI batch run-code failed with exit %d", processExitCode(commandErr))
		if detail := compactCLIOutput(joinOutputs(stdout.String(), stderr.String())); detail != "" {
			response.Error = truncateDisplay(detail, 800)
		}
	} else if parseErr != nil {
		response.Status = "failed"
		response.Error = parseErr.Error()
	} else {
		previousSnapshot := payload.Baseline
		for payloadIndex, result := range payload.Steps {
			planned := plan[payloadIndex]
			if planned.EvidenceName != "" && result.Status == "passed" && len(result.Evidence) == 0 {
				result.Status = "failed"
				result.Error = "Playwright evidence expression returned undefined; return a JSON value"
			}
			stepResult, err := recordSessionBatchFastStep(state, statePath, planned, result, previousSnapshot, started, finished)
			response.Steps = append(response.Steps, stepResult)
			if len(stepResult.Evidence) > 0 {
				if response.Evidence == nil {
					response.Evidence = make(map[string]json.RawMessage)
				}
				response.Evidence[stepResult.EvidenceName] = stepResult.Evidence
			}
			if err != nil {
				response.Status = "failed"
				response.Error = err.Error()
				break
			}
			if result.Status == "failed" {
				response.Status = "failed"
				response.Error = result.Error
				break
			}
			if result.Snapshot != "" {
				previousSnapshot = result.Snapshot
			}
		}
	}

	view, refreshErr := refreshSessionBatchSnapshot(ctx, project, state, statePath, options, retainedSnapshot)
	response.Invocations++
	if refreshErr != nil {
		response.Status = "failed"
		if response.Error == "" {
			response.Error = refreshErr.Error()
		} else {
			response.Error += "; " + refreshErr.Error()
		}
	} else {
		response.Snapshot = view.Text
		response.SnapshotMode = view.Mode
	}
	return response
}

func parseSessionBatchFastOutput(output string, planned int) (sessionBatchFastPayload, error) {
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
	var payload sessionBatchFastPayload
	if err := json.Unmarshal([]byte(clean), &payload); err != nil {
		var encoded string
		if stringErr := json.Unmarshal([]byte(clean), &encoded); stringErr != nil || json.Unmarshal([]byte(encoded), &payload) != nil {
			return sessionBatchFastPayload{}, fmt.Errorf("parse Playwright batch result: %w", err)
		}
	}
	if payload.Version != 1 {
		return sessionBatchFastPayload{}, fmt.Errorf("parse Playwright batch result: unsupported version %d", payload.Version)
	}
	if len(payload.Steps) == 0 || len(payload.Steps) > planned {
		return sessionBatchFastPayload{}, fmt.Errorf("parse Playwright batch result: got %d results for %d planned steps", len(payload.Steps), planned)
	}
	if len(payload.Baseline) > sessionBatchFastSnapshotChars+256 {
		return sessionBatchFastPayload{}, errors.New("parse Playwright batch result: baseline snapshot exceeds the evidence bound")
	}
	failed := false
	for index, step := range payload.Steps {
		if step.Index != index+1 {
			return sessionBatchFastPayload{}, fmt.Errorf("parse Playwright batch result: step index %d is out of order", step.Index)
		}
		if step.Status != "passed" && step.Status != "failed" {
			return sessionBatchFastPayload{}, fmt.Errorf("parse Playwright batch result: invalid status %q at step %d", step.Status, step.Index)
		}
		if failed || (step.Status == "failed" && index != len(payload.Steps)-1) {
			return sessionBatchFastPayload{}, errors.New("parse Playwright batch result: execution continued after a failed step")
		}
		if step.Status == "failed" {
			failed = true
			if step.Error == "" {
				step.Error = "Playwright batch step failed"
				payload.Steps[index] = step
			}
		}
		if len(step.Snapshot) > sessionBatchFastSnapshotChars+256 {
			return sessionBatchFastPayload{}, fmt.Errorf("parse Playwright batch result: step %d snapshot exceeds the evidence bound", step.Index)
		}
		if len(step.Evidence) > coordinationMaxMetadataBytes {
			return sessionBatchFastPayload{}, fmt.Errorf("parse Playwright batch result: step %d evidence exceeds %d bytes", step.Index, coordinationMaxMetadataBytes)
		}
	}
	if !failed && len(payload.Steps) != planned {
		return sessionBatchFastPayload{}, fmt.Errorf("parse Playwright batch result: stopped after %d of %d steps without a failure", len(payload.Steps), planned)
	}
	return payload, nil
}

func recordSessionBatchFastStep(state *SessionState, statePath string, planned sessionBatchFastStep, payload sessionBatchFastStepPayload, previousSnapshot string, batchStarted, batchFinished time.Time) (sessionBatchStepResult, error) {
	state.ActionCount++
	sequence := state.ActionCount
	started := parseSessionBatchFastTime(payload.StartedAt, batchStarted)
	finished := parseSessionBatchFastTime(payload.FinishedAt, batchFinished)
	if finished.Before(started) {
		finished = started
	}
	exitCode := 0
	if payload.Status == "failed" {
		exitCode = 1
	}
	stdoutPath := filepath.Join(state.SessionDir, fmt.Sprintf("action-%04d.stdout.log", sequence))
	stderrPath := filepath.Join(state.SessionDir, fmt.Sprintf("action-%04d.stderr.log", sequence))
	stdout := "batched Playwright action " + payload.Status + "\n"
	if planned.EvidenceName != "" && len(payload.Evidence) > 0 {
		stdout += "HEIMDAL_EVIDENCE " + planned.EvidenceName + " " + string(payload.Evidence) + "\n"
	}
	stderr := ""
	if payload.Error != "" {
		stderr = payload.Error + "\n"
	}
	if err := os.WriteFile(stdoutPath, []byte(stdout), 0o644); err != nil {
		return sessionBatchStepResult{}, fmt.Errorf("write session action stdout: %w", err)
	}
	if err := os.WriteFile(stderrPath, []byte(stderr), 0o644); err != nil {
		return sessionBatchStepResult{}, fmt.Errorf("write session action stderr: %w", err)
	}
	record := SessionActionRecord{
		Sequence: sequence, StartedAt: started, FinishedAt: finished,
		Args: append([]string(nil), planned.LogicalArgs...), Locator: planned.Locator,
		Stdout: strings.TrimSpace(stdout), Stderr: strings.TrimSpace(stderr),
		StdoutFile: stdoutPath, StderrFile: stderrPath, ExitCode: exitCode,
	}
	if err := appendSessionAction(state.ActionLog, record); err != nil {
		return sessionBatchStepResult{}, err
	}

	var view snapshotPresentation
	var err error
	if payload.Snapshot != "" {
		stored, storeErr := storeSessionSnapshot(state, statePath, sequence, payload.Snapshot, false, planned.Full, planned.Target, planned.RefreshRefs)
		view, err = stored, storeErr
		if storeErr == nil && previousSnapshot != "" && !planned.Full {
			view = semanticSnapshotDelta(previousSnapshot, payload.Snapshot, planned.Target, planned.RefreshRefs)
			view.Artifact = stored.Artifact
		}
	} else {
		err = writeSessionState(statePath, *state)
	}
	stepResult := sessionBatchStepResult{
		Index: planned.Index, Command: compactSessionBatchArgs(planned.LogicalArgs), Status: payload.Status,
		Action: sequence, Output: compactSessionBatchAction(planned.LogicalArgs, planned.Locator),
		EvidenceName: planned.EvidenceName, Evidence: payload.Evidence,
		Snapshot: view.Text, SnapshotMode: view.Mode, SnapshotOmitted: view.Omitted,
		Error: payload.Error,
	}
	return stepResult, err
}

func compactSessionBatchArgs(args []string) []string {
	compact := compactSessionArgs(args)
	if len(args) > 2 && args[0] == "type" {
		compact[1] = args[1]
		compact[2] = fmt.Sprintf("<text:%d chars>", len(args[2]))
	}
	if len(args) > 2 && args[0] == "evidence" {
		compact[1] = args[1]
		compact[2] = fmt.Sprintf("<expression:%d chars>", len(args[2]))
	}
	return compact
}

func compactSessionBatchAction(args []string, locator string) string {
	if len(args) > 1 && args[0] == "evidence" {
		return "captured named evidence " + args[1]
	}
	if len(args) > 0 && args[0] == "expect" {
		return "expectation passed"
	}
	output := commandString(compactSessionBatchArgs(args))
	if locator != "" {
		output += "\nlocator: " + locator
	}
	return output
}

func parseSessionBatchFastTime(value string, fallback time.Time) time.Time {
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed
	}
	return fallback
}

func refreshSessionBatchSnapshot(ctx context.Context, project Project, state *SessionState, statePath string, options sessionBatchOptions, retainedSnapshot string) (snapshotPresentation, error) {
	args := sessionSnapshotArgs(false, false, nil)
	result, err := runSessionCommandMode(ctx, project, state, statePath, args, "", true)
	if err != nil {
		return snapshotPresentation{}, fmt.Errorf("final snapshot refresh failed: %w", err)
	}
	snapshot, ok := sessionSnapshotPayload(project, *state, result.Stdout)
	if !ok {
		return snapshotPresentation{}, errors.New("final snapshot refresh failed: Playwright did not return a semantic snapshot")
	}
	view, err := storeSessionSnapshot(state, statePath, result.Sequence, snapshot, false, false, "", true)
	if err != nil {
		return snapshotPresentation{}, fmt.Errorf("final snapshot refresh failed: %w", err)
	}
	if retainedSnapshot != "" {
		compact := semanticSnapshotDelta(retainedSnapshot, snapshot, "", true)
		compact.Artifact = view.Artifact
		view = compact
	}
	return view, nil
}
