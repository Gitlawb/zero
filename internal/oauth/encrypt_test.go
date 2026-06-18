package oauth

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestLoadOrCreateSecretConcurrentConverges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tok.json.secret")
	const n = 16
	secrets := make([][]byte, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			secrets[i], errs[i] = loadOrCreateSecret(path, true)
		}(i)
	}
	wg.Wait()
	// Exactly one creator wins; every racer must converge on the same on-disk
	// secret rather than reading a half-published file or orphaning its own.
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if !bytes.Equal(secrets[i], secrets[0]) {
			t.Fatalf("goroutine %d got a divergent secret; concurrent create did not converge", i)
		}
	}
}

func TestAESGCMCrypterRoundTripAndTamper(t *testing.T) {
	secretPath := filepath.Join(t.TempDir(), "tok.json.secret")
	c := newAESGCMCrypter(secretPath)
	plaintext := []byte(`{"schemaVersion":1,"tokens":{}}`)
	blob, err := c.seal(plaintext)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(blob, plaintext) {
		t.Fatal("sealed blob must not contain the plaintext")
	}
	got, err := c.open(blob)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("round-trip mismatch: %q", got)
	}
	// Tamper: flipping any byte breaks the GCM tag.
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := c.open(tampered); err == nil {
		t.Fatal("open must reject a tampered blob (GCM auth)")
	}
}

func TestLoadOrCreateSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tok.json.secret")
	// Missing + create=false => fail closed (can't decrypt without the secret).
	if _, err := loadOrCreateSecret(path, false); err == nil {
		t.Fatal("missing secret with create=false must error")
	}
	secret, err := loadOrCreateSecret(path, true)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(secret) != secretBytes {
		t.Fatalf("secret length = %d, want %d", len(secret), secretBytes)
	}
	// Stable across reads.
	again, err := loadOrCreateSecret(path, false)
	if err != nil || !bytes.Equal(again, secret) {
		t.Fatalf("secret not stable: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat secret: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("secret file mode = %o, want 600", perm)
		}
	}
	// Wrong-sized secret fails closed.
	if err := os.WriteFile(path, []byte("short"), 0o600); err != nil {
		t.Fatalf("corrupt secret: %v", err)
	}
	if _, err := loadOrCreateSecret(path, true); err == nil {
		t.Fatal("wrong-sized secret must error")
	}
}

func newEncryptedStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	s, err := NewStore(StoreOptions{FilePath: path, Encrypted: true})
	if err != nil {
		t.Fatalf("NewStore(encrypted): %v", err)
	}
	return s, path
}

// The unified Storage="encrypted-file" selector must encrypt at rest, the same as
// the legacy Encrypted:true alias (the file/keyring/encrypted-file merge).
func TestNewStoreEncryptedFileStorageSelector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oauth-tokens.json")
	s, err := NewStore(StoreOptions{FilePath: path, Storage: "encrypted-file"})
	if err != nil {
		t.Fatalf("NewStore(encrypted-file): %v", err)
	}
	if err := s.Save(ProviderKey("demo"), Token{AccessToken: "super-secret-access"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "super-secret-access") || strings.Contains(string(raw), "schemaVersion") {
		t.Fatalf("Storage=encrypted-file did not encrypt at rest:\n%s", raw)
	}
	got, ok, err := s.Load(ProviderKey("demo"))
	if err != nil || !ok || got.AccessToken != "super-secret-access" {
		t.Fatalf("Load = %+v ok=%v err=%v", got, ok, err)
	}
}

func TestEncryptedStoreRoundTripAndCiphertextOnDisk(t *testing.T) {
	s, path := newEncryptedStore(t)
	tok := Token{AccessToken: "super-secret-access", RefreshToken: "super-secret-refresh", Account: "me@x"}
	if err := s.Save(ProviderKey("demo"), tok); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// On-disk file must be ciphertext: not valid JSON, no plaintext token.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if strings.Contains(string(raw), "super-secret-access") || strings.Contains(string(raw), "schemaVersion") {
		t.Fatalf("token file is not encrypted at rest:\n%s", raw)
	}
	// The secret file exists beside it.
	if _, err := os.Stat(path + ".secret"); err != nil {
		t.Fatalf("secret file missing: %v", err)
	}
	// A fresh Store (same path) decrypts and reads it back.
	s2, err := NewStore(StoreOptions{FilePath: path, Encrypted: true})
	if err != nil {
		t.Fatalf("NewStore 2: %v", err)
	}
	got, ok, err := s2.Load(ProviderKey("demo"))
	if err != nil || !ok || got.AccessToken != "super-secret-access" || got.Account != "me@x" {
		t.Fatalf("Load = %+v ok=%v err=%v", got, ok, err)
	}
	// Delete + Status work through the encrypted backend too.
	statuses, err := s2.Status(KeyPrefixProvider)
	if err != nil || len(statuses) != 1 {
		t.Fatalf("Status = %+v err=%v", statuses, err)
	}
	if removed, err := s2.Delete(ProviderKey("demo")); err != nil || !removed {
		t.Fatalf("Delete = %v %v", removed, err)
	}
}

func TestEncryptedStoreTamperFailsClosed(t *testing.T) {
	s, path := newEncryptedStore(t)
	if err := s.Save(ProviderKey("demo"), Token{AccessToken: "a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("encrypted token file is empty")
	}
	raw[len(raw)-1] ^= 0xff
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if _, _, err := s.Load(ProviderKey("demo")); err == nil {
		t.Fatal("a tampered encrypted store must fail closed")
	}
}

func TestEncryptedStoreMissingSecretFailsClosed(t *testing.T) {
	s, path := newEncryptedStore(t)
	if err := s.Save(ProviderKey("demo"), Token{AccessToken: "a"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Remove(path + ".secret"); err != nil {
		t.Fatalf("remove secret: %v", err)
	}
	if _, _, err := s.Load(ProviderKey("demo")); err == nil {
		t.Fatal("missing secret must fail closed, not return empty")
	}
}
