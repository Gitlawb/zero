package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

// AN ENDPOINT CAN BE A CREDENTIAL. HTTP and SSE send the configured URL
// verbatim, and it accepts userinfo and arbitrary query keys. The candidate
// collector gathered headers, env, args and OAuth material but never looked at
// the URL, and the generic query redaction downstream only recognises
// conventional key names, so a parameter the operator named "workspace" carried
// its token straight into this panel and the transcript.
func TestEndpointCredentialsAreRedactedFromAFailureReason(t *testing.T) {
	const token = "opaque-workspace-token-9f3c2b7ae1d8"

	for _, testCase := range []struct{ name, endpoint string }{
		{name: "arbitrary query name", endpoint: "https://host.invalid/mcp?workspace=" + token},
		{name: "conventional query name", endpoint: "https://host.invalid/mcp?api_key=" + token},
		{name: "userinfo password", endpoint: "https://svc:" + token + "@host.invalid/mcp"},
		{name: "userinfo username", endpoint: "https://" + token + "@host.invalid/mcp"},
		{name: "second of two parameters", endpoint: "https://host.invalid/mcp?mode=sse&tenant=" + token},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			raw := config.MCPServerConfig{URL: testCase.endpoint}
			// The server echoes its own endpoint back in the failure body, which is
			// what httpStatusError retains.
			failure := errors.New("502 Bad Gateway from " + testCase.endpoint)

			got := redactMCPFailureReason(failure, raw, nil)
			if strings.Contains(got, token) {
				t.Errorf("the endpoint credential survived into the rendered failure, which is also persisted:\n%s", got)
			}
		})
	}
}

// The host and path must survive, or the operator cannot tell which endpoint
// failed. Redacting the whole URL would be safe and useless.
func TestEndpointHostAndPathSurviveRedaction(t *testing.T) {
	const token = "opaque-workspace-token-9f3c2b7ae1d8"
	endpoint := "https://docs.host.invalid/mcp/v1?workspace=" + token
	raw := config.MCPServerConfig{URL: endpoint}

	got := redactMCPFailureReason(errors.New("502 Bad Gateway from "+endpoint), raw, nil)
	if strings.Contains(got, token) {
		t.Fatalf("credential survived:\n%s", got)
	}
	for _, want := range []string{"docs.host.invalid", "/mcp/v1"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was redacted too, so the message no longer identifies the endpoint:\n%s", want, got)
		}
	}
}

// Short, ordinary parameters are not credentials, and blanking them would punch
// holes through unrelated text. The shortestMCPSecret floor is what stops it.
func TestOrdinaryShortQueryValuesAreNotTreatedAsSecrets(t *testing.T) {
	raw := config.MCPServerConfig{URL: "https://host.invalid/mcp?v=1&mode=sse"}
	got := redactMCPFailureReason(errors.New("transport mode sse rejected, protocol v1"), raw, nil)
	for _, want := range []string{"sse", "v1"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was redacted as if it were a credential:\n%s", want, got)
		}
	}
}
