package redaction

import (
	"strings"
	"testing"
)

func TestRedactStringRedactsOpaqueAuthorization(t *testing.T) {
	// Opaque tokens and custom (non-standard) schemes must still be redacted in
	// free text — not just the recognized bearer/basic/digest/etc. schemes (M12).
	cases := []struct{ in, secret string }{
		{"Authorization: Bearer sk-secret-123", "sk-secret-123"},
		{"Authorization: sk-opaque-no-scheme-456", "sk-opaque-no-scheme-456"},
		{"authorization: Custom-Scheme deadbeefsecret", "deadbeefsecret"},
		{"Proxy-Authorization: rawproxytoken789", "rawproxytoken789"},
	}
	for _, c := range cases {
		out := RedactString(c.in, Options{})
		if strings.Contains(out, c.secret) {
			t.Errorf("secret leaked for %q -> %q", c.in, out)
		}
	}
	// A recognized scheme keeps its name for readability.
	out := RedactString("Authorization: Bearer sk-x", Options{})
	if !strings.Contains(strings.ToLower(out), "bearer") {
		t.Errorf("known scheme name should be preserved: %q", out)
	}
}
