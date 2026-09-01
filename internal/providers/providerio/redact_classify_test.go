package providerio

import (
	"net/http"
	"strings"
	"testing"
)

func TestRedactDoesNotMangleHelpText(t *testing.T) {
	// AUDIT-H7: the Bearer heuristic must only scrub token-shaped words, not ordinary
	// help text like "use Bearer authentication".
	for _, in := range []string{
		"Incorrect API key provided. Use Bearer authentication with your key.",
		"Send the token as a Bearer token in the Authorization header.",
	} {
		if got := Redact(in); got != in {
			t.Errorf("Redact mangled help text:\n in:  %q\n got: %q", in, got)
		}
	}
	// A real bearer token IS redacted.
	got := Redact("Authorization: Bearer sk-proj-abcdef0123456789ABCDEF")
	if strings.Contains(got, "sk-proj-abcdef0123456789ABCDEF") {
		t.Errorf("Redact failed to scrub a real bearer token: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("expected [REDACTED] for a real token, got %q", got)
	}
	// An explicit secret is always scrubbed.
	if got := Redact("key=topsecretvalue", "topsecretvalue"); strings.Contains(got, "topsecretvalue") {
		t.Errorf("explicit secret not redacted: %q", got)
	}
}

func TestClassifiedErrorCuratesAuthFailure(t *testing.T) {
	// AUDIT-H7: 401/403 must lead with an actionable instruction,
	// not the raw upstream dashboard blurb.
	msg := ClassifiedError(http.StatusUnauthorized, "Incorrect API key provided: sk-bad. Visit https://platform.openai.com/account/api-keys", "sk-bad")
	if !strings.Contains(msg, "zero setup") || !strings.Contains(msg, "zero auth openrouter") {
		t.Errorf("401 message should point at setup/provider-specific auth, got %q", msg)
	}
	if !strings.HasPrefix(msg, "auth error:") {
		t.Errorf("401 message should lead with 'auth error:', got %q", msg)
	}
	if strings.Contains(msg, "sk-bad") {
		t.Errorf("401 message leaked the key: %q", msg)
	}
	// 403 also curated.
	if got := ClassifiedError(http.StatusForbidden, "forbidden"); !strings.Contains(got, "zero setup") {
		t.Errorf("403 should also be curated, got %q", got)
	}
}
