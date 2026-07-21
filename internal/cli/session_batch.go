package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxSessionBatchBytes = 64 * 1024
	maxSessionBatchSteps = 50
)

type sessionBatchDocument struct {
	Version int                `json:"version"`
	Steps   []sessionBatchStep `json:"steps"`
}

type sessionBatchStep struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Full    bool     `json:"full,omitempty"`
}

type sessionBatchStepResult struct {
	Index           int             `json:"index"`
	Command         []string        `json:"command"`
	Status          string          `json:"status"`
	Action          int             `json:"action,omitempty"`
	Output          string          `json:"output,omitempty"`
	EvidenceName    string          `json:"evidence_name,omitempty"`
	Evidence        json.RawMessage `json:"evidence,omitempty"`
	Snapshot        string          `json:"snapshot,omitempty"`
	SnapshotMode    string          `json:"snapshot_mode,omitempty"`
	SnapshotOmitted int             `json:"snapshot_omitted,omitempty"`
	Error           string          `json:"error,omitempty"`
	Issues          []string        `json:"issues,omitempty"`
}

type sessionBatchResponse struct {
	SchemaVersion  int                        `json:"schema_version"`
	Status         string                     `json:"status"`
	Session        string                     `json:"session"`
	Execution      string                     `json:"execution"`
	Invocations    int                        `json:"playwright_invocations"`
	Planned        int                        `json:"planned_steps"`
	Steps          []sessionBatchStepResult   `json:"steps"`
	Evidence       map[string]json.RawMessage `json:"evidence,omitempty"`
	Snapshot       string                     `json:"snapshot,omitempty"`
	SnapshotMode   string                     `json:"snapshot_mode,omitempty"`
	Error          string                     `json:"error,omitempty"`
	Artifact       string                     `json:"artifact,omitempty"`
	SessionDetails *SessionState              `json:"session_details,omitempty"`
}

func runSessionBatch(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseSessionBatchOptions(args)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	document, contents, err := readSessionBatch(options.File, os.Stdin)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	project, state, statePath, err := discoverSession(options.SessionOptions)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if state.StoppedAt != nil {
		return reportError(options.JSON, fmt.Errorf("session %q is stopped", state.Name), out, errOut)
	}
	artifact := filepath.Join(state.SessionDir, fmt.Sprintf("batch-%04d.json", state.ActionCount+1))
	if err := os.WriteFile(artifact, contents, 0o600); err != nil {
		return reportError(options.JSON, fmt.Errorf("write batch artifact: %w", err), out, errOut)
	}

	response := sessionBatchResponse{SchemaVersion: 1, Status: "passed", Session: state.Name, Planned: len(document.Steps), Artifact: artifact}
	if plan, ok := planSessionBatchFast(document, state, options); ok {
		response.Execution = "atomic"
		response = executeSessionBatchFast(ctx, project, &state, statePath, plan, options, response)
		if options.FullJSON {
			response.SessionDetails = &state
		}
		return printSessionBatchResponse(out, errOut, response, options.JSON)
	}
	response.Execution = "sequential"
	initialActionCount := state.ActionCount
	for index, step := range document.Steps {
		if step.Command == "evidence" {
			stepResult := executeSessionBatchEvidence(ctx, project, &state, statePath, index+1, step)
			response.Steps = append(response.Steps, stepResult)
			if len(stepResult.Evidence) > 0 {
				if response.Evidence == nil {
					response.Evidence = make(map[string]json.RawMessage)
				}
				response.Evidence[stepResult.EvidenceName] = stepResult.Evidence
			}
			if stepResult.Status == "failed" {
				response.Status = "failed"
				response.Error = stepResult.Error
				break
			}
			continue
		}
		stepOptions := options.SessionOptions
		stepOptions.Forwarded = append([]string(nil), step.Args...)
		stepOptions.Full = stepOptions.Full || step.Full
		result := executeSessionAction(ctx, project, &state, statePath, step.Command, stepOptions)
		stepResult := sessionBatchStepResult{
			Index:           index + 1,
			Command:         compactSessionBatchArgs(append([]string{step.Command}, step.Args...)),
			Status:          result.Status,
			Action:          result.Action,
			Output:          result.Output,
			Snapshot:        result.Snapshot,
			SnapshotMode:    result.SnapshotMode,
			SnapshotOmitted: result.SnapshotOmitted,
			Error:           result.Error,
			Issues:          result.Issues,
		}
		response.Steps = append(response.Steps, stepResult)
		if result.Snapshot != "" {
			response.Snapshot = result.Snapshot
			response.SnapshotMode = result.SnapshotMode
		}
		if result.Status == "failed" || result.Status == "issues" {
			response.Status = result.Status
			response.Error = result.Error
			break
		}
	}
	response.Invocations = state.ActionCount - initialActionCount
	if options.FullJSON {
		response.SessionDetails = &state
	}
	return printSessionBatchResponse(out, errOut, response, options.JSON)
}

func parseSessionBatchOptions(args []string) (sessionBatchOptions, error) {
	var common []string
	options := sessionBatchOptions{}
	for index := 0; index < len(args); index++ {
		if args[index] != "--file" {
			common = append(common, args[index])
			continue
		}
		value, next, err := nextValue(args, index, "--file")
		if err != nil {
			return options, err
		}
		if options.File != "" {
			return options, errors.New("--file may only be specified once")
		}
		options.File = value
		index = next
	}
	parsed, err := parseSessionOptions(common)
	options.SessionOptions = parsed
	if err != nil {
		return options, err
	}
	if len(parsed.Forwarded) > 0 {
		return options, fmt.Errorf("session batch does not accept Playwright arguments outside the batch file: %s", strings.Join(parsed.Forwarded, " "))
	}
	if options.File == "" {
		return options, errors.New("session batch requires --file FILE|-")
	}
	return options, nil
}

func readSessionBatch(path string, stdin io.Reader) (sessionBatchDocument, []byte, error) {
	var reader io.Reader
	var file *os.File
	if path == "-" {
		reader = stdin
	} else {
		opened, err := os.Open(path)
		if err != nil {
			return sessionBatchDocument{}, nil, fmt.Errorf("open session batch: %w", err)
		}
		file = opened
		defer file.Close()
		reader = file
	}
	contents, err := io.ReadAll(io.LimitReader(reader, maxSessionBatchBytes+1))
	if err != nil {
		return sessionBatchDocument{}, nil, fmt.Errorf("read session batch: %w", err)
	}
	if len(contents) > maxSessionBatchBytes {
		return sessionBatchDocument{}, nil, fmt.Errorf("session batch exceeds %d bytes", maxSessionBatchBytes)
	}
	var document sessionBatchDocument
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return sessionBatchDocument{}, nil, fmt.Errorf("parse session batch JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return sessionBatchDocument{}, nil, errors.New("parse session batch JSON: expected one JSON document")
	}
	if document.Version == 0 {
		document.Version = 1
	}
	if document.Version != 1 {
		return sessionBatchDocument{}, nil, fmt.Errorf("unsupported session batch version %d", document.Version)
	}
	if len(document.Steps) == 0 || len(document.Steps) > maxSessionBatchSteps {
		return sessionBatchDocument{}, nil, fmt.Errorf("session batch must contain 1 to %d steps", maxSessionBatchSteps)
	}
	evidenceNames := make(map[string]struct{})
	for index, step := range document.Steps {
		if err := validateSessionBatchStep(step); err != nil {
			return sessionBatchDocument{}, nil, fmt.Errorf("session batch step %d: %w", index+1, err)
		}
		if step.Command == "evidence" {
			if _, exists := evidenceNames[step.Args[0]]; exists {
				return sessionBatchDocument{}, nil, fmt.Errorf("session batch step %d: duplicate evidence name %q", index+1, step.Args[0])
			}
			evidenceNames[step.Args[0]] = struct{}{}
		}
	}
	canonical, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return sessionBatchDocument{}, nil, err
	}
	return document, append(canonical, '\n'), nil
}

func validateSessionBatchStep(step sessionBatchStep) error {
	if strings.TrimSpace(step.Command) == "" || sanitize(step.Command) != strings.ToLower(step.Command) {
		return errors.New("command must contain lowercase letters, numbers, or dashes")
	}
	switch step.Command {
	case "start", "stop", "status", "save", "diagnose", "batch", "timeline", "report", "checkpoint", "measure", "help":
		return fmt.Errorf("command %q is not valid inside a running-session batch", step.Command)
	case "evidence":
		if len(step.Args) != 2 {
			return errors.New("evidence requires NAME and one Playwright evaluation expression")
		}
		if err := validateCoordinationSelector("evidence", step.Args[0]); err != nil {
			return err
		}
		if strings.TrimSpace(step.Args[1]) == "" {
			return errors.New("evidence expression must not be empty")
		}
	}
	return nil
}

func executeSessionBatchEvidence(ctx context.Context, project Project, state *SessionState, statePath string, index int, step sessionBatchStep) sessionBatchStepResult {
	name, expression := step.Args[0], step.Args[1]
	logicalArgs := []string{"evidence", name, expression}
	runtimeCode := "async page => await page.evaluate(" + expression + ")"
	result, commandErr := runSessionCommandModeArgs(ctx, project, state, statePath, logicalArgs, []string{"run-code", runtimeCode}, "", true)
	stepResult := sessionBatchStepResult{Index: index, Command: compactSessionBatchArgs(logicalArgs), Status: "passed", Action: result.Sequence, EvidenceName: name}
	if commandErr != nil {
		stepResult.Status = "failed"
		stepResult.Error = commandErr.Error()
		if detail := compactCLIOutput(joinOutputs(result.Stdout, result.Stderr)); detail != "" {
			stepResult.Error = truncateDisplay(detail, 800)
		}
		return stepResult
	}
	payload, err := parseSessionBatchEvidence(result.Stdout)
	if err != nil {
		stepResult.Status = "failed"
		stepResult.Error = err.Error()
		return stepResult
	}
	stepResult.Output = "captured named evidence " + name
	stepResult.Evidence = payload
	return stepResult
}

func parseSessionBatchEvidence(output string) (json.RawMessage, error) {
	clean := strings.TrimSpace(stripANSI(output))
	if start := strings.Index(clean, "### Result"); start >= 0 {
		clean = strings.TrimSpace(clean[start+len("### Result"):])
	}
	if end := strings.Index(clean, "\n### "); end >= 0 {
		clean = strings.TrimSpace(clean[:end])
	}
	clean = strings.TrimSpace(strings.Trim(clean, "`"))
	if strings.HasPrefix(clean, "json\n") || strings.HasPrefix(clean, "js\n") {
		clean = strings.TrimSpace(clean[3:])
	}
	var encoded string
	if json.Unmarshal([]byte(clean), &encoded) == nil && json.Valid([]byte(encoded)) {
		clean = encoded
	}
	if clean == "" || !json.Valid([]byte(clean)) {
		return nil, errors.New("Playwright evidence expression did not return JSON")
	}
	if len(clean) > coordinationMaxMetadataBytes {
		return nil, fmt.Errorf("Playwright evidence exceeds %d bytes", coordinationMaxMetadataBytes)
	}
	return json.RawMessage(append([]byte(nil), clean...)), nil
}

func printSessionBatchResponse(out, errOut io.Writer, response sessionBatchResponse, asJSON bool) int {
	if asJSON {
		if err := writeJSONTo(out, response); err != nil {
			fmt.Fprintln(errOut, "heimdal:", err)
			return 1
		}
	} else {
		for _, step := range response.Steps {
			fmt.Fprintf(out, "%d. %s: %s", step.Index, commandString(step.Command), step.Status)
			if step.Action > 0 {
				fmt.Fprintf(out, " (action %d)", step.Action)
			}
			fmt.Fprintln(out)
			if step.Output != "" {
				fmt.Fprintf(out, "   %s\n", strings.ReplaceAll(step.Output, "\n", "\n   "))
			}
			if step.Snapshot != "" {
				label := "snapshot"
				if step.SnapshotMode == "delta" {
					label = "snapshot delta"
				}
				fmt.Fprintf(out, "   %s:\n   %s\n", label, strings.ReplaceAll(step.Snapshot, "\n", "\n   "))
			}
			for _, issue := range step.Issues {
				fmt.Fprintf(errOut, "heimdal: batch step %d issue: %s\n", step.Index, issue)
			}
		}
		if response.Snapshot != "" {
			label := "Snapshot"
			if response.SnapshotMode == "delta" {
				label = "Snapshot delta"
			}
			fmt.Fprintf(out, "%s:\n%s\n", label, response.Snapshot)
		}
		fmt.Fprintf(out, "Heimdal session %s batch: %s (%d/%d steps, %s, %d Playwright invocations)\n", response.Session, response.Status, len(response.Steps), response.Planned, response.Execution, response.Invocations)
	}
	if response.Error != "" {
		fmt.Fprintln(errOut, "heimdal:", response.Error)
	}
	if response.Status == "failed" || response.Status == "issues" {
		return 1
	}
	return 0
}
