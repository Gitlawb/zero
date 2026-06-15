package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// defaultDeviceCodeLifetime bounds a device authorization that omits expires_in,
// so the expiry gate in PollDeviceToken is always active (fail closed).
const defaultDeviceCodeLifetime = 10 * time.Minute

// DeviceAuth is the result of an RFC 8628 device-authorization request.
type DeviceAuth struct {
	DeviceCode              string
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	Interval                time.Duration
	ExpiresAt               time.Time
}

type deviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
	Error                   string `json:"error"`
	ErrorDescription        string `json:"error_description"`
}

// RequestDeviceCode performs the RFC 8628 device-authorization request. This is
// the headless/SSH login path (no browser, no loopback).
func RequestDeviceCode(ctx context.Context, client *http.Client, cfg Config, now func() time.Time) (DeviceAuth, error) {
	endpoint := trimmed(cfg.DeviceAuthorizationEndpoint)
	if endpoint == "" {
		return DeviceAuth{}, errors.New("oauth: provider has no device authorization endpoint")
	}
	if err := validateTokenEndpoint(endpoint); err != nil {
		return DeviceAuth{}, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	if now == nil {
		now = time.Now
	}
	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	if secret := trimmed(cfg.ClientSecret); secret != "" {
		// Confidential clients must authenticate on the device endpoint too,
		// consistent with the token poll (pollDeviceOnce).
		form.Set("client_secret", secret)
	}
	if len(cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(cfg.Scopes, " "))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceAuth{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return DeviceAuth{}, fmt.Errorf("oauth: device authorization request failed: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, tokenResponseLimit))
	var parsed deviceAuthResponse
	if len(body) > 0 {
		_ = json.Unmarshal(body, &parsed)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		if parsed.Error != "" {
			return DeviceAuth{}, fmt.Errorf("oauth: device authorization error %q", parsed.Error)
		}
		return DeviceAuth{}, fmt.Errorf("oauth: device authorization returned HTTP %d", response.StatusCode)
	}
	if trimmed(parsed.DeviceCode) == "" || trimmed(parsed.UserCode) == "" {
		return DeviceAuth{}, errors.New("oauth: device authorization response missing device_code/user_code")
	}
	interval := time.Duration(parsed.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second // RFC 8628 default
	}
	auth := DeviceAuth{
		DeviceCode:              parsed.DeviceCode,
		UserCode:                parsed.UserCode,
		VerificationURI:         parsed.VerificationURI,
		VerificationURIComplete: parsed.VerificationURIComplete,
		Interval:                interval,
	}
	lifetime := time.Duration(parsed.ExpiresIn) * time.Second
	if lifetime <= 0 {
		// Fail closed: a response without expires_in must still get a bounded
		// lifetime so the poll loop's expiry gate stays active.
		lifetime = defaultDeviceCodeLifetime
	}
	auth.ExpiresAt = now().Add(lifetime)
	return auth, nil
}

// PollDeviceToken polls the token endpoint for the device grant until the user
// approves, the device code expires, ctx is done, or a terminal error occurs. It
// honors authorization_pending (keep waiting), slow_down (increase the
// interval), and expired_token.
func PollDeviceToken(ctx context.Context, client *http.Client, cfg Config, auth DeviceAuth, now func() time.Time) (Token, error) {
	if now == nil {
		now = time.Now
	}
	interval := auth.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if !auth.ExpiresAt.IsZero() && !auth.ExpiresAt.After(now()) {
			return Token{}, errors.New("oauth: device code expired before authorization")
		}
		select {
		case <-ctx.Done():
			return Token{}, fmt.Errorf("oauth: device authorization canceled: %w", ctx.Err())
		case <-time.After(interval):
		}
		// Re-check expiry after sleeping: slow_down (or a long interval) can push
		// the next poll past ExpiresAt, and we must not poll after the deadline.
		if !auth.ExpiresAt.IsZero() && !auth.ExpiresAt.After(now()) {
			return Token{}, errors.New("oauth: device code expired before authorization")
		}
		token, err := pollDeviceOnce(ctx, client, cfg, auth.DeviceCode, now)
		switch {
		case err == nil:
			return token, nil
		case errors.Is(err, ErrAuthorizationPending):
			// keep waiting at the current interval
		case errors.Is(err, ErrSlowDown):
			interval += 5 * time.Second // RFC 8628: add 5s on slow_down
		default:
			return Token{}, err
		}
	}
}

// pollDeviceOnce makes a single device-token poll, translating the RFC 8628
// error codes into ErrAuthorizationPending / ErrSlowDown / terminal errors.
func pollDeviceOnce(ctx context.Context, client *http.Client, cfg Config, deviceCode string, now func() time.Time) (Token, error) {
	if err := validateTokenEndpoint(cfg.TokenEndpoint); err != nil {
		return Token{}, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	form := url.Values{}
	form.Set("grant_type", deviceGrantType)
	form.Set("device_code", deviceCode)
	form.Set("client_id", cfg.ClientID)
	if secret := trimmed(cfg.ClientSecret); secret != "" {
		form.Set("client_secret", secret)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, trimmed(cfg.TokenEndpoint), strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return Token{}, fmt.Errorf("oauth: device token poll failed: %w", err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, tokenResponseLimit))
	var parsed tokenResponse
	if len(body) > 0 {
		_ = json.Unmarshal(body, &parsed)
	}
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices && trimmed(parsed.AccessToken) != "" {
		token := Token{Scopes: cfg.Scopes}
		token.AccessToken = parsed.AccessToken
		token.RefreshToken = parsed.RefreshToken
		token.TokenType = parsed.TokenType
		if parsed.ExpiresIn > 0 {
			token.ExpiresAt = now().Add(time.Duration(parsed.ExpiresIn) * time.Second).UTC()
		}
		if scope := trimmed(parsed.Scope); scope != "" {
			token.Scopes = strings.Fields(scope)
		}
		return token, nil
	}
	switch parsed.Error {
	case "authorization_pending":
		return Token{}, ErrAuthorizationPending
	case "slow_down":
		return Token{}, ErrSlowDown
	case "expired_token":
		return Token{}, errors.New("oauth: device code expired before authorization")
	case "access_denied":
		return Token{}, errors.New("oauth: authorization denied by the user")
	case "":
		return Token{}, fmt.Errorf("oauth: device token poll returned HTTP %d", response.StatusCode)
	default:
		return Token{}, fmt.Errorf("oauth: device token error %q", parsed.Error)
	}
}
