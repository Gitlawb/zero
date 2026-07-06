package cli

import (
	"bytes"
	"strings"
	"testing"
)

// A recognized provider error in text mode prints the raw message plus a one-line
// actionable hint; a non-provider error (or JSON mode) prints no hint.
func TestWriteExecProviderErrorAppendsHint(t *testing.T) {
	t.Run("provider auth error gets a hint", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := writeExecProviderError(&stdout, &stderr, execOutputText, "provider_error",
			"auth error: your API key is missing or invalid")
		if code != exitProvider {
			t.Fatalf("exit code = %d, want exitProvider", code)
		}
		out := stderr.String()
		if !strings.Contains(out, "auth error:") {
			t.Fatalf("expected raw message, got %q", out)
		}
		if !strings.Contains(out, "zero auth") {
			t.Fatalf("expected an actionable hint referencing `zero auth`, got %q", out)
		}
	})

	t.Run("non-provider error gets no hint", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		writeExecProviderError(&stdout, &stderr, execOutputText, "sandbox_error",
			"sandbox setup failed: permission denied")
		out := stderr.String()
		// Exactly one "[zero]" line — no spurious hint attached to a local error.
		if n := strings.Count(out, "[zero]"); n != 1 {
			t.Fatalf("expected exactly one [zero] line for a non-provider error, got %d:\n%s", n, out)
		}
	})

	t.Run("json mode never appends a hint line", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		writeExecProviderError(&stdout, &stderr, execOutputJSON, "provider_error",
			"auth error: bad key")
		if stderr.Len() != 0 {
			t.Fatalf("json mode must not write to stderr, got %q", stderr.String())
		}
		if strings.Contains(stdout.String(), "zero auth") {
			t.Fatalf("json mode must not inject a hint into the structured payload, got %q", stdout.String())
		}
	})
}
