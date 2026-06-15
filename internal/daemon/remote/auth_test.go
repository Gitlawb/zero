package remote

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTokenAuthenticator(t *testing.T) {
	if _, err := NewTokenAuthenticator("  "); err == nil {
		t.Fatal("empty token must be rejected (fail closed)")
	}
	a, err := NewTokenAuthenticator("s3cret")
	if err != nil {
		t.Fatalf("NewTokenAuthenticator: %v", err)
	}
	if err := a.Authenticate("s3cret"); err != nil {
		t.Fatalf("matching token should authenticate: %v", err)
	}
	if err := a.Authenticate("wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("mismatch err = %v, want ErrUnauthorized", err)
	}
	if err := a.Authenticate(""); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("empty presented token err = %v, want ErrUnauthorized", err)
	}
}

func TestTokenFromEnv(t *testing.T) {
	// Clear both so the no-config path errors.
	t.Setenv(EnvToken, "")
	t.Setenv(EnvTokenFile, "")
	if _, err := TokenFromEnv(); err == nil {
		t.Fatal("TokenFromEnv with neither var set must error")
	}
	// Direct env.
	t.Setenv(EnvToken, "from-env")
	if tok, err := TokenFromEnv(); err != nil || tok != "from-env" {
		t.Fatalf("TokenFromEnv(env) = %q, %v", tok, err)
	}
	// File (env takes precedence, so clear env first).
	t.Setenv(EnvToken, "")
	file := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(file, []byte("  from-file\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	t.Setenv(EnvTokenFile, file)
	if tok, err := TokenFromEnv(); err != nil || tok != "from-file" {
		t.Fatalf("TokenFromEnv(file) = %q, %v", tok, err)
	}
	// Empty file fails closed.
	if err := os.WriteFile(file, []byte("   \n"), 0o600); err != nil {
		t.Fatalf("rewrite token file: %v", err)
	}
	if _, err := TokenFromEnv(); err == nil {
		t.Fatal("empty token file must error")
	}
}

func TestServerTLSConfigRequiresCertKey(t *testing.T) {
	if _, err := ServerTLSConfig("", ""); err == nil {
		t.Fatal("ServerTLSConfig must require a cert and key (TLS mandatory)")
	}
	if _, err := ServerTLSConfig("/nope/cert.pem", "/nope/key.pem"); err == nil {
		t.Fatal("ServerTLSConfig must error on unreadable cert/key")
	}
}
