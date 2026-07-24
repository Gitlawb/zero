package redaction

import (
	"strings"
	"testing"
)

func TestIsSensitiveKey_CompoundAndTokenCounts(t *testing.T) {
	o := Options{}
	sensitive := []string{
		"db_password", "smtp_password", "user_password",
		"session_secret", "webhook_secret", "stripe_secret_key", "client-secret",
		"auth_token", "csrf_token", "vault_token", "my_token", "xsrf-token",
		"ssh_private_key", "rsa_private_key",
		"backup_api_key", "my_apikey", "service_apikey",
		"db_passphrase", "aws_credential", "gcp_credentials",
	}
	for _, k := range sensitive {
		if !IsSensitiveKey(k, o) {
			t.Errorf("expected %q to be sensitive", k)
		}
	}
	// CRITICAL: token-count and ordinary "*_key" fields must NOT be redacted —
	// they are everywhere in an LLM agent and redacting them would break it.
	notSensitive := []string{
		"max_tokens", "prompt_tokens", "completion_tokens", "total_tokens",
		"input_tokens", "output_tokens", "reasoning_tokens", "tokens",
		"token_count", "token_usage", "tokens_used",
		"primary_key", "public_key", "cache_key", "foreign_key", "sort_key",
		"idempotency_key", "key", "key_name", "api_version", "username", "message",
	}
	for _, k := range notSensitive {
		if IsSensitiveKey(k, o) {
			t.Errorf("expected %q to NOT be sensitive (false positive)", k)
		}
	}
}

func TestRedactString_CompoundKeyForms(t *testing.T) {
	o := Options{}
	cases := []struct{ in, secret string }{
		{"db_password=hunter2supersecret", "hunter2supersecret"},
		{`{"stripe_secret_key": "rk_live_plaintextvalue"}`, "rk_live_plaintextvalue"},
		{"GET /x?session_secret=plainsecretvalue HTTP/1.1", "plainsecretvalue"},
		{"auth_token='opaque-assigned-token-value'", "opaque-assigned-token-value"},
	}
	for _, c := range cases {
		out := RedactString(c.in, o)
		if strings.Contains(out, c.secret) {
			t.Errorf("RedactString(%q) leaked %q: got %q", c.in, c.secret, out)
		}
		if !strings.Contains(out, RedactedSecret) {
			t.Errorf("RedactString(%q) should contain %q: got %q", c.in, RedactedSecret, out)
		}
	}
	// Token-count lines must pass through untouched.
	for _, c := range []string{"max_tokens: 4096", "prompt_tokens=128", "token_count: 50"} {
		if out := RedactString(c, o); out != c {
			t.Errorf("RedactString(%q) should be unchanged, got %q", c, out)
		}
	}
}

func TestRedactString_OverlappingExtraSecrets(t *testing.T) {
	opts := Options{
		ExtraSecretValues: []string{"secret", "secret_token"},
	}
	out := RedactString("my secret_token value", opts)
	if strings.Contains(out, "_token") {
		t.Fatalf("RedactString left partial fragment '_token': got %q", out)
	}
	if !strings.Contains(out, RedactedSecret) {
		t.Fatalf("expected RedactedSecret in output: got %q", out)
	}
}

func TestRedactString_AuthHeaderSchemes(t *testing.T) {
	o := Options{}
	// Opaque values (no token-format prefix) so only the header/colon logic can
	// redact them — isolates the auth-scheme fix from the text-pattern fallback.
	cases := []struct{ in, secret string }{
		{"Authorization: Bearer opaquebearervalue1234567", "opaquebearervalue1234567"},
		{"Authorization: token opaquetokenvalue1234567", "opaquetokenvalue1234567"},
		{"Authorization: ApiKey opaqueapikeyvalue1234567", "opaqueapikeyvalue1234567"},
		{"Proxy-Authorization: Digest opaquedigestvalue1234", "opaquedigestvalue1234"},
	}
	for _, c := range cases {
		out := RedactString(c.in, o)
		if strings.Contains(out, c.secret) {
			t.Errorf("RedactString(%q) leaked %q: got %q", c.in, c.secret, out)
		}
	}
}

// Parameterized schemes spread the secret across comma-separated params, so the
// WHOLE value after the scheme must be redacted — not just the first token.
func TestRedactString_MultiPartAuthSchemes(t *testing.T) {
	o := Options{}
	cases := []struct {
		in      string
		leaked  []string // every one of these must be gone
		keepsch string   // scheme name that should remain visible
	}{
		{
			in:      `Authorization: Digest username="alice", realm="api", nonce="abc123", uri="/x", response="deadbeefcafef00ddeadbeefcafef00d", qop=auth`,
			leaked:  []string{"deadbeefcafef00ddeadbeefcafef00d", `nonce="abc123"`, `username="alice"`},
			keepsch: "Digest",
		},
		{
			in:      `Authorization: AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20260618/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-date, Signature=fe5f80f77d5fa3beca038a248ff027d0445342fe2855ddc963176630326f1024`,
			leaked:  []string{"fe5f80f77d5fa3beca038a248ff027d0445342fe2855ddc963176630326f1024", "SignedHeaders=host;x-amz-date", "AKIAIOSFODNN7EXAMPLE"},
			keepsch: "AWS4-HMAC-SHA256",
		},
	}
	for _, c := range cases {
		out := RedactString(c.in, o)
		for _, secret := range c.leaked {
			if strings.Contains(out, secret) {
				t.Errorf("multi-part header leaked %q\n  in:  %s\n  out: %s", secret, c.in, out)
			}
		}
		if !strings.Contains(out, c.keepsch) {
			t.Errorf("scheme %q should remain visible, got: %s", c.keepsch, out)
		}
		if !strings.Contains(out, RedactedSecret) {
			t.Errorf("expected a redaction marker, got: %s", out)
		}
	}
}

// Guard against re-introducing an over-broad "key: value" pattern: a
// "file.go:line: message" line (universal in test/compiler output, where the
// filename may even contain a secret-looking segment) must keep its structure so
// downstream parsers still work. Only an actual secret VALUE on the line is
// redacted (by the token-format patterns), never the file:line prefix.
func TestRedactString_PreservesFileLineColons(t *testing.T) {
	o := Options{}
	in := "    secret_test.go:12: token sk-proj-abcdefghijklmnopqrstuvwxyz"
	out := RedactString(in, o)
	if !strings.Contains(out, "secret_test.go:12:") {
		t.Errorf("file:line prefix mangled: %q", out)
	}
	if strings.Contains(out, "sk-proj-abcdefghijklmnopqrstuvwxyz") {
		t.Errorf("secret leaked: %q", out)
	}
	if !strings.Contains(out, RedactedSecret) {
		t.Errorf("expected the token to be redacted: %q", out)
	}
}

func TestRedactValue_CompoundKeys(t *testing.T) {
	o := Options{}
	in := map[string]any{
		"db_password": "plainpw",
		"max_tokens":  4096,
		"nested":      map[string]any{"session_secret": "innersecretvalue"},
	}
	out, ok := RedactValue(in, o).(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", RedactValue(in, o))
	}
	if out["db_password"] != RedactedSecret {
		t.Errorf("db_password not redacted: %v", out["db_password"])
	}
	if out["max_tokens"] != int64(4096) {
		t.Errorf("max_tokens should be preserved as 4096, got %v (%T)", out["max_tokens"], out["max_tokens"])
	}
	inner := out["nested"].(map[string]any)
	if inner["session_secret"] != RedactedSecret {
		t.Errorf("nested session_secret not redacted: %v", inner["session_secret"])
	}
}
