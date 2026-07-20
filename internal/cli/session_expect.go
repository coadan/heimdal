package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

type sessionExpectOptions struct {
	SessionOptions
	Role   string
	Name   string
	Text   string
	URL    string
	Target string
	Value  string
	State  string
}

func runSessionExpect(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseSessionExpectOptions(args)
	if err != nil {
		return reportSessionGrammarError(options.JSON, err, sessionActionCorrection("expect"), out, errOut)
	}
	project, state, statePath, err := discoverSession(options.SessionOptions)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if state.StoppedAt != nil {
		return reportError(options.JSON, fmt.Errorf("session %q is stopped", state.Name), out, errOut)
	}
	logicalArgs := expectLogicalArgs(options)
	locator, err := expectLocator(ctx, project, &state, statePath, options)
	if err != nil {
		return printSessionResponse(out, errOut, failedSessionGrammarResponse(state, logicalArgs, err, sessionActionCorrection("expect"), options.FullJSON), options.JSON)
	}
	result, commandErr := runSessionCommandModeArgs(ctx, project, &state, statePath, logicalArgs, []string{"run-code", expectPlaywrightCode(options, locator)}, locator, true)
	response := sessionResponse(state, result, commandErr)
	response.CompactJSON = !options.FullJSON
	response.Command = logicalArgs
	if commandErr == nil {
		response.Status = "passed"
		response.Output = "expectation passed"
	} else {
		response.Status = "failed"
		if detail := compactCLIOutput(joinOutputs(result.Stdout, result.Stderr)); detail != "" {
			response.Error = truncateDisplay(detail, 800)
		}
	}
	return printSessionResponse(out, errOut, response, options.JSON)
}

func parseSessionExpectOptions(args []string) (sessionExpectOptions, error) {
	options := sessionExpectOptions{State: "visible"}
	var common []string
	for index := 0; index < len(args); index++ {
		flag := args[index]
		switch flag {
		case "--role", "--name", "--text", "--url", "--target", "--value", "--state", "--timeout":
			value, next, err := nextValue(args, index, flag)
			if err != nil {
				return options, err
			}
			index = next
			switch flag {
			case "--role":
				options.Role = strings.ToLower(value)
			case "--name":
				options.Name = value
			case "--text":
				options.Text = value
			case "--url":
				options.URL = value
			case "--target":
				options.Target = value
			case "--value":
				options.Value = value
			case "--state":
				options.State = strings.ToLower(value)
			case "--timeout":
				duration, parseErr := time.ParseDuration(value)
				if parseErr != nil || duration <= 0 {
					return options, fmt.Errorf("--timeout must be a positive duration such as 5s (got %q)", value)
				}
				options.Timeout = duration
			}
		default:
			common = append(common, flag)
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
		options.Timeout = 5 * time.Second
	}
	parsed.Timeout = options.Timeout
	options.SessionOptions = parsed
	if len(parsed.Forwarded) > 0 {
		return options, fmt.Errorf("unknown expect arguments: %s", strings.Join(parsed.Forwarded, " "))
	}

	selectors := 0
	for _, value := range []string{options.Role, options.Text, options.URL, options.Target} {
		if value != "" {
			selectors++
		}
	}
	if selectors != 1 {
		return options, errors.New("expect requires exactly one of --role ROLE, --text TEXT, --url URL, or --target TARGET --value VALUE")
	}
	if options.Name != "" && options.Role == "" {
		return options, errors.New("--name requires --role; use --session to select a named browser session")
	}
	if options.Target != "" && options.Value == "" {
		return options, errors.New("--target requires --value")
	}
	if options.Value != "" && options.Target == "" {
		return options, errors.New("--value requires --target")
	}
	if options.URL != "" || options.Target != "" {
		if options.State != "visible" {
			return options, errors.New("--state is only valid with --role or --text")
		}
		return options, nil
	}
	switch options.State {
	case "visible", "hidden":
		return options, nil
	case "enabled", "disabled", "checked", "unchecked":
		if options.Role == "" {
			return options, fmt.Errorf("state %q requires --role", options.State)
		}
		return options, nil
	default:
		return options, fmt.Errorf("unsupported expect state %q; use visible, hidden, enabled, disabled, checked, or unchecked", options.State)
	}
}

func expectLogicalArgs(options sessionExpectOptions) []string {
	args := []string{"expect"}
	switch {
	case options.Role != "":
		args = append(args, "--role", options.Role)
		if options.Name != "" {
			args = append(args, "--name", options.Name)
		}
		args = append(args, "--state", options.State)
	case options.Text != "":
		args = append(args, "--text", options.Text, "--state", options.State)
	case options.URL != "":
		args = append(args, "--url", options.URL)
	case options.Target != "":
		args = append(args, "--target", options.Target, "--value", options.Value)
	}
	return append(args, "--timeout", options.Timeout.String())
}

func expectLocator(ctx context.Context, project Project, state *SessionState, statePath string, options sessionExpectOptions) (string, error) {
	switch {
	case options.Role != "":
		locator := "page.getByRole(" + jsonString(options.Role)
		if options.Name != "" {
			locator += ", { name: " + jsonString(options.Name) + ", exact: true }"
		}
		return locator + ").first()", nil
	case options.Text != "":
		return "page.getByText(" + jsonString(options.Text) + ", { exact: true }).first()", nil
	case options.Target != "":
		return resolveSessionLocator(ctx, project, state, statePath, options.Target)
	default:
		return "", nil
	}
}

func expectPlaywrightCode(options sessionExpectOptions, locator string) string {
	timeoutMS := options.Timeout.Milliseconds()
	if options.URL != "" {
		return fmt.Sprintf(`async page => {
  const deadline = Date.now() + %d;
  while (Date.now() < deadline) {
    if (page.url() === %s) return;
    await page.waitForTimeout(50);
  }
  throw new Error('Expectation failed: URL did not match');
}`, timeoutMS, jsonString(options.URL))
	}
	if options.Target != "" {
		return fmt.Sprintf(`async page => {
  const deadline = Date.now() + %d;
  while (Date.now() < deadline) {
    if ((await %s.inputValue()) === %s) return;
    await page.waitForTimeout(50);
  }
  throw new Error('Expectation failed: value did not match');
}`, timeoutMS, locator, jsonString(options.Value))
	}
	if options.State == "visible" || options.State == "hidden" {
		return fmt.Sprintf("async page => { await %s.waitFor({ state: %s, timeout: %d }); }", locator, jsonString(options.State), timeoutMS)
	}
	wanted := true
	method := "isEnabled"
	if options.State == "disabled" {
		wanted = false
	}
	if options.State == "checked" || options.State == "unchecked" {
		method = "isChecked"
		wanted = options.State == "checked"
	}
	return fmt.Sprintf(`async page => {
  const deadline = Date.now() + %d;
  await %s.waitFor({ state: 'visible', timeout: %d });
  while (Date.now() < deadline) {
    if ((await %s.%s()) === %t) return;
    await page.waitForTimeout(50);
  }
  throw new Error('Expectation failed: state did not match');
}`, timeoutMS, locator, timeoutMS, locator, method, wanted)
}

func expectationTestLines(action SessionActionRecord) []string {
	options, err := parseSessionExpectOptions(action.Args[1:])
	if err != nil {
		return []string{"// TODO: replace unsupported recorded expectation: " + strings.Join(action.Args, " ")}
	}
	timeout := fmt.Sprintf("{ timeout: %d }", options.Timeout.Milliseconds())
	locator := action.Locator
	if locator == "" && options.Target != "" {
		return []string{"// TODO: replace the recorded element ref with a semantic locator"}
	}
	if locator == "" {
		locator, _ = expectLocator(context.Background(), Project{}, &SessionState{}, "", options)
	}
	switch {
	case options.URL != "":
		return []string{"await expect(page).toHaveURL(" + quoteTypeScript(options.URL) + ", " + timeout + ");"}
	case options.Target != "":
		return []string{"await expect(" + locator + ").toHaveValue(" + quoteTypeScript(options.Value) + ", " + timeout + ");"}
	default:
		matcher := map[string]string{"visible": "toBeVisible", "hidden": "toBeHidden", "enabled": "toBeEnabled", "disabled": "toBeDisabled", "checked": "toBeChecked", "unchecked": "not.toBeChecked"}[options.State]
		return []string{"await expect(" + locator + ")." + matcher + "(" + timeout + ");"}
	}
}
