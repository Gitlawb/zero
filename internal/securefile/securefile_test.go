package securefile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "k.secret")
	c := NewCrypter(secret)
	plain := []byte(`{"openai":{"type":"api","key":"sk-test-123"}}`)
	blob, err := c.Seal(plain)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if string(blob) == string(plain) {
		t.Fatal("blob must not equal plaintext")
	}
	// The secret file is created 0600.
	info, err := os.Stat(secret)
	if err != nil {
		t.Fatalf("secret not created: %v", err)
	}
	// Windows doesn't honor Unix permission bits (Chmod only toggles read-only), so
	// the 0600 guarantee only holds on Unix.
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("secret perm = %o, want 600", perm)
		}
	}
	got, err := c.Open(blob)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if string(got) != string(plain) {
		t.Fatalf("round-trip = %q, want %q", got, plain)
	}
}

func TestOpenTamperedFailsClosed(t *testing.T) {
	c := NewCrypter(filepath.Join(t.TempDir(), "k.secret"))
	blob, err := c.Seal([]byte("secret-data"))
	if err != nil {
		t.Fatal(err)
	}
	blob[len(blob)-1] ^= 0xff // flip a ciphertext bit
	if _, err := c.Open(blob); err == nil {
		t.Fatal("expected tampered blob to fail authentication")
	}
}

func TestOpenMissingSecretFailsClosed(t *testing.T) {
	c := NewCrypter(filepath.Join(t.TempDir(), "absent.secret"))
	if _, err := c.Open([]byte("0123456789012")); err == nil {
		t.Fatal("expected open with no secret to fail, not mint a new key")
	}
}

func TestOpenShortBlobFails(t *testing.T) {
	c := NewCrypter(filepath.Join(t.TempDir(), "k.secret"))
	if _, err := c.Seal([]byte("x")); err != nil { // create the secret
		t.Fatal(err)
	}
	if _, err := c.Open([]byte("short")); err == nil {
		t.Fatal("expected too-short blob to fail")
	}
}

func TestWrongSecretCannotDecrypt(t *testing.T) {
	dir := t.TempDir()
	a := NewCrypter(filepath.Join(dir, "a.secret"))
	blob, err := a.Seal([]byte("data"))
	if err != nil {
		t.Fatal(err)
	}
	b := NewCrypter(filepath.Join(dir, "b.secret")) // different secret
	if _, err := b.Open(blob); err == nil {
		t.Fatal("expected decryption under a different secret to fail")
	}
}
