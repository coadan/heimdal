package cli

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	diagnosticConsoleLine = regexp.MustCompile(`^\[(ERROR|WARNING)\]\s+(.+?)(?:\s+@\s+(\S+))?$`)
	diagnosticRequestLine = regexp.MustCompile(`^\d+\.\s+\[([A-Z]+)\]\s+(\S+)\s+=>\s+\[(\d+)\]\s*(.*)$`)
	diagnosticZeroConsole = regexp.MustCompile(`(?m)^Total messages:\s*0\s+\(Errors:\s*0,\s*Warnings:\s*0\)\s*$`)
	diagnosticStaticOnly  = regexp.MustCompile(`(?m)^Note:\s+(\d+) static requests? not shown.*$`)
)

type diagnosticSignature struct {
	Text  string
	Count int
}

func compactSessionDiagnosticOutput(command []string, result sessionCommandResult) string {
	raw := compactCLIOutput(joinOutputs(result.Stdout, result.Stderr))
	if raw == "" {
		return strings.Join(command, " ") + ": none"
	}
	if command[0] == "console" && diagnosticZeroConsole.MatchString(raw) {
		return strings.Join(command, " ") + ": none"
	}
	if command[0] == "requests" {
		if match := diagnosticStaticOnly.FindStringSubmatch(raw); match != nil && strings.TrimSpace(diagnosticStaticOnly.ReplaceAllString(raw, "")) == "" {
			return "requests: none (" + match[1] + " static omitted)"
		}
	}
	var signatures []diagnosticSignature
	switch command[0] {
	case "console":
		signatures = groupDiagnosticLines(raw, diagnosticConsoleLine, func(match []string) string {
			location := normalizeDiagnosticLocation(match[3])
			text := "[" + match[1] + "] " + strings.TrimSpace(match[2])
			if location != "" {
				text += " @ " + location
			}
			return text
		})
	case "requests":
		signatures = groupDiagnosticLines(raw, diagnosticRequestLine, func(match []string) string {
			text := match[1] + " " + normalizeDiagnosticLocation(match[2]) + " => " + match[3]
			if detail := strings.TrimSpace(match[4]); detail != "" {
				text += " " + detail
			}
			return text
		})
	}
	if len(signatures) == 0 {
		return strings.Join(command, " ") + ":\n" + raw
	}
	total := 0
	for _, signature := range signatures {
		total += signature.Count
	}
	var output strings.Builder
	fmt.Fprintf(&output, "%s: %d entries, %d signatures\n", strings.Join(command, " "), total, len(signatures))
	limit := len(signatures)
	if limit > 12 {
		limit = 12
	}
	for _, signature := range signatures[:limit] {
		fmt.Fprintf(&output, "%d× %s\n", signature.Count, truncateDisplay(signature.Text, 500))
	}
	if len(signatures) > limit {
		fmt.Fprintf(&output, "… %d more signatures retained in action artifacts\n", len(signatures)-limit)
	}
	return strings.TrimSpace(output.String())
}

func groupDiagnosticLines(raw string, pattern *regexp.Regexp, normalize func([]string) string) []diagnosticSignature {
	counts := make(map[string]int)
	for _, line := range strings.Split(stripANSI(raw), "\n") {
		match := pattern.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		if signature := normalize(match); signature != "" {
			counts[signature]++
		}
	}
	result := make([]diagnosticSignature, 0, len(counts))
	for text, count := range counts {
		result = append(result, diagnosticSignature{Text: text, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Text < result[j].Text
	})
	return result
}

func normalizeDiagnosticLocation(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if index := strings.LastIndexByte(raw, ':'); index > 0 {
		if _, err := strconv.Atoi(raw[index+1:]); err == nil {
			raw = raw[:index]
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return raw
	}
	location := parsed.EscapedPath()
	if location == "" {
		location = "/"
	}
	if parsed.RawQuery != "" {
		location += "?" + parsed.RawQuery
	}
	return location
}
