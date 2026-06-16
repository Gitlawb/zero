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
}

func newAESGCMCrypter(secretPath string) *aesGCMCrypter {
	return &aesGCMCrypter{secretPath: secretPath}
}

// aead loads (or, when create is set, generates) the secret and returns the GCM
// AEAD. open passes create=false so a missing secret is a hard error rather than
// silently minting a new key that could never decrypt the existing file.
func (c *aesGCMCrypter) aead(create bool) (cipher.AEAD, error) {
	secret, err := loadOrCreateSecret(c.secretPath, create)
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
// the file is absent, it generates a random secret and creates the file
// exclusively (0600). A wrong-sized existing secret fails closed (corruption).
func loadOrCreateSecret(path string, create bool) ([]byte, error) {
	if data, err := readSecretFile(path); err == nil {
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
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
	// Create exclusively: an os.Rename publish would clobber on POSIX, so two
	// concurrent first-run processes could each generate a secret and the loser
	// would silently orphan the tokens it encrypts. O_EXCL lets exactly one
	// process win; everyone else adopts the winner's on-disk secret.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			// The winner may not have finished writing yet, so a read here can see a
			// short/absent file. Retry briefly so concurrent first-run invocations
			// converge on the winner's secret instead of failing transiently.
			var lastErr error
			for attempt := 0; attempt < 50; attempt++ {
				secret, rerr := readSecretFile(path)
				if rerr == nil {
					return secret, nil
				}
				lastErr = rerr
				time.Sleep(2 * time.Millisecond)
			}
			return nil, lastErr
		}
		return nil, fmt.Errorf("oauth: create token secret: %w", err)
	}
	if _, werr := f.Write(secret); werr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("oauth: write token secret: %w", werr)
	}
	if cerr := f.Close(); cerr != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("oauth: write token secret: %w", cerr)
	}
	return secret, nil
}

// readSecretFile reads and validates the secret at path, returning a wrapped
// os.ErrNotExist when it is absent so callers can branch on creation.
func readSecretFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("oauth: read token secret: %w", err)
	}
	if len(data) != secretBytes {
		return nil, fmt.Errorf("oauth: token secret at %s has unexpected size %d", path, len(data))
	}
	return data, nil
}
