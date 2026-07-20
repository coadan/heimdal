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
	Index   int      `json:"index"`
	Command []string `json:"command"`
	Status  string   `json:"status"`
	Action  int      `json:"action,omitempty"`
	Output  string   `json:"output,omitempty"`
	Error   string   `json:"error,omitempty"`
	Issues  []string `json:"issues,omitempty"`
}

type sessionBatchResponse struct {
	SchemaVersion  int                      `json:"schema_version"`
	Status         string                   `json:"status"`
	Session        string                   `json:"session"`
	Planned        int                      `json:"planned_steps"`
	Steps          []sessionBatchStepResult `json:"steps"`
	Snapshot       string                   `json:"snapshot,omitempty"`
	SnapshotMode   string                   `json:"snapshot_mode,omitempty"`
	Error          string                   `json:"error,omitempty"`
	Artifact       string                   `json:"artifact,omitempty"`
	SessionDetails *SessionState            `json:"session_details,omitempty"`
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
	for index, step := range document.Steps {
		stepOptions := options.SessionOptions
		stepOptions.Forwarded = append([]string(nil), step.Args...)
		stepOptions.Full = stepOptions.Full || step.Full
		result := executeSessionAction(ctx, project, &state, statePath, step.Command, stepOptions)
		stepResult := sessionBatchStepResult{
			Index:   index + 1,
			Command: compactSessionArgs(append([]string{step.Command}, step.Args...)),
			Status:  result.Status,
			Action:  result.Action,
			Output:  result.Output,
			Error:   result.Error,
			Issues:  result.Issues,
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
	for index, step := range document.Steps {
		if err := validateSessionBatchStep(step); err != nil {
			return sessionBatchDocument{}, nil, fmt.Errorf("session batch step %d: %w", index+1, err)
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
	case "start", "stop", "status", "save", "diagnose", "batch", "timeline", "report", "checkpoint", "help":
		return fmt.Errorf("command %q is not valid inside a running-session batch", step.Command)
	}
	return nil
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
		fmt.Fprintf(out, "Heimdal session %s batch: %s (%d/%d steps)\n", response.Session, response.Status, len(response.Steps), response.Planned)
	}
	if response.Error != "" {
		fmt.Fprintln(errOut, "heimdal:", response.Error)
	}
	if response.Status == "failed" || response.Status == "issues" {
		return 1
	}
	return 0
}
