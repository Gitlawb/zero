package remote

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

// TestTokenFilePathFromEnvPreservesFilenameWhitespace pins the pointer as a
// pathname rather than a trimmed word. Trimming it made this boundary select
// "<dir>/bridge-token" while the operator had named "<dir>/bridge-token " —
// which fails the daemon start at best, and at worst reads and protects a
// different file than the one holding the bearer token.
func TestTokenFilePathFromEnvPreservesFilenameWhitespace(t *testing.T) {
	t.Setenv(EnvToken, "")
	for _, configured := range []string{"/srv/zero/bridge-token ", " /srv/zero/bridge-token", "/srv/zero/ token "} {
		t.Setenv(EnvTokenFile, configured)
		if got := TokenFilePathFromEnv(); got != configured {
			t.Fatalf("TokenFilePathFromEnv() = %q, want the configured pathname %q", got, configured)
		}
	}
	for _, blank := range []string{"", "   ", "\t\n"} {
		t.Setenv(EnvTokenFile, blank)
		if got := TokenFilePathFromEnv(); got != "" {
			t.Fatalf("TokenFilePathFromEnv() = %q for a blank value, want unset", got)
		}
	}
}

// TestCanonicalizeTokenFileEnvKeepsTrailingSpaceFilename covers the same rule at
// the daemon boundary: the file the bridge authenticates against must survive
// canonicalization byte for byte.
func TestCanonicalizeTokenFileEnvKeepsTrailingSpaceFilename(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot end in a space")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	token := filepath.Join(base, "bridge-token ")
	if err := os.WriteFile(token, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	t.Chdir(base)
	t.Setenv(EnvToken, "")
	t.Setenv(EnvTokenFile, "bridge-token ")

	if err := CanonicalizeTokenFileEnv(); err != nil {
		t.Fatalf("CanonicalizeTokenFileEnv: %v", err)
	}
	if got := os.Getenv(EnvTokenFile); got != token {
		t.Fatalf("%s = %q, want %q", EnvTokenFile, got, token)
	}
	t.Chdir(t.TempDir())
	if tok, err := TokenFromEnv(); err != nil || tok != "from-file" {
		t.Fatalf("TokenFromEnv after canonicalization = %q, %v", tok, err)
	}
}

// TestCanonicalizeTokenFileEnv pins the value every child process (and the
// sandbox profile derived for it) inherits to the file this process reads.
func TestCanonicalizeTokenFileEnv(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks: %v", err)
	}
	token := filepath.Join(base, "tok")
	if err := os.WriteFile(token, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	t.Run("relative value becomes absolute", func(t *testing.T) {
		// A worker resolves the inherited value against its own session directory,
		// so a relative value must not survive the daemon boundary.
		t.Chdir(base)
		t.Setenv(EnvToken, "")
		t.Setenv(EnvTokenFile, "tok")
		if err := CanonicalizeTokenFileEnv(); err != nil {
			t.Fatalf("CanonicalizeTokenFileEnv: %v", err)
		}
		if got := os.Getenv(EnvTokenFile); got != token {
			t.Fatalf("%s = %q, want %q", EnvTokenFile, got, token)
		}
		// Workers run from session directories, so prove the selected path remains
		// pinned after crossing that boundary rather than merely inspecting the env.
		t.Chdir(t.TempDir())
		if tok, err := TokenFromEnv(); err != nil || tok != "from-file" {
			t.Fatalf("TokenFromEnv from a worker directory after canonicalization = %q, %v", tok, err)
		}
	})

	t.Run("symlinked pathname is resolved", func(t *testing.T) {
		link := filepath.Join(base, "tok-link")
		if err := os.Symlink(token, link); err != nil {
			t.Skipf("symlink unsupported: %v", err)
		}
		t.Setenv(EnvToken, "")
		t.Setenv(EnvTokenFile, link)
		if err := CanonicalizeTokenFileEnv(); err != nil {
			t.Fatalf("CanonicalizeTokenFileEnv: %v", err)
		}
		if got := os.Getenv(EnvTokenFile); got != token {
			t.Fatalf("%s = %q, want the resolved target %q", EnvTokenFile, got, token)
		}
	})

	t.Run("an inline token keeps precedence over a dangling pointer", func(t *testing.T) {
		// TokenFromEnv prefers EnvToken, so an unused (even dangling) file pointer
		// must neither fail the start nor be rewritten.
		dangling := filepath.Join(base, "missing", "tok")
		t.Setenv(EnvToken, "from-env")
		t.Setenv(EnvTokenFile, dangling)
		if err := CanonicalizeTokenFileEnv(); err != nil {
			t.Fatalf("CanonicalizeTokenFileEnv with an inline token: %v", err)
		}
		if got := os.Getenv(EnvTokenFile); got != dangling {
			t.Fatalf("%s = %q, want it left alone", EnvTokenFile, got)
		}
		if tok, err := TokenFromEnv(); err != nil || tok != "from-env" {
			t.Fatalf("TokenFromEnv = %q, %v, want the inline token", tok, err)
		}
	})

	t.Run("a selected but unreadable pointer fails closed", func(t *testing.T) {
		t.Setenv(EnvToken, "")
		t.Setenv(EnvTokenFile, filepath.Join(base, "missing", "tok"))
		if err := CanonicalizeTokenFileEnv(); err == nil {
			t.Fatal("a selected token file that cannot be resolved must error")
		}
	})

	t.Run("no pointer is a no-op", func(t *testing.T) {
		t.Setenv(EnvToken, "")
		t.Setenv(EnvTokenFile, "")
		if err := CanonicalizeTokenFileEnv(); err != nil {
			t.Fatalf("CanonicalizeTokenFileEnv without a pointer: %v", err)
		}
		if got := os.Getenv(EnvTokenFile); got != "" {
			t.Fatalf("%s = %q, want empty", EnvTokenFile, got)
		}
	})
}

func TestServerTLSConfigRequiresCertKey(t *testing.T) {
	if _, err := ServerTLSConfig("", ""); err == nil {
		t.Fatal("ServerTLSConfig must require a cert and key (TLS mandatory)")
	}
	if _, err := ServerTLSConfig("/nope/cert.pem", "/nope/key.pem"); err == nil {
		t.Fatal("ServerTLSConfig must error on unreadable cert/key")
	}
}
