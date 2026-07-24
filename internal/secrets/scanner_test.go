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

func TestRedactNestedSecretStillRemovesWholeBlock(t *testing.T) {
	// A PEM body can contain a substring that matches a shorter pattern (here an
	// AWS key shape, which sorts before private_key_block). Redaction must remove
	// the WHOLE block: if the inner match were replaced first it would corrupt the
	// block's exact string and leave the BEGIN/END header in the output.
	key := "-----BEGIN PRIVATE KEY-----\nAKIAIOSFODNN7EXAMPLE\nMIIEowIBAAKCAQEAbody\n-----END PRIVATE KEY-----"
	text := "leaked:\n" + key + "\ndone"

	redacted, _ := Redact(text)
	for _, leaked := range []string{"PRIVATE KEY", "AKIAIOSFODNN7EXAMPLE", "MIIEowIBAAKCAQEAbody"} {
		if strings.Contains(redacted, leaked) {
			t.Fatalf("redaction leaked %q from a nested-secret block: %q", leaked, redacted)
		}
	}
	if !strings.Contains(redacted, "[REDACTED:private_key_block]") {
		t.Fatalf("missing block placeholder: %q", redacted)
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

func TestScanDetectsModernPrefixedOpenAIKeys(t *testing.T) {
	// Modern keys carry sk-proj-/sk-svcacct-/sk-admin- prefixes and use - and _ in
	// the body; the legacy sk-<alnum> pattern would have missed them.
	for _, key := range []string{
		"sk-proj-abcDEF123_ghiJKL456-mnoPQR789stu",
		"sk-svcacct-abcDEF123_ghiJKL456-mnoPQR789",
		"sk-admin-abcDEF123_ghiJKL456-mnoPQR789stu",
	} {
		redacted, findings := Redact("token=" + key)
		if len(findings) != 1 || findings[0].Type != "openai_key" {
			t.Fatalf("expected one openai_key finding for %q, got %#v", key, findings)
		}
		if strings.Contains(redacted, key) {
			t.Fatalf("key leaked after redaction: %q", redacted)
		}
		if !strings.Contains(redacted, "[REDACTED:openai_key]") {
			t.Fatalf("missing typed placeholder for %q: %q", key, redacted)
		}
	}
}

func TestScanDetectsHyphenatedOpenAICompatibleKeys(t *testing.T) {
	// OpenRouter's sk-or-v1- keys are hyphenated like the modern sk-proj-
	// family; the legacy sk-<alnum-only> branch does not match a "-" right
	// after "sk-", so this format needs its own explicit prefix branch.
	key := "sk-or-v1-1234567890abcdef1234567890abcdef1234567890abcdef1234"
	redacted, findings := Redact("token=" + key)
	if len(findings) != 1 || findings[0].Type != "openai_key" {
		t.Fatalf("expected one openai_key finding for %q, got %#v", key, findings)
	}
	if strings.Contains(redacted, key) {
		t.Fatalf("key leaked after redaction: %q", redacted)
	}
	if !strings.Contains(redacted, "[REDACTED:openai_key]") {
		t.Fatalf("missing typed placeholder for %q: %q", key, redacted)
	}
}

func TestScanDetectsAnthropicKeys(t *testing.T) {
	for _, key := range []string{
		"sk-ant-api03-1234567890abcdefghijklmnopqrstuvwxyz-12345",
		"sk-ant-1234567890abcdefghijklmnopqrstuvwxyz-12345",
	} {
		redacted, findings := Redact("token=" + key)
		if len(findings) != 1 || findings[0].Type != "openai_key" {
			t.Fatalf("expected one openai_key finding for %q, got %#v", key, findings)
		}
		if strings.Contains(redacted, key) {
			t.Fatalf("key leaked after redaction: %q", redacted)
		}
		if !strings.Contains(redacted, "[REDACTED:openai_key]") {
			t.Fatalf("missing typed placeholder for %q: %q", key, redacted)
		}
	}
}

func TestScanRedactsLongerKeysWithoutTailLeak(t *testing.T) {
	cases := []struct {
		wantType string
		secret   string
	}{
		{"github_token", "ghp_1234567890abcdefghijklmnopqrstuvwxyz1234567890abcdef"}, // 50 chars instead of 40
		{"google_api_key", "AIzaSyA1234567890abcdefghijklmnopqrstuv12345678"},        // 50 chars instead of 39
	}
	for _, tc := range cases {
		text := "longer key is " + tc.secret + " in text"
		redacted, findings := Redact(text)
		if len(findings) != 1 || findings[0].Type != tc.wantType {
			t.Errorf("expected one %s finding, got %#v", tc.wantType, findings)
			continue
		}
		wantRedacted := "longer key is [REDACTED:" + tc.wantType + "] in text"
		if redacted != wantRedacted {
			t.Errorf("redacted = %q, want %q", redacted, wantRedacted)
		}
	}
}

// Patterns whose body class allows "-" (slack_token, google_api_key, the
// modern openai_key branch, jwt) must redact a secret that ends in "-" right
// before a delimiter: a trailing \b anchor would have no word/non-word
// transition to match there, forcing the engine to drop that last character
// from the match and leaking it.
func TestScanRedactsTrailingHyphenWithoutTailLeak(t *testing.T) {
	cases := []struct {
		wantType string
		secret   string
	}{
		{"slack_token", "xoxb-1234567890-abcdefghi-"},
		{"google_api_key", "AIzaSyA1234567890abcdefghijklmnopqrstu-"},
		{"openai_key", "sk-proj-abcDEF123_ghiJKL456-mnoPQR789st-"},
		{"jwt", "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fw-"},
	}
	for _, tc := range cases {
		text := "secret is " + tc.secret + " end"
		redacted, findings := Redact(text)
		if len(findings) != 1 || findings[0].Type != tc.wantType {
			t.Errorf("%s: expected one finding for %q, got %#v", tc.wantType, tc.secret, findings)
			continue
		}
		if strings.Contains(redacted, "-  end") || strings.Contains(redacted, "-] end") {
			t.Errorf("%s: trailing hyphen leaked: %q", tc.wantType, redacted)
		}
		wantRedacted := "secret is [REDACTED:" + tc.wantType + "] end"
		if redacted != wantRedacted {
			t.Errorf("%s: redacted = %q, want %q", tc.wantType, redacted, wantRedacted)
		}
	}
}

// A credential with more word characters appended right after its body (a
// copy-paste artifact, an env-var-style suffix) must still have its real
// secret material redacted, even though the appended run itself is outside
// the body class and so is left un-redacted. A trailing \b anchor would find
// no word/non-word transition there and, for a fixed or unbounded-greedy
// quantifier, fail the whole match instead of backtracking, leaking the
// credential entirely.
func TestScanRedactsCredentialWithAppendedSuffix(t *testing.T) {
	cases := []struct {
		wantType string
		secret   string
		suffix   string
	}{
		{"aws_access_key_id", "AKIAIOSFODNN7EXAMPLE", "EXTRA"},
		{"github_token", "ghp_1234567890abcdefghijklmnopqrstuvwxyz", "_suffix"},
	}
	for _, tc := range cases {
		text := "token=" + tc.secret + tc.suffix + " end"
		redacted, findings := Redact(text)
		if len(findings) != 1 || findings[0].Type != tc.wantType {
			t.Fatalf("%s: expected one finding for %q, got %#v", tc.wantType, tc.secret+tc.suffix, findings)
		}
		if findings[0].Match != tc.secret {
			t.Fatalf("%s: matched %q, want the credential prefix %q", tc.wantType, findings[0].Match, tc.secret)
		}
		if strings.Contains(redacted, tc.secret) {
			t.Fatalf("%s: credential leaked after redaction: %q", tc.wantType, redacted)
		}
		wantRedacted := "token=[REDACTED:" + tc.wantType + "]" + tc.suffix + " end"
		if redacted != wantRedacted {
			t.Fatalf("%s: redacted = %q, want %q", tc.wantType, redacted, wantRedacted)
		}
	}
}

func TestScanIgnoresKebabCaseStartingWithSk(t *testing.T) {
	phrase := "sk-learn-machine-learning-model"
	findings := Scan("testing " + phrase + " in text")
	if len(findings) != 0 {
		t.Errorf("expected no match for non-secret kebab-case phrase %q, got: %#v", phrase, findings)
	}
}
