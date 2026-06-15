package oauth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// secretBytes is the AES-256 key length kept in the per-user secret file.
const secretBytes = 32

// aesGCMCrypter encrypts the token file at rest with AES-256-GCM under a
// per-user random secret persisted (0600) beside the token file. The on-disk
// blob is nonce || ciphertext; GCM provides confidentiality AND tamper
// detection, so a corrupted/forged file fails closed on open. This is the
// opt-in "encrypted-file" storage backend; the default backend writes the
// 0600 plaintext JSON unchanged.
type aesGCMCrypter struct {
	secretPath string
	now        func() time.Time
}

func newAESGCMCrypter(secretPath string, now func() time.Time) *aesGCMCrypter {
	if now == nil {
		now = time.Now
	}
	return &aesGCMCrypter{secretPath: secretPath, now: now}
}

// aead loads (or, when create is set, generates) the secret and returns the GCM
// AEAD. open passes create=false so a missing secret is a hard error rather than
// silently minting a new key that could never decrypt the existing file.
func (c *aesGCMCrypter) aead(create bool) (cipher.AEAD, error) {
	secret, err := loadOrCreateSecret(c.secretPath, create, c.now)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, fmt.Errorf("oauth: build cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// seal encrypts plaintext, prefixing a fresh random nonce. It creates the secret
// on first use.
func (c *aesGCMCrypter) seal(plaintext []byte) ([]byte, error) {
	gcm, err := c.aead(true)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("oauth: generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// open decrypts a nonce||ciphertext blob, failing closed on a missing secret,
// a short blob, or a failed authentication tag (tampering / wrong key).
func (c *aesGCMCrypter) open(blob []byte) ([]byte, error) {
	gcm, err := c.aead(false)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, errors.New("oauth: encrypted token file is too short")
	}
	nonce, ciphertext := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("oauth: decrypt token file (wrong secret or tampered): %w", err)
	}
	return plaintext, nil
}

// loadOrCreateSecret reads the 32-byte secret at path. When create is set and
// the file is absent, it generates a random secret and writes it atomically
// 0600. A wrong-sized existing secret fails closed (corruption).
func loadOrCreateSecret(path string, create bool, now func() time.Time) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		if len(data) != secretBytes {
			return nil, fmt.Errorf("oauth: token secret at %s has unexpected size %d", path, len(data))
		}
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("oauth: read token secret: %w", err)
	}
	if !create {
		return nil, fmt.Errorf("oauth: token secret %s is missing; cannot decrypt the token file", path)
	}
	secret := make([]byte, secretBytes)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		return nil, fmt.Errorf("oauth: generate token secret: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	tmp := fmt.Sprintf("%s.tmp-%d-%d", path, os.Getpid(), now().UnixNano())
	if err := os.WriteFile(tmp, secret, 0o600); err != nil {
		return nil, fmt.Errorf("oauth: write token secret: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("oauth: commit token secret: %w", err)
	}
	return secret, nil
}
