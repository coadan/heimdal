package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

type sessionWaitOptions struct {
	SessionOptions
	Role   string
	Name   string
	Text   string
	State  string
	Change bool
	Settle time.Duration
}

func runSessionWait(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseSessionWaitOptions(args)
	if err != nil {
		return reportSessionGrammarError(options.JSON, err, sessionActionCorrection("wait"), out, errOut)
	}
	project, state, statePath, err := discoverSession(options.SessionOptions)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if state.StoppedAt != nil {
		return reportError(options.JSON, fmt.Errorf("session %q is stopped", state.Name), out, errOut)
	}
	response := executeSessionWaitAction(ctx, project, &state, statePath, options)
	return printSessionResponse(out, errOut, response, options.JSON)
}

func executeSessionWaitAction(ctx context.Context, project Project, state *SessionState, statePath string, options sessionWaitOptions) SessionResponse {
	if options.Change {
		if response, completed := completedChangeBeforeWait(ctx, project, state, statePath, options); completed {
			return response
		}
	}
	logicalArgs := waitLogicalArgs(options)
	runtimeArgs := []string{"run-code", waitPlaywrightCode(options)}
	return executeSessionActionPlan(ctx, project, state, statePath, "wait", options.SessionOptions, logicalArgs, runtimeArgs, "")
}

func completedChangeBeforeWait(ctx context.Context, project Project, state *SessionState, statePath string, options sessionWaitOptions) (SessionResponse, bool) {
	if state.LastSnapshot == "" {
		return SessionResponse{}, false
	}
	previous, err := os.ReadFile(state.LastSnapshot)
	if err != nil {
		return SessionResponse{}, false
	}
	logicalArgs := append(waitLogicalArgs(options), "--baseline-check")
	result, commandErr := runSessionCommandModeArgs(ctx, project, state, statePath, logicalArgs, []string{"snapshot"}, "", true)
	if commandErr != nil {
		response := sessionResponse(*state, result, commandErr)
		response.Status = "failed"
		response.CompactJSON = !options.FullJSON
		response.Command = waitLogicalArgs(options)
		return response, true
	}
	current, ok := sessionSnapshotPayload(project, *state, result.Stdout)
	if !ok {
		return SessionResponse{}, false
	}
	view := semanticSnapshotDelta(string(previous), current, "", false)
	if view.Text == "No semantic changes." {
		return SessionResponse{}, false
	}
	stored, storeErr := storeSessionSnapshot(state, statePath, result.Sequence, current, true, options.Full, "", true)
	response := sessionResponse(*state, result, storeErr)
	response.CompactJSON = !options.FullJSON
	response.Command = waitLogicalArgs(options)
	response.Output = "semantic change already observed"
	response.Snapshot = stored.Text
	response.SnapshotMode = stored.Mode
	response.SnapshotOmitted = stored.Omitted
	if storeErr != nil {
		response.Status = "failed"
	} else {
		response.Status = "passed"
	}
	return response, true
}

func parseSessionWaitOptions(args []string) (sessionWaitOptions, error) {
	options := sessionWaitOptions{State: "visible"}
	var common []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--role":
			value, next, err := nextValue(args, index, "--role")
			if err != nil {
				return options, err
			}
			index, options.Role = next, strings.ToLower(value)
		case "--name", "--accessible-name":
			value, next, err := nextValue(args, index, args[index])
			if err != nil {
				return options, err
			}
			index, options.Name = next, value
		case "--text":
			value, next, err := nextValue(args, index, "--text")
			if err != nil {
				return options, err
			}
			index, options.Text = next, value
		case "--state":
			value, next, err := nextValue(args, index, "--state")
			if err != nil {
				return options, err
			}
			index, options.State = next, strings.ToLower(value)
		case "--timeout":
			value, next, err := nextValue(args, index, "--timeout")
			if err != nil {
				return options, err
			}
			duration, err := time.ParseDuration(value)
			if err != nil || duration <= 0 {
				return options, fmt.Errorf("--timeout must be a positive duration such as 30s (got %q)", value)
			}
			index, options.Timeout = next, duration
		case "--settle":
			value, next, err := nextValue(args, index, "--settle")
			if err != nil {
				return options, err
			}
			duration, err := time.ParseDuration(value)
			if err != nil || duration < 0 {
				return options, fmt.Errorf("--settle must be a non-negative duration such as 300ms (got %q)", value)
			}
			index, options.Settle = next, duration
		case "--change":
			options.Change = true
		case "--json":
			options.JSON = true
			common = append(common, args[index])
		case "--json=full":
			options.JSON, options.FullJSON = true, true
			common = append(common, args[index])
		default:
			common = append(common, args[index])
		}
	}
	parsed, err := parseSessionOptions(common)
	if err != nil {
		return options, err
	}
	if options.Timeout != 0 && parsed.Timeout != 0 {
		return options, errors.New("use either --timeout or --timeout-ms, not both")
	}
	if options.Timeout == 0 {
		options.Timeout = parsed.Timeout
	}
	if options.Timeout == 0 {
		options.Timeout = 30 * time.Second
	}
	parsed.Timeout = options.Timeout
	options.SessionOptions = parsed
	if len(parsed.Forwarded) > 0 {
		return options, fmt.Errorf("unknown wait arguments: %s", strings.Join(parsed.Forwarded, " "))
	}
	selectors := 0
	if options.Role != "" {
		selectors++
	}
	if options.Text != "" {
		selectors++
	}
	if options.Change {
		selectors++
	}
	if selectors != 1 {
		return options, errors.New("wait requires exactly one of --role ROLE, --text TEXT, or --change")
	}
	if options.Name != "" && options.Role == "" {
		return options, errors.New("--name requires --role; use --session to select a named browser session")
	}
	if options.Change {
		if options.State != "visible" {
			return options, errors.New("--change does not accept --state")
		}
		return options, nil
	}
	switch options.State {
	case "attached", "detached", "visible", "hidden", "enabled", "disabled":
		return options, nil
	default:
		return options, fmt.Errorf("unsupported wait state %q; use attached, detached, visible, hidden, enabled, or disabled", options.State)
	}
}

func reportSessionGrammarError(asJSON bool, err error, correction string, out, errOut io.Writer) int {
	if asJSON {
		_ = writeJSONTo(out, map[string]any{"schema_version": 1, "status": "error", "error": err.Error(), "correction": correction})
	} else {
		fmt.Fprintln(errOut, "heimdal:", err)
		fmt.Fprintln(errOut, "heimdal: correction:", correction)
	}
	return 1
}

func waitLogicalArgs(options sessionWaitOptions) []string {
	args := []string{"wait"}
	if options.Change {
		args = append(args, "--change")
	} else if options.Role != "" {
		args = append(args, "--role", options.Role)
		if options.Name != "" {
			args = append(args, "--name", options.Name)
		}
	} else {
		args = append(args, "--text", options.Text)
	}
	if !options.Change {
		args = append(args, "--state", options.State)
	}
	args = append(args, "--timeout", options.Timeout.String())
	if options.Settle > 0 {
		args = append(args, "--settle", options.Settle.String())
	}
	return args
}

func waitPlaywrightCode(options sessionWaitOptions) string {
	timeoutMS := options.Timeout.Milliseconds()
	settleMS := options.Settle.Milliseconds()
	if options.Change {
		return fmt.Sprintf(`async page => {
  const root = page.locator('body');
  const before = await root.ariaSnapshot();
  const deadline = Date.now() + %d;
  let changed = false;
  let stable = '';
  let stableSince = 0;
  while (Date.now() < deadline) {
    await page.waitForTimeout(100);
    const current = await root.ariaSnapshot();
    if (!changed && current !== before) {
      changed = true;
      stable = current;
      stableSince = Date.now();
      if (%d === 0) return;
      continue;
    }
    if (changed && current !== stable) {
      stable = current;
      stableSince = Date.now();
    }
    if (changed && Date.now() - stableSince >= %d) return;
  }
  throw new Error(changed ? 'Timed out waiting for semantic state to settle' : 'Timed out waiting for a semantic page change');
}`, timeoutMS, settleMS, settleMS)
	}
	locator := "page.getByText(" + jsonString(options.Text) + ").first()"
	if options.Role != "" {
		locator = "page.getByRole(" + jsonString(options.Role)
		if options.Name != "" {
			locator += ", { name: " + jsonString(options.Name) + " }"
		}
		locator += ").first()"
	}
	settle := fmt.Sprintf(`
  if (%d > 0) {
    const root = page.locator('body');
    let stable = await root.ariaSnapshot();
    let stableSince = Date.now();
    while (Date.now() < deadline) {
      await page.waitForTimeout(Math.min(100, Math.max(1, deadline - Date.now())));
      const current = await root.ariaSnapshot();
      if (current !== stable) {
        stable = current;
        stableSince = Date.now();
      } else if (Date.now() - stableSince >= %d) {
        return;
      }
    }
    throw new Error('Timed out waiting for semantic state to settle');
  }`, settleMS, settleMS)
	if options.State != "enabled" && options.State != "disabled" {
		return fmt.Sprintf(`async page => {
  const deadline = Date.now() + %d;
  await %s.waitFor({ state: %s, timeout: Math.max(1, deadline - Date.now()) });%s
}`, timeoutMS, locator, jsonString(options.State), settle)
	}
	wanted := "true"
	if options.State == "disabled" {
		wanted = "false"
	}
	return fmt.Sprintf(`async page => {
  const target = %s;
  const deadline = Date.now() + %d;
  await target.waitFor({ state: 'visible', timeout: Math.max(1, deadline - Date.now()) });
  while (Date.now() < deadline) {
    if ((await target.isEnabled()) === %s) break;
    await page.waitForTimeout(100);
  }
  if ((await target.isEnabled()) !== %s) throw new Error('Timed out waiting for element to become %s');%s
}`, locator, timeoutMS, wanted, wanted, options.State, settle)
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
