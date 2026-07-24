package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	defaultReconnectOfflineFor = 500 * time.Millisecond
	defaultReconnectTimeout    = 30 * time.Second
)

type sessionReconnectOptions struct {
	SessionOptions
	Request    string
	OfflineFor time.Duration
}

func runSessionReconnect(ctx context.Context, args []string, out, errOut io.Writer) int {
	options, err := parseSessionReconnectOptions(args)
	if err != nil {
		return reportSessionGrammarError(options.JSON, err, sessionActionCorrection("reconnect"), out, errOut)
	}
	project, state, statePath, err := discoverSession(options.SessionOptions)
	if err != nil {
		return reportError(options.JSON, err, out, errOut)
	}
	if state.StoppedAt != nil {
		return reportError(options.JSON, fmt.Errorf("session %q is stopped", state.Name), out, errOut)
	}
	response := executeSessionReconnectAction(ctx, project, &state, statePath, options)
	return printSessionResponse(out, errOut, response, options.JSON)
}

func executeSessionReconnectAction(ctx context.Context, project Project, state *SessionState, statePath string, options sessionReconnectOptions) SessionResponse {
	logicalArgs := reconnectLogicalArgs(options)
	runtimeArgs := []string{"run-code", reconnectPlaywrightCode(options)}
	response := executeSessionActionPlan(ctx, project, state, statePath, "reconnect", options.SessionOptions, logicalArgs, runtimeArgs, "")
	if response.Status == "passed" {
		response.Output = fmt.Sprintf("connection cycled offline for %s", options.OfflineFor)
		if options.Request != "" {
			response.Output += fmt.Sprintf("; observed reconnect request containing %q", options.Request)
		}
	}
	return response
}

func parseSessionReconnectOptions(args []string) (sessionReconnectOptions, error) {
	options := sessionReconnectOptions{
		OfflineFor: defaultReconnectOfflineFor,
	}
	var common []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--request":
			value, next, err := nextValue(args, index, "--request")
			if err != nil {
				return options, err
			}
			if strings.TrimSpace(value) == "" {
				return options, errors.New("--request requires a non-empty URL substring")
			}
			index, options.Request = next, value
		case "--offline-for":
			value, next, err := nextValue(args, index, "--offline-for")
			if err != nil {
				return options, err
			}
			duration, err := time.ParseDuration(value)
			if err != nil || duration < time.Millisecond {
				return options, fmt.Errorf("--offline-for must be at least 1ms, such as 500ms (got %q)", value)
			}
			index, options.OfflineFor = next, duration
		case "--timeout":
			value, next, err := nextValue(args, index, "--timeout")
			if err != nil {
				return options, err
			}
			duration, err := time.ParseDuration(value)
			if err != nil || duration < time.Millisecond {
				return options, fmt.Errorf("--timeout must be at least 1ms, such as 30s (got %q)", value)
			}
			index, options.Timeout = next, duration
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
		options.Timeout = defaultReconnectTimeout
	}
	parsed.Timeout = options.Timeout
	options.SessionOptions = parsed
	if len(parsed.Forwarded) > 0 {
		return options, fmt.Errorf("unknown reconnect arguments: %s", strings.Join(parsed.Forwarded, " "))
	}
	return options, nil
}

func reconnectLogicalArgs(options sessionReconnectOptions) []string {
	args := []string{"reconnect", "--offline-for", options.OfflineFor.String()}
	if options.Request != "" {
		args = append(args, "--request", options.Request, "--timeout", options.Timeout.String())
	}
	return args
}

func reconnectPlaywrightCode(options sessionReconnectOptions) string {
	return fmt.Sprintf(`async page => {
  const context = page.context();
  const requestContains = %s;
  const offlineForMs = %d;
  const timeoutMs = %d;
  await context.setOffline(false);
  try {
    await page.evaluate(() => window.stop());
    await context.setOffline(true);
    await page.waitForTimeout(offlineForMs);
    const reconnectRequest = requestContains
      ? page.waitForRequest(request => request.url().includes(requestContains), { timeout: timeoutMs })
      : null;
    await context.setOffline(false);
    if (!reconnectRequest) {
      return { version: 1, offline_for_ms: offlineForMs };
    }
    const request = await reconnectRequest;
    return {
      version: 1,
      offline_for_ms: offlineForMs,
      request: {
        url: request.url(),
        method: request.method(),
        resource_type: request.resourceType()
      }
    };
  } finally {
    await context.setOffline(false);
  }
}`, jsonString(options.Request), options.OfflineFor.Milliseconds(), options.Timeout.Milliseconds())
}
