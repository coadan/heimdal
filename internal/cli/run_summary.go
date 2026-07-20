package cli

import (
	"bufio"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type TestCounts struct {
	Total      int `json:"total"`
	Executed   int `json:"executed"`
	Passed     int `json:"passed"`
	Failed     int `json:"failed"`
	Skipped    int `json:"skipped"`
	Flaky      int `json:"flaky"`
	DidNotRun  int `json:"did_not_run"`
	Expected   int `json:"expected"`
	Unexpected int `json:"unexpected"`
}

type PrimaryFailure struct {
	Test        string `json:"test,omitempty"`
	Location    string `json:"location,omitempty"`
	Message     string `json:"message"`
	Step        string `json:"step,omitempty"`
	Fingerprint string `json:"fingerprint"`
}

type RunWarning struct {
	Source  string `json:"source"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

type RunInvocation struct {
	TestFiles []string `json:"test_files,omitempty"`
	Grep      string   `json:"grep,omitempty"`
	Project   string   `json:"project,omitempty"`
	Retries   string   `json:"retries,omitempty"`
}

type RunEnvironmentVariable struct {
	Name   string `json:"name"`
	Set    bool   `json:"set"`
	Source string `json:"source"`
}

type runAnalysis struct {
	Tests          *TestCounts
	PrimaryFailure *PrimaryFailure
	Warnings       []RunWarning
}

func compactRunReport(report any) any {
	switch value := report.(type) {
	case RunResult:
		value.StdoutTail = ""
		value.StderrTail = ""
		value.Artifacts.Files = nil
		return value
	case RunManifest:
		value.Artifacts.Files = nil
		return value
	default:
		return report
	}
}

func failureContextExcerpt(files []string) string {
	for _, path := range files {
		if filepath.Base(path) != "error-context.md" {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 16*1024), 256*1024)
		var excerpt strings.Builder
		for scanner.Scan() && excerpt.Len() < 4000 {
			line := strings.TrimRight(strings.ToValidUTF8(scanner.Text(), "�"), " \t")
			if excerpt.Len() > 0 {
				if excerpt.Len() == 4000 {
					break
				}
				excerpt.WriteByte('\n')
			}
			remaining := 4000 - excerpt.Len()
			line, truncated := truncateUTF8(line, remaining)
			excerpt.WriteString(line)
			if truncated {
				break
			}
		}
		_ = file.Close()
		return strings.TrimSpace(excerpt.String())
	}
	return ""
}

func truncateUTF8(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	for len(value) > limit {
		_, width := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-width]
	}
	return value, true
}

func failureContextExcerptInDirectory(root string) string {
	var contextPath string
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !entry.IsDir() && filepath.Base(path) == "error-context.md" {
			contextPath = path
			return filepath.SkipAll
		}
		return nil
	})
	if contextPath == "" {
		return ""
	}
	return failureContextExcerpt([]string{contextPath})
}

func addRunTraceDiagnosis(result *RunResult, runDir string) {
	if result.TraceDiagnosis != nil {
		return
	}
	tracePath, err := findTrace(runDir)
	if err != nil {
		return
	}
	diagnosis, err := summarizeTrace(tracePath, result, 2)
	if err != nil {
		result.DiagnosisError = err.Error()
		return
	}
	result.TraceDiagnosis = &diagnosis
	result.NextCommand = "heimdal trace --run " + result.RunID
}

func liveRunProgress(manifest RunManifest, runDir string, now time.Time) RunProgress {
	progress := RunProgress{ElapsedMS: now.Sub(manifest.StartedAt).Milliseconds()}
	stdoutBytes, stdoutLast, stdoutModified := fileProgress(filepath.Join(runDir, "stdout.log"))
	progress.StdoutBytes, progress.LastOutput = stdoutBytes, stdoutLast
	stderrBytes, stderrLast, stderrModified := fileProgress(filepath.Join(runDir, "stderr.log"))
	progress.StderrBytes = stderrBytes
	if stderrLast != "" && (progress.LastOutput == "" || stderrModified.After(stdoutModified)) {
		progress.LastOutput = stderrLast
	}
	return progress
}

func fileProgress(path string) (int64, string, time.Time) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, "", time.Time{}
	}
	file, err := os.Open(path)
	if err != nil {
		return info.Size(), "", info.ModTime()
	}
	defer file.Close()
	start := info.Size() - 8192
	if start < 0 {
		start = 0
	}
	if _, err := file.Seek(start, 0); err != nil {
		return info.Size(), "", info.ModTime()
	}
	scanner := bufio.NewScanner(file)
	last := ""
	for scanner.Scan() {
		if line := strings.TrimSpace(stripANSI(strings.ToValidUTF8(scanner.Text(), "�"))); line != "" {
			last = line
		}
	}
	return info.Size(), truncateTraceValue(last, 500), info.ModTime()
}

func enrichRunResult(result *RunResult) {
	analysis := analyzeRunOutput(result.StdoutTail, result.StderrTail)
	if result.Tests == nil {
		result.Tests = analysis.Tests
	}
	if result.PrimaryFailure == nil {
		result.PrimaryFailure = analysis.PrimaryFailure
	}
	if len(result.Warnings) == 0 {
		result.Warnings = analysis.Warnings
	}
	if result.PrimaryFailure != nil && result.Status == "failed" {
		result.ProcessError = result.Failure
		result.Failure = result.PrimaryFailure.Message
	}
	if result.Status == "passed" && result.Tests != nil && result.Tests.Total > 0 && result.Tests.Executed == 0 {
		result.Status = "skipped"
		result.ExitCode = 3
		result.Failure = fmt.Sprintf("no tests executed (%d skipped)", result.Tests.Skipped)
	}
	if result.Status == "failed" {
		result.NextCommand = "heimdal trace --run " + result.RunID + " --json"
	}
}

var runLocationPattern = regexp.MustCompile(`^(.+\.[cm]?[jt]sx?):([0-9]+):([0-9]+)$`)
var runWarningPIDPattern = regexp.MustCompile(`^\(node:[0-9]+\)\s*`)

func analyzeRunOutput(stdout, stderr string) runAnalysis {
	cleanStdout := stripANSI(stdout)
	cleanStderr := stripANSI(stderr)
	return runAnalysis{
		Tests:          parseTestCounts(cleanStdout + "\n" + cleanStderr),
		PrimaryFailure: parsePrimaryFailure(cleanStdout + "\n" + cleanStderr),
		Warnings:       parseRunWarnings(cleanStdout + "\n" + cleanStderr),
	}
}

func parseTestCounts(output string) *TestCounts {
	counts := TestCounts{}
	found := false
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		count, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		switch fields[1] {
		case "passed":
			counts.Passed = count
			found = true
		case "failed":
			counts.Failed = count
			found = true
		case "skipped":
			counts.Skipped = count
			found = true
		case "flaky":
			counts.Flaky = count
			found = true
		case "did":
			if len(fields) >= 4 && fields[2] == "not" && fields[3] == "run" {
				counts.DidNotRun = count
				found = true
			}
		}
	}
	if !found {
		return nil
	}
	counts.Executed = counts.Passed + counts.Failed + counts.Flaky
	counts.Total = counts.Executed + counts.Skipped + counts.DidNotRun
	counts.Expected = counts.Passed
	counts.Unexpected = counts.Failed
	return &counts
}

func parsePrimaryFailure(output string) *PrimaryFailure {
	lines := strings.Split(output, "\n")
	failure := PrimaryFailure{}
	header := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		close := strings.Index(trimmed, ") ")
		if close < 1 {
			continue
		}
		if _, err := strconv.Atoi(trimmed[:close]); err != nil {
			continue
		}
		segments := strings.Split(trimmed[close+2:], " › ")
		for segmentIndex, segment := range segments {
			if runLocationPattern.MatchString(segment) {
				failure.Location = segment
				if segmentIndex+1 < len(segments) {
					failure.Test = strings.TrimRight(strings.Join(segments[segmentIndex+1:], " › "), " ─")
				}
				header = index
				break
			}
		}
		if header >= 0 {
			break
		}
	}
	start := 0
	if header >= 0 {
		start = header + 1
	}
	for index := start; index < len(lines); index++ {
		line := strings.TrimSpace(lines[index])
		if line == "" {
			continue
		}
		if failure.Message == "" && (strings.HasPrefix(line, "Error:") || strings.HasPrefix(line, "TimeoutError:") || strings.HasPrefix(line, "AssertionError:")) {
			failure.Message = line
			for next := index + 1; next < len(lines) && next <= index+4; next++ {
				detail := strings.TrimSpace(lines[next])
				if strings.HasPrefix(detail, "Expected:") || strings.HasPrefix(detail, "Received:") || strings.HasPrefix(detail, "Locator:") || strings.HasPrefix(detail, "Timeout:") {
					failure.Message += "\n" + detail
				}
			}
		}
		if failure.Step == "" && strings.HasPrefix(line, "at ") {
			step := strings.TrimSpace(strings.TrimPrefix(line, "at "))
			if end := strings.IndexAny(step, " ("); end > 0 {
				failure.Step = step[:end]
			}
		}
		if failure.Message != "" && failure.Step != "" {
			break
		}
	}
	if failure.Message == "" {
		return nil
	}
	fingerprintInput := strings.ToLower(strings.Join([]string{failure.Test, failureLocationFile(failure.Location), normalizeFailureMessage(failure.Message), failure.Step}, "|"))
	digest := sha256.Sum256([]byte(fingerprintInput))
	failure.Fingerprint = fmt.Sprintf("%x", digest[:8])
	return &failure
}

func failureLocationFile(location string) string {
	match := runLocationPattern.FindStringSubmatch(location)
	if len(match) > 1 {
		return match[1]
	}
	return location
}

func normalizeFailureMessage(message string) string {
	fields := strings.Fields(message)
	for index, field := range fields {
		trimmed := strings.Trim(field, `"'(),.:`)
		if _, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSuffix(trimmed, "ms"), "s"), 64); err == nil {
			fields[index] = strings.Replace(field, trimmed, "#", 1)
		}
	}
	return strings.Join(fields, " ")
}

func parseRunWarnings(output string) []RunWarning {
	type warningKey struct{ source, message string }
	counts := make(map[warningKey]int)
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)
		if line == "" || (!strings.Contains(lower, "warning:") && !strings.HasPrefix(lower, "warning ")) {
			continue
		}
		line = runWarningPIDPattern.ReplaceAllString(line, "")
		source := "application"
		if strings.Contains(lower, "node:") || strings.Contains(lower, "no_color") || strings.Contains(lower, "npm warn") {
			source = "runner"
		}
		counts[warningKey{source: source, message: truncateTraceValue(line, 500)}]++
	}
	warnings := make([]RunWarning, 0, len(counts))
	for key, count := range counts {
		warnings = append(warnings, RunWarning{Source: key.source, Message: key.message, Count: count})
	}
	sort.Slice(warnings, func(left, right int) bool {
		if warnings[left].Source == warnings[right].Source {
			return warnings[left].Message < warnings[right].Message
		}
		return warnings[left].Source < warnings[right].Source
	})
	return warnings
}

func parseRunInvocation(args []string) RunInvocation {
	invocation := RunInvocation{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		value := func() string {
			if index+1 < len(args) {
				index++
				return args[index]
			}
			return ""
		}
		switch {
		case arg == "--grep" || arg == "-g":
			invocation.Grep = value()
		case strings.HasPrefix(arg, "--grep="):
			invocation.Grep = strings.TrimPrefix(arg, "--grep=")
		case arg == "--project":
			invocation.Project = value()
		case strings.HasPrefix(arg, "--project="):
			invocation.Project = strings.TrimPrefix(arg, "--project=")
		case arg == "--retries":
			invocation.Retries = value()
		case strings.HasPrefix(arg, "--retries="):
			invocation.Retries = strings.TrimPrefix(arg, "--retries=")
		case !strings.HasPrefix(arg, "-") && isLikelyTestPath(arg):
			invocation.TestFiles = append(invocation.TestFiles, filepath.ToSlash(arg))
		}
	}
	return invocation
}

func isLikelyTestPath(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, ".spec.") || strings.Contains(lower, ".test.")
}

func runEnvironmentProvenance(config PlaywrightConfig, environment []string) []RunEnvironmentVariable {
	set := make(map[string]bool, len(environment))
	for _, entry := range environment {
		name, _, found := strings.Cut(entry, "=")
		if found {
			set[name] = true
		}
	}
	sources := make(map[string]string)
	for name := range config.Env {
		if validEnvironmentName(name) {
			sources[name] = "configured"
		}
	}
	for _, name := range config.ProvenanceEnv {
		if validEnvironmentName(name) {
			if _, exists := sources[name]; !exists {
				sources[name] = "tracked"
			}
		}
	}
	variables := make([]RunEnvironmentVariable, 0, len(sources))
	for name, source := range sources {
		variables = append(variables, RunEnvironmentVariable{Name: name, Set: set[name], Source: source})
	}
	sort.Slice(variables, func(left, right int) bool { return variables[left].Name < variables[right].Name })
	return variables
}

func validEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for index, char := range name {
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || char == '_' || (index > 0 && char >= '0' && char <= '9') {
			continue
		}
		return false
	}
	return true
}

func directoryBytes(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}
