package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
)

// THE BOUND IS AT INGRESS, NOT AT THE END.
//
// The cap lived only in sanitizeTerminalReason, so the entire server-controlled
// string was redacted, walked rune by rune into a fresh builder and a fresh
// []rune by stripTerminalRejoiners, redacted again, and only then cut, for a
// panel that shows at most 400 runes. The existing raw-bound test calls the
// sanitizer directly, so it never exercised this path.
func TestOversizedFailureIsBoundedBeforeRedactionAndNormalization(t *testing.T) {
	raw := config.MCPServerConfig{URL: "https://host.invalid/mcp"}
	huge := strings.Repeat("A", 8*1024*1024)

	got := redactMCPFailureReason(errors.New("tool name conflict: "+huge), raw, nil)
	if len(got) > maxMCPReasonRawLen+1024 {
		t.Errorf("the failure reason is %d bytes; the pipeline is still carrying the whole server-controlled value", len(got))
	}
}

// A SECRET THAT STRADDLES THE CUT MUST NOT LEAVE A VISIBLE PREFIX. Slicing at
// exactly the cap would truncate the credential, and the surviving prefix would
// then match nothing and print, so the bound would have created the leak it has
// nothing to do with.
func TestASecretStraddlingTheBoundIsStillFullyRedacted(t *testing.T) {
	const token = "opaque-workspace-token-9f3c2b7ae1d8"
	raw := config.MCPServerConfig{URL: "https://host.invalid/mcp?workspace=" + token}

	// Position the token so it begins just inside the cap and ends past it.
	filler := strings.Repeat("A", maxMCPReasonRawLen-len(token)/2)
	got := redactMCPFailureReason(errors.New(filler+token+strings.Repeat("B", 4096)), raw, nil)

	if strings.Contains(got, token) {
		t.Fatalf("the whole token survived")
	}
	// Any prefix of the token longer than a few characters is a leak.
	for size := len(token); size > 8; size-- {
		if strings.Contains(got, token[:size]) {
			t.Errorf("a %d-character prefix of the credential survived the bound: %q", size, token[:size])
			break
		}
	}
}

// And an ordinary short failure is untouched, or the bound would be quietly
// eating normal diagnostics.
func TestOrdinaryFailureIsNotTruncatedByTheBound(t *testing.T) {
	raw := config.MCPServerConfig{URL: "https://host.invalid/mcp"}
	message := "dial tcp 10.0.0.5:443: connect: connection refused"
	got := redactMCPFailureReason(errors.New(message), raw, nil)
	if !strings.Contains(got, message) {
		t.Errorf("an ordinary failure was altered by the bound:\n%s", got)
	}
}
