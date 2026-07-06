// Package securefile provides AES-256-GCM encryption-at-rest for small on-disk
// blobs under a per-user random secret persisted (0600) beside the data file. The
// on-disk blob is nonce || ciphertext; GCM provides confidentiality AND tamper
// detection, so a corrupted/forged file fails closed on Open. It is a neutral,
// reusable extraction of the OAuth token store's encrypted-file backend, shared by
// the credential store (and available to other callers).
package securefile

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

const (
	// secretBytes is the AES-256 key length kept in the per-user secret file.
	secretBytes = 32

	secretRetryAttempts = 500
	secretRetryDelay    = 2 * time.Millisecond
)

var openSecretLockFile = os.OpenFile

// Crypter encrypts a blob at rest with AES-256-GCM under a per-user random secret
// persisted (0600) at secretPath.
type Crypter struct {
	secretPath string
}

// NewCrypter returns a Crypter whose key lives at secretPath (created on first
// Seal). Keep secretPath beside the data file (e.g. data + ".secret").
func NewCrypter(secretPath string) *Crypter {
	return &Crypter{secretPath: secretPath}
}

// aead loads (or, when create is set, generates) the secret and returns the GCM
// AEAD. Open passes create=false so a missing secret is a hard error rather than
// silently minting a new key that could never decrypt the existing file.
func (c *Crypter) aead(create bool) (cipher.AEAD, error) {
	secret, err := loadOrCreateSecret(c.secretPath, create)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(secret)
	if err != nil {
		return nil, fmt.Errorf("securefile: build cipher: %w", err)
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext, prefixing a fresh random nonce. It creates the secret
// on first use.
func (c *Crypter) Seal(plaintext []byte) ([]byte, error) {
	gcm, err := c.aead(true)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("securefile: generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Open decrypts a nonce||ciphertext blob, failing closed on a missing secret, a
// short blob, or a failed authentication tag (tampering / wrong key).
func (c *Crypter) Open(blob []byte) ([]byte, error) {
	gcm, err := c.aead(false)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, errors.New("securefile: encrypted file is too short")
	}
	nonce, ciphertext := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("securefile: decrypt (wrong secret or tampered): %w", err)
	}
	return plaintext, nil
}

// loadOrCreateSecret reads the 32-byte secret at path. When create is set and the
// file is absent, it generates a random secret and creates the file atomically
// (0600). A wrong-sized existing secret fails closed (corruption).
func loadOrCreateSecret(path string, create bool) ([]byte, error) {
	if data, err := readSecretFileRetry(path); err == nil {
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !create {
		return nil, fmt.Errorf("securefile: secret %s is missing; cannot decrypt the file", path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return createSecretFile(path)
}

func createSecretFile(path string) ([]byte, error) {
	lockPath := path + ".lock"
	var lastErr error
	for attempt := 0; attempt < secretRetryAttempts; attempt++ {
		lock, err := openSecretLockFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			_ = lock.Close()
			defer os.Remove(lockPath)
			if data, rerr := readSecretFileRetry(path); rerr == nil {
				return data, nil
			} else if !errors.Is(rerr, os.ErrNotExist) {
				return nil, rerr
			}
			return writeNewSecretFile(path)
		}
		// On Windows a concurrent holder's os.Remove leaves the lock file in a
		// "delete pending" state, so an O_EXCL create races it with
		// ERROR_ACCESS_DENIED (os.ErrPermission) rather than ErrExist. Treat that
		// as contention and retry too -- mirroring oauth's createSecretFile --
		// otherwise concurrent secret creation spuriously fails on Windows.
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("securefile: create secret lock: %w", err)
		}
		// Remember the lock-creation error itself rather than a subsequent
		// "secret file doesn't exist yet" read error -- otherwise a real
		// contention/ACL problem is masked by the expected-while-waiting
		// ErrNotExist once the retries are exhausted.
		lastErr = err
		if data, rerr := readSecretFileRetry(path); rerr == nil {
			return data, nil
		}
		time.Sleep(secretRetryDelay)
	}
	return nil, fmt.Errorf("securefile: timed out waiting for secret %s: %w", path, lastErr)
}

func writeNewSecretFile(path string) ([]byte, error) {
	secret := make([]byte, secretBytes)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		return nil, fmt.Errorf("securefile: generate secret: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*.tmp")
	if err != nil {
		return nil, fmt.Errorf("securefile: create secret temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("securefile: chmod secret temp file: %w", err)
	}
	if _, werr := tmp.Write(secret); werr != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("securefile: write secret: %w", werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		return nil, fmt.Errorf("securefile: write secret: %w", cerr)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return nil, fmt.Errorf("securefile: publish secret: %w", err)
	}
	return secret, nil
}

func readSecretFileRetry(path string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < secretRetryAttempts; attempt++ {
		data, err := readSecretFile(path)
		if !isTransientSecretAccessError(err) {
			return data, err
		}
		lastErr = err
		time.Sleep(secretRetryDelay)
	}
	return nil, fmt.Errorf("securefile: timed out waiting for secret %s: %w", path, lastErr)
}

// readSecretFile reads and validates the secret at path, returning a wrapped
// os.ErrNotExist when it is absent so callers can branch on creation.
func readSecretFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		return nil, fmt.Errorf("securefile: read secret: %w", err)
	}
	if len(data) != secretBytes {
		return nil, fmt.Errorf("securefile: secret at %s has unexpected size %d", path, len(data))
	}
	return data, nil
}
