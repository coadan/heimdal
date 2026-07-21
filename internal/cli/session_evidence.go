package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

func runSessionEvidence(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseSessionOptions(args)
	if err != nil {
		return reportSessionGrammarError(options.JSON, err, sessionActionCorrection("evidence"), out, errOut)
	}
	if len(options.Forwarded) != 2 {
		return reportSessionGrammarError(options.JSON, errors.New("session evidence requires NAME and one Playwright evaluation expression"), sessionActionCorrection("evidence"), out, errOut)
	}
	name, expression := options.Forwarded[0], options.Forwarded[1]
	if err := validateCoordinationSelector("evidence", name); err != nil {
		return reportSessionGrammarError(options.JSON, err, sessionActionCorrection("evidence"), out, errOut)
	}
	project, state, statePath, err := discoverSession(options)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if state.StoppedAt != nil {
		return reportError(options.JSON, fmt.Errorf("session %q is stopped", state.Name), out, errOut)
	}
	response := executeSessionEvidenceAction(ctx, project, &state, statePath, name, expression, options.FullJSON)
	return printSessionResponse(out, errOut, response, options.JSON)
}

func executeSessionEvidenceAction(ctx context.Context, project Project, state *SessionState, statePath, name, expression string, fullJSON bool) SessionResponse {
	logicalArgs := []string{"evidence", name, expression}
	runtimeCode := "async page => await page.evaluate(" + expression + ")"
	result, commandErr := runSessionCommandModeArgs(ctx, project, state, statePath, logicalArgs, []string{"run-code", runtimeCode}, "", true)
	response := sessionResponse(*state, result, commandErr)
	response.CompactJSON = !fullJSON
	response.Command = compactSessionBatchArgs(logicalArgs)
	if commandErr != nil {
		response.Status = "failed"
		if detail := compactCLIOutput(joinOutputs(result.Stdout, result.Stderr)); detail != "" {
			response.Error = truncateDisplay(detail, 800)
		}
		return response
	}
	payload, err := parseSessionBatchEvidence(result.Stdout)
	if err != nil {
		response.Status = "failed"
		response.Error = err.Error()
		return response
	}
	response.Status = "passed"
	response.Output = "captured named evidence " + name
	response.Evidence = map[string]json.RawMessage{name: payload}
	return response
}
