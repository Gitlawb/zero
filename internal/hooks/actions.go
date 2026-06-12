package hooks

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// PromptRunner evaluates a hook prompt against a model and returns the model's
// text output. It is injected so the hooks package stays decoupled from the
// provider/agent stack and can be exercised with a fake in tests. A nil runner
// makes prompt hooks fail (as a non-blocking error), never block.
type PromptRunner func(ctx context.Context, model string, prompt string, payload []byte) (string, error)

// maxHTTPHookResponseBytes caps how much of an HTTP hook response is captured so
// a chatty endpoint cannot bloat the audit log or model context.
const maxHTTPHookResponseBytes = 64 * 1024

// noRedirectHTTPClient refuses to follow redirects. Following a 3xx would let an
// allowlisted endpoint bounce the request (and its event payload) to a host that
// is not on the allowlist — an allowlist bypass / SSRF vector. ErrUseLastResponse
// returns the 3xx response unfollowed, which runHTTPAction reports as a failure.
var noRedirectHTTPClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// executeAction runs one hook through the executor selected by its Type, bounded
// by the dispatcher timeout. The command path preserves the original behaviour;
// prompt and http are the Stage 05 additions. A deadline/cancellation that fired
// mid-run is reported via TimedOut so classifyResult can fail closed on blocking
// events, exactly as the command path does.
func (dispatcher *Dispatcher) executeAction(ctx context.Context, hook Definition, payload []byte) commandResult {
	runCtx, cancel := context.WithTimeout(ctx, dispatcher.timeout)
	defer cancel()

	var result commandResult
	switch hook.Type {
	case ActionPrompt:
		result = dispatcher.runPromptAction(runCtx, hook, payload)
	case ActionHTTP:
		result = dispatcher.runHTTPAction(runCtx, hook, payload)
	default: // ActionCommand or "" (back-compat)
		env := os.Environ()
		if len(dispatcher.env) > 0 {
			env = append(env, dispatcher.env...)
		}
		result = dispatcher.run(runCtx, hook.Command, hook.Args, payload, dispatcher.cwd, env)
	}
	if runCtx.Err() != nil {
		result.TimedOut = true
	}
	return result
}

func (dispatcher *Dispatcher) runPromptAction(ctx context.Context, hook Definition, payload []byte) commandResult {
	if dispatcher.promptRunner == nil {
		return commandResult{ExitCode: -1, Err: errors.New("prompt hook requires a configured prompt runner")}
	}
	output, err := dispatcher.promptRunner(ctx, hook.Model, hook.Prompt, payload)
	if err != nil {
		return commandResult{ExitCode: -1, Err: err}
	}
	return commandResult{ExitCode: 0, Stdout: output}
}

func (dispatcher *Dispatcher) runHTTPAction(ctx context.Context, hook Definition, payload []byte) commandResult {
	// Allowlist is enforced here, never at parse time: untrusted config must not be
	// able to POST to an arbitrary URL just because it is syntactically valid.
	if !dispatcher.allowedHTTPURLs[hook.URL] {
		return commandResult{ExitCode: -1, Err: fmt.Errorf("http hook url is not on the allowlist: %s", hook.URL)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, bytes.NewReader(payload))
	if err != nil {
		return commandResult{ExitCode: -1, Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	client := dispatcher.httpClient
	if client == nil {
		client = noRedirectHTTPClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return commandResult{ExitCode: -1, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxHTTPHookResponseBytes))
	if readErr != nil {
		// A reset/truncated body means we never got the full verdict; treat it as
		// an execution failure rather than silently reporting a clean (ExitCode 0) pass.
		return commandResult{ExitCode: -1, Err: fmt.Errorf("read http hook response: %w", readErr)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return commandResult{ExitCode: 1, Stderr: fmt.Sprintf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	}
	return commandResult{ExitCode: 0, Stdout: strings.TrimSpace(string(body))}
}
