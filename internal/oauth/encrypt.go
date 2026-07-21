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

const (
	// secretBytes is the AES-256 key length kept in the per-user secret file.
	secretBytes = 32

	secretRetryAttempts = 500
	secretRetryDelay    = 2 * time.Millisecond
)

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
// atomically (0600). A wrong-sized existing secret fails closed (corruption).
func loadOrCreateSecret(path string, create bool) ([]byte, error) {
	if data, err := readSecretFileRetry(path); err == nil {
		return data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !create {
		return nil, fmt.Errorf("oauth: token secret %s is missing; cannot decrypt the token file", path)
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
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
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
		// as contention and retry too — mirroring acquireFileLock in lock.go —
		// otherwise concurrent secret creation spuriously fails on Windows.
		if !errors.Is(err, os.ErrExist) && !errors.Is(err, os.ErrPermission) {
			return nil, fmt.Errorf("oauth: create token secret lock: %w", err)
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
	return nil, fmt.Errorf("oauth: timed out waiting for token secret %s: %w", path, lastErr)
}

func writeNewSecretFile(path string) ([]byte, error) {
	secret := make([]byte, secretBytes)
	if _, err := io.ReadFull(rand.Reader, secret); err != nil {
		return nil, fmt.Errorf("oauth: generate token secret: %w", err)
	}
	tmpPath := path + ".tmp"
	_ = os.Remove(tmpPath)
	tmp, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("oauth: create token secret temp file: %w", err)
	}
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("oauth: chmod token secret temp file: %w", err)
	}
	if _, werr := tmp.Write(secret); werr != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("oauth: write token secret: %w", werr)
	}
	if cerr := tmp.Close(); cerr != nil {
		return nil, fmt.Errorf("oauth: write token secret: %w", cerr)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return nil, fmt.Errorf("oauth: publish token secret: %w", err)
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
	return nil, fmt.Errorf("oauth: timed out waiting for token secret %s: %w", path, lastErr)
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
