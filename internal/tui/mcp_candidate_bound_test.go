package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
)

// A DELIMITER-HEAVY CONFIGURED VALUE MUST NOT EXPAND WITHOUT BOUND.
//
// credentialCandidates walked every suffix after every space or colon and kept
// them all, and RedactString then ran a replacement pass per candidate. The
// expansion happened on the CONFIG side, outside the raw-error bound, so the
// cost did not depend on the server's error being long at all: opening or
// refreshing /mcp for that server paid it however short the failure was.
func TestADelimiterHeavyValueDoesNotExpandWithoutBound(t *testing.T) {
	value := strings.Repeat("token:part ", 4000)

	candidates := credentialCandidates(value)
	if len(candidates) > maxMCPCredentialCandidates {
		t.Errorf("one configured value expanded into %d candidates, want at most %d", len(candidates), maxMCPCredentialCandidates)
	}

	// And the whole value is still redacted, which is the point of bounding the
	// tails rather than the value.
	raw := config.MCPServerConfig{
		URL:     "https://host.invalid/mcp",
		Headers: map[string]string{"X-Workspace": value},
	}
	started := time.Now()
	got := redactMCPFailureReason(errors.New("502 Bad Gateway from "+value), raw, nil)
	elapsed := time.Since(started)

	if strings.Contains(got, value) {
		t.Error("the configured value survived redaction")
	}
	// Loose on purpose: it fails on the old superlinear behaviour and not on a
	// slow machine.
	if elapsed > 2*time.Second {
		t.Errorf("redacting one failure took %s; the candidate expansion is still superlinear in the configured value", elapsed)
	}
}

// An oversized value is still redacted whole. Only the suffix enumeration is
// dropped, and that is what cost.
func TestAnOversizedValueIsStillRedactedWhole(t *testing.T) {
	value := strings.Repeat("Qw7ZmPr4", (maxMCPCredentialInput/8)+64)
	if len(value) <= maxMCPCredentialInput {
		t.Fatalf("the fixture is %d bytes, which does not exceed the %d-byte input bound", len(value), maxMCPCredentialInput)
	}
	candidates := credentialCandidates(value)
	if len(candidates) != 1 || candidates[0] != value {
		t.Fatalf("an oversized value produced %d candidates; it must still yield itself", len(candidates))
	}
}
