package oauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
)

const (
	// pkceVerifierBytes yields a 43-character base64url verifier — the minimum
	// length the PKCE spec permits while remaining high-entropy.
	pkceVerifierBytes = 32
	stateBytes        = 32
	// MethodS256 is the only code-challenge method this engine accepts.
	MethodS256 = "S256"
)

// PKCE is a verifier/challenge pair for an authorization-code flow. The method
// is always S256 (the engine refuses "plain").
type PKCE struct {
	Verifier  string
	Challenge string
	Method    string
}

// NewPKCE generates a high-entropy code verifier and its S256 challenge. It
// matches internal/mcp's newPKCE byte-for-byte (RawURLEncoding of 32 random
// bytes; challenge = base64url(SHA-256(verifier))) so the two engines are
// interchangeable.
func NewPKCE() (PKCE, error) {
	raw := make([]byte, pkceVerifierBytes)
	if _, err := rand.Read(raw); err != nil {
		return PKCE{}, fmt.Errorf("oauth: generate PKCE verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	return PKCE{Verifier: verifier, Challenge: challenge, Method: MethodS256}, nil
}

// NewState generates a high-entropy CSRF state value.
func NewState() (string, error) {
	raw := make([]byte, stateBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("oauth: generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
