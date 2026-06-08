package secrets

import (
	"strings"
	"testing"
)

func TestScanDetectsHighConfidenceSecrets(t *testing.T) {
	cases := map[string]string{
		"aws_access_key_id": "AKIAIOSFODNN7EXAMPLE",
		"github_token":      "ghp_1234567890abcdefghijklmnopqrstuvwxyz",
		"slack_token":       "xoxb-1234567890-abcdefghijklmno",
		"google_api_key":    "AIzaSyA1234567890abcdefghijklmnopqrstuv",
		"private_key_block": "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAB\n-----END OPENSSH PRIVATE KEY-----",
	}
	for wantType, secret := range cases {
		text := "log line before " + secret + " and after"
		findings := Scan(text)
		if len(findings) == 0 {
			t.Errorf("%s: expected a finding for %q", wantType, secret)
			continue
		}
		if findings[0].Type != wantType {
			t.Errorf("%s: got type %q for %q", wantType, findings[0].Type, secret)
		}
	}
}

func TestRedactPrivateKeyBlockRemovesBody(t *testing.T) {
	key := "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA_secret_body_line_1\n_secret_body_line_2\n-----END RSA PRIVATE KEY-----"
	text := "here is a key:\n" + key + "\nbye"

	redacted, findings := Redact(text)
	if len(findings) != 1 || findings[0].Type != "private_key_block" {
		t.Fatalf("findings = %#v, want one private_key_block", findings)
	}
	// The KEY BODY (not just the header) must be gone.
	for _, leaked := range []string{"MIIEowIBAAKCAQEA", "_secret_body_line_1", "_secret_body_line_2", "PRIVATE KEY"} {
		if strings.Contains(redacted, leaked) {
			t.Fatalf("redaction leaked key material %q: %q", leaked, redacted)
		}
	}
	if !strings.Contains(redacted, "[REDACTED:private_key_block]") {
		t.Fatalf("missing placeholder: %q", redacted)
	}
}

func TestScanIgnoresOrdinaryText(t *testing.T) {
	clean := []string{
		"the quick brown fox jumps over the lazy dog",
		"func main() { fmt.Println(\"hello\") }",
		"commit a1b2c3d4 fixed the build",
		"export PATH=/usr/local/bin:$PATH",
		"",
	}
	for _, text := range clean {
		if findings := Scan(text); len(findings) != 0 {
			t.Errorf("false positive on %q: %#v", text, findings)
		}
	}
}

func TestRedactReplacesSecretsAndReports(t *testing.T) {
	text := "key=AKIAIOSFODNN7EXAMPLE done"
	redacted, findings := Redact(text)
	if strings.Contains(redacted, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("secret not redacted: %q", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED:aws_access_key_id]") {
		t.Fatalf("missing typed placeholder: %q", redacted)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %d, want 1", len(findings))
	}
}

func TestRedactNoMatchReturnsInputUnchanged(t *testing.T) {
	text := "nothing secret here"
	redacted, findings := Redact(text)
	if redacted != text || findings != nil {
		t.Fatalf("expected unchanged input and nil findings, got %q / %#v", redacted, findings)
	}
}
