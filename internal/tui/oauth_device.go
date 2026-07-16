package tui

import (
	"context"
	"crypto/sha256"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Gitlawb/zero/internal/browser"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/oauth"
	"github.com/Gitlawb/zero/internal/provideroauth"
)

type oauthDiscoveryCredentialResolver struct {
	mu              sync.Mutex
	copilot         *provideroauth.CopilotTokenSource
	githubTokenHash [sha256.Size]byte
}

func newOAuthDiscoveryCredentialResolver() *oauthDiscoveryCredentialResolver {
	return &oauthDiscoveryCredentialResolver{
		copilot: &provideroauth.CopilotTokenSource{
			HTTPClient: &http.Client{Timeout: 30 * time.Second},
			GitHubToken: func(ctx context.Context) (string, error) {
				token := oauthStoredToken(ctx, "copilot")
				if token == "" {
					return "", oauth.ErrNoToken
				}
				return token, nil
			},
		},
	}
}

// resolve returns the bearer token and any extra request
// headers needed to authenticate a /models discovery probe for an OAuth
// provider. For GitHub Copilot the stored login is a durable GitHub user token
// that api.githubcopilot.com does NOT accept directly — and even when it did,
// GitHub only exposes a limited (GPT-only) model set to it. Exchange it for the
// short-lived Copilot token and attach the editor headers so discovery sees the
// full Copilot catalog (Claude, Gemini, GPT-5.x, …), matching the chat request
// path. If the exchange fails, fall back to the raw stored token so discovery
// can still surface the limited set rather than nothing. Other providers use
// their stored bearer as-is with no extra headers.
//
// The returned baseURL is the account-specific Copilot API host derived from the
// minted token; callers set it on the discovery profile so probes hit the same
// host the model calls will. It is empty for non-Copilot providers and when only
// the raw stored token is available.
func (r *oauthDiscoveryCredentialResolver) resolve(ctx context.Context, providerID string) (string, map[string]string, string) {
	stored := oauthStoredToken(ctx, providerID)
	if stored == "" {
		return "", nil, ""
	}
	if strings.EqualFold(strings.TrimSpace(providerID), "copilot") {
		if token, err := r.copilotSource(stored).Bearer(ctx, false); err == nil && token != "" {
			return token, provideroauth.CopilotChatHeaderMap(), provideroauth.CopilotBaseURLFromToken(token)
		}
		return stored, nil, ""
	}

	return stored, nil, ""
}

func (r *oauthDiscoveryCredentialResolver) copilotSource(githubToken string) *provideroauth.CopilotTokenSource {
	r.mu.Lock()
	defer r.mu.Unlock()
	tokenHash := sha256.Sum256([]byte(strings.TrimSpace(githubToken)))
	if r.githubTokenHash == tokenHash {
		return r.copilot
	}
	if r.githubTokenHash != ([sha256.Size]byte{}) {
		provideroauth.InvalidateCopilotModelCache()
	}
	client := r.copilot.HTTPClient
	r.copilot = &provideroauth.CopilotTokenSource{
		HTTPClient: client,
		GitHubToken: func(ctx context.Context) (string, error) {
			token := oauthStoredToken(ctx, "copilot")
			if token == "" {
				return "", oauth.ErrNoToken
			}
			return token, nil
		},
	}
	r.githubTokenHash = tokenHash
	return r.copilot
}

func oauthDiscoveryCredential(ctx context.Context, providerID string) (string, map[string]string, string) {
	return newOAuthDiscoveryCredentialResolver().resolve(ctx, providerID)
}

func (m model) resolveDiscoveryProfile(ctx context.Context, providerID string, needOAuth bool, profile config.ProviderProfile) config.ProviderProfile {
	if !needOAuth {
		return profile
	}
	resolve := m.resolveOAuthDiscovery
	if resolve == nil {
		resolve = oauthDiscoveryCredential
	}
	token, headers, baseURL := resolve(ctx, providerID)
	if token == "" {
		return profile
	}
	profile.APIKey = token
	if len(headers) > 0 {
		profile.CustomHeaders = headers
	}
	if baseURL != "" {
		profile.BaseURL = baseURL
	}
	return profile
}

// oauthPreferDeviceFlow reports whether the device-code flow should be the
// default for a device-capable provider because no usable browser is likely
// present (SSH session or a headless Linux box). On a desktop the browser flow
// stays the default; users can still force device code with the "d" shortcut.
// ZERO_OAUTH_DEVICE forces it on for any environment.
func oauthPreferDeviceFlow() bool {
	if strings.TrimSpace(os.Getenv("ZERO_OAUTH_DEVICE")) != "" {
		return true
	}
	if strings.TrimSpace(os.Getenv("SSH_CONNECTION")) != "" || strings.TrimSpace(os.Getenv("SSH_TTY")) != "" {
		return true
	}
	if runtime.GOOS == "linux" &&
		strings.TrimSpace(os.Getenv("DISPLAY")) == "" &&
		strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) == "" {
		return true
	}
	return false
}

// oauthDevicePrepare requests an RFC 8628 device code for the provider (phase 1).
// The returned DeviceAuth carries the verification URI + user code to display;
// pass cfg/auth to oauthDeviceComplete to poll for the token.
func oauthDevicePrepare(name string) (oauth.DeviceAuth, oauth.Config, error) {
	store, err := oauth.NewStore(oauth.StoreOptions{})
	if err != nil {
		return oauth.DeviceAuth{}, oauth.Config{}, err
	}
	manager, err := oauth.NewManager(oauth.ManagerOptions{
		Store:       store,
		HTTPClient:  &http.Client{Timeout: 60 * time.Second},
		OpenBrowser: browser.OpenURL,
		// Device-flow providers (e.g. Hugging Face) rely on the baked-in preset for
		// their endpoints; opt in so the device code can be requested.
		AllowPresets: true,
	})
	if err != nil {
		return oauth.DeviceAuth{}, oauth.Config{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return manager.PrepareDeviceLogin(ctx, oauth.LoginOptions{Provider: name})
}

// oauthDeviceComplete polls for the token authorized via oauthDevicePrepare and
// stores it under provider:<name> (phase 2). The runtime resolver then attaches
// the refreshable token to model calls.
func oauthDeviceComplete(name string, cfg oauth.Config, auth oauth.DeviceAuth) error {
	store, err := oauth.NewStore(oauth.StoreOptions{})
	if err != nil {
		return err
	}
	manager, err := oauth.NewManager(oauth.ManagerOptions{
		Store:        store,
		HTTPClient:   &http.Client{Timeout: 60 * time.Second},
		AllowPresets: true, // preset config is needed to poll/exchange the device token
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	_, err = manager.CompleteDeviceLogin(ctx, name, cfg, auth)
	return err
}

// oauthStoredToken returns a fresh access token for a provider that was logged in
// via OAuth (token stored under provider:<id>), refreshing on demand. Empty when
// there is no stored login or the refresh fails. Used to authenticate the model
// discovery /models call so the wizard can show the live model list after login.
func oauthStoredToken(ctx context.Context, providerID string) string {
	store, err := oauth.NewStore(oauth.StoreOptions{})
	if err != nil {
		return ""
	}
	manager, err := oauth.NewManager(oauth.ManagerOptions{
		Store:        store,
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		AllowPresets: true, // refreshing a preset-provider token re-resolves its config
	})
	if err != nil {
		return ""
	}
	token, err := manager.GetFresh(ctx, oauth.ProviderKey(providerID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(token)
}

// oauthDeviceVerifyTarget picks the best URL to show the user: the complete URI
// (code pre-filled) when present, else the bare verification URI.
func oauthDeviceVerifyTarget(auth oauth.DeviceAuth) string {
	if target := strings.TrimSpace(auth.VerificationURIComplete); target != "" {
		return target
	}
	return strings.TrimSpace(auth.VerificationURI)
}
