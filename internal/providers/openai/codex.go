package openai

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Codex-specific headers, lifted from the openai/codex CLI's behavior. The
// Codex backend at https://chatgpt.com/backend-api/codex requires all three on
// every request — the bearer, the `originator` that identifies the client, and
// the `chatgpt-account-id` claim that ties the bearer to a specific ChatGPT
// subscription. Drop any one and the request 401s at Cloudflare.
const (
	codexDefaultOriginator = "codex_cli_rs"
	codexAccountHeader     = "chatgpt-account-id"
	codexOriginatorHeader  = "originator"
)

// CodexAccountResolver returns the `chatgpt_account_id` claim for the bearer
// that is about to be sent on a request. It is invoked once per request
// (including the 401-refresh retry) so the value can be re-derived from the
// live OAuth token rather than cached at construction time.
//
// ok=false means "no account id known" — the Codex provider simply omits the
// header in that case (the request will 401, but that's recoverable: the user
// re-auths and the next login persists a fresh id).
type CodexAccountResolver func(ctx context.Context) (accountID string, ok bool, err error)

// CodexOptions configures a Codex-flavored provider. It embeds the standard
// openai.Options so every chat-completions knob (model, custom headers,
// MaxTokens, parse-think-tags, etc.) is supported unchanged. The Codex-specific
// fields below add the headers the Codex backend requires.
type CodexOptions struct {
	Options
	// Originator is the value of the `originator` header. Empty defaults to
	// "codex_cli_rs" (the same value the openai/codex CLI ships). The Codex
	// backend reads this to attribute traffic; changing it is supported but
	// unusual.
	Originator string
	// UserAgent overrides the openai Options.UserAgent when non-empty. The
	// Codex backend logs the User-Agent for diagnostics, so a "codex_cli_rs"
	// / "zero" branded value is recommended.
	UserAgent string
	// AccountID is a static `chatgpt-account-id` that bypasses the resolver.
	// Leave empty in production wiring so the AccountResolver is consulted on
	// every request — that path reads the live OAuth token from the store and
	// survives a refresh that rotates the bearer (and its account claim).
	// The field exists for tests that want a pinned value without standing up
	// a resolver.
	AccountID string
	// AccountResolver, when set, returns the account id dynamically per
	// request (including the 401-refresh retry). The factory wires this so a
	// refresh that updates the stored token's Account field takes effect on
	// the next outgoing request without restarting the agent.
	//
	// ok=false means "no account id known" — the Codex provider simply omits
	// the header in that case (the request will 401, but that's recoverable:
	// the user re-auths and the next login persists a fresh id).
	AccountResolver CodexAccountResolver
	// RequestTimeout caps each outbound Codex request. 0 => 60s. The Codex
	// backend is hosted behind Cloudflare, so a few seconds is plenty for a
	// healthy connection; the cap is a safety net for the rare case the
	// request hangs past the streaming idle watchdog.
	RequestTimeout time.Duration
}

// CodexProvider is the Codex-flavored variant of the openai provider. It is
// a thin shim that adds the Codex-specific request headers
// (`originator`, `chatgpt-account-id`, branded `User-Agent`) on top of the
// generic OpenAI chat-completions transport. The Codex backend speaks the
// Responses API at `{baseURL}/responses` (not `/chat/completions`), so the
// constructor overrides the endpoint the wrapped openai provider would
// otherwise default to. The request body is byte-for-byte the same
// chat-completions shape today; the path difference is the only divergence.
type CodexProvider struct {
	inner          *Provider
	originator     string
	userAgent      string
	accountID      string
	accountResolve CodexAccountResolver
}

// NewCodexProvider builds a CodexProvider. It is a thin wrapper over the
// openai.New constructor plus the Codex-specific Options.SetRequestExtra
// callback that injects the Codex headers.
func NewCodexProvider(options CodexOptions) (*CodexProvider, error) {
	originator := strings.TrimSpace(options.Originator)
	if originator == "" {
		originator = codexDefaultOriginator
	}
	userAgent := strings.TrimSpace(options.UserAgent)
	if userAgent == "" {
		// Default to the openai Options.UserAgent (typically "zero/<ver>")
		// and fall back to a Codex-branded value when the caller didn't set
		// either — the Codex backend logs the User-Agent and a clearly
		// branded string makes operational issues easier to triage.
		userAgent = strings.TrimSpace(options.Options.UserAgent)
		if userAgent == "" {
			userAgent = codexDefaultOriginator
		}
	}

	// Reuse the openai provider's transport. Embed Options so the openai
	// constructor sees the full struct; here we set SetRequestExtra below.
	openaiOpts := options.Options
	openaiOpts.UserAgent = userAgent
	// The Codex backend serves the Responses API at `{baseURL}/responses`,
	// not `/chat/completions`. Override the endpoint the openai transport
	// would otherwise default to so the Codex provider hits the right path.
	// (The chat-completions request body is still accepted; only the path
	// diverges.)
	if baseURL := strings.TrimRight(strings.TrimSpace(openaiOpts.BaseURL), "/"); baseURL != "" {
		openaiOpts.Endpoint = baseURL + "/responses"
	}

	provider := &CodexProvider{
		originator:     originator,
		userAgent:      userAgent,
		accountID:      strings.TrimSpace(options.AccountID),
		accountResolve: options.AccountResolver,
	}
	openaiOpts.SetRequestExtra = provider.injectCodexHeaders
	inner, err := New(openaiOpts)
	if err != nil {
		return nil, fmt.Errorf("openai codex provider: %w", err)
	}
	provider.inner = inner
	return provider, nil
}

// StreamCompletion forwards to the wrapped openai provider. The Codex headers
// are injected on every request via the SetRequestExtra callback.
func (p *CodexProvider) StreamCompletion(ctx context.Context, request zeroruntime.CompletionRequest) (<-chan zeroruntime.StreamEvent, error) {
	return p.inner.StreamCompletion(ctx, request)
}

// injectCodexHeaders is the SetRequestExtra callback installed on the wrapped
// openai provider. It sets the three Codex-required headers; the bearer is
// applied separately by the openai provider's auth path.
func (p *CodexProvider) injectCodexHeaders(req *http.Request) {
	req.Header.Set(codexOriginatorHeader, p.originator)
	if account, ok, err := p.resolveAccount(req.Context()); err == nil && ok && account != "" {
		req.Header.Set(codexAccountHeader, account)
	}
	// Branded User-Agent overrides the openai provider's default. Set last
	// so a caller that supplies a different UserAgent in custom-headers is
	// still respected (the openai provider's setExtra already ran before us).
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
}

// resolveAccount returns the account id to inject, preferring the static
// AccountID (set at construction from the OAuth token) and falling back to the
// per-request AccountResolver. ok=false means "omit the header".
func (p *CodexProvider) resolveAccount(ctx context.Context) (string, bool, error) {
	if p.accountID != "" {
		return p.accountID, true, nil
	}
	if p.accountResolve != nil {
		account, ok, err := p.accountResolve(ctx)
		if err != nil {
			return "", false, err
		}
		return account, ok, nil
	}
	return "", false, nil
}

// ValidateAccount is a convenience for tests/callers that want to confirm the
// account id is the right shape (non-empty, trimmed). It is a no-op helper
// rather than a constructor check so a Codex provider can be built before the
// first login completes.
func ValidateAccount(account string) error {
	if strings.TrimSpace(account) == "" {
		return errors.New("openai codex: account id is empty")
	}
	return nil
}
