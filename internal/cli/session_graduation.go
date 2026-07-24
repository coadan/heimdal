package cli

import "strings"

type SessionGraduation struct {
	Ready             bool     `json:"ready"`
	RecordedActions   int      `json:"recorded_actions"`
	PortableActions   int      `json:"portable_actions"`
	Assertions        int      `json:"assertions"`
	CoordinateActions int      `json:"coordinate_actions,omitempty"`
	StaleReferences   int      `json:"stale_references,omitempty"`
	Unsupported       int      `json:"unsupported_actions,omitempty"`
	Issues            []string `json:"issues,omitempty"`
}

func auditSessionGraduation(actions []SessionActionRecord) SessionGraduation {
	audit := SessionGraduation{}
	for _, action := range actions {
		if len(action.Args) == 0 || action.ExitCode != 0 || ignoredGraduationAction(action.Args[0]) {
			continue
		}
		audit.RecordedActions++
		if action.Args[0] == "expect" {
			audit.Assertions++
		}
		if action.Args[0] == "mouse" || action.Args[0] == "drag" || action.Args[0] == "pointer" {
			audit.CoordinateActions++
		}
		if len(action.Args) > 1 && strings.HasPrefix(action.Args[1], "e") && action.Locator == "" && action.Args[0] != "expect" {
			audit.StaleReferences++
		}
		lines := sessionActionTestLines(action)
		portable := len(lines) > 0
		for _, line := range lines {
			if strings.Contains(line, "TODO:") || strings.Contains(line, "Heimdal action:") {
				portable = false
			}
		}
		if action.Args[0] == "eval" || action.Args[0] == "run-code" {
			portable = false
		}
		if portable {
			audit.PortableActions++
		} else {
			audit.Unsupported++
		}
	}
	if audit.Assertions == 0 {
		audit.Issues = append(audit.Issues, "generated test has no recorded outcome assertion; use `heimdal session expect ...`")
	}
	if audit.CoordinateActions > 0 {
		audit.Issues = append(audit.Issues, "coordinate actions require element-relative or semantic replacement before graduation")
	}
	if audit.StaleReferences > 0 {
		audit.Issues = append(audit.Issues, "recorded refs without portable Playwright locators require replacement")
	}
	if audit.Unsupported > 0 {
		audit.Issues = append(audit.Issues, "unsupported exploration actions remain as TODO comments in the generated test")
	}
	audit.Ready = len(audit.Issues) == 0
	return audit
}

func ignoredGraduationAction(command string) bool {
	switch command {
	case "open", "snapshot", "screenshot", "measure", "evidence", "console", "requests", "highlight", "find", "tab-list", "request", "request-headers", "request-body", "response-headers", "response-body", "cookie-list", "cookie-get", "localstorage-list", "localstorage-get", "sessionstorage-list", "sessionstorage-get", "checkpoint", "wait", "reconnect":
		return true
	default:
		return false
	}
}
