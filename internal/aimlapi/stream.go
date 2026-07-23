package aimlapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// StreamTopUpOptions drives a session-based partner-checkout top-up for the
// interactive (TUI) onboarding. The caller already holds a user session —
// obtained from an email-code sign-in (Path B existing) or a
// passwordless new account (Path B sign-up) — so this skips authentication and
// runs create-session → pay → wait-for-payment, optionally exchanging the paid
// session into a fresh key (new accounts) versus keeping the caller's key.
type StreamTopUpOptions struct {
	SessionToken string // user session bearer (required)
	AmountUSD    string // dollars; parsed via ParseAmountUSD (min $20)
	// InferenceBaseURL pins key-bound billing and the returned provider profile to
	// the endpoint that validated the credential. Empty uses ResolveEndpoints.
	InferenceBaseURL string
	Method           PaymentMethod
	AutoTopUp        bool // enroll the account in automatic top-up at checkout time
	Exchange         bool // exchange the paid session into a new key (new accounts)
	Model            string
	PartnerID        string
	PartnerName      string
	NoOpen           bool

	// ResumeSessionToken, when set, resumes that partner-checkout session instead
	// of opening a new one, so a retry after an ambiguous failure (a lost Pay/
	// Exchange response) can never double-charge or strand an already-paid session.
	ResumeSessionToken string
	// PaymentSessionID is generated once per amount/auto-top-up intent and reused
	// on retry so an ambiguous Pay response cannot create a second checkout.
	PaymentSessionID string

	HTTPClient  *http.Client
	OpenBrowser func(string) error
	OnStatus    func(Status, string)
	// OnSession reports the partner-checkout session token the moment it exists (a
	// fresh CreateSession, or the resumed one), so the caller can retain it and
	// resume on a later failure. It fires with "" when the session is dead and the
	// retained token should be dropped (a fresh run must start over).
	OnSession func(sessionToken string)
}

// topupPhase is where StreamTopUp enters its create-checkout → poll → exchange
// pipeline. A fresh run starts at phasePay; a resume skips ahead to whatever the
// existing session's status makes safe.
type topupPhase int

const (
	phasePay          topupPhase = iota // (re-)open the Stripe checkout, then poll + exchange
	phasePoll                           // checkout already open; poll for payment, never re-pay
	phaseExchange                       // already paid; exchange only (recovers a lost key)
	phaseWaitExchange                   // an exchange claim is in flight; poll, never call Exchange twice
)

// StreamTopUp performs the browser-checkout top-up and reports progress via
// OnStatus.
func StreamTopUp(ctx context.Context, options StreamTopUpOptions) (ProvisionedKey, error) {
	endpoints := ResolveEndpoints()
	if baseURL := strings.TrimSpace(options.InferenceBaseURL); baseURL != "" {
		endpoints.InferenceBaseURL = baseURL
	}
	client := NewClient(endpoints, options.HTTPClient)
	partnerID := ResolvePartnerID(options.PartnerID)
	partnerName := firstNonEmpty(options.PartnerName, DefaultPartnerName)
	method := options.Method
	if method != PaymentMethodCrypto {
		method = PaymentMethodCard
	}
	amount, err := ParseAmountUSD(options.AmountUSD)
	if err != nil {
		return ProvisionedKey{}, err
	}
	if strings.TrimSpace(options.SessionToken) == "" {
		return ProvisionedKey{}, fmt.Errorf("a session is required to top up")
	}
	if strings.TrimSpace(options.PaymentSessionID) == "" {
		return ProvisionedKey{}, fmt.Errorf("a payment session id is required to top up")
	}

	status(options.OnStatus, StatusCreatingSession, "")
	sessionToken, phase, err := resolveTopupSession(ctx, client, options, partnerID, partnerName, endpoints)
	if err != nil {
		return ProvisionedKey{}, err
	}
	// Retain the live token so an ambiguous failure below can resume this session
	// rather than open a second checkout on the next attempt.
	reportSession(options.OnSession, sessionToken)

	if phase <= phasePay {
		successURL, cancelURL := BuildPartnerCheckoutReturnURLs(endpoints.PayBaseURL, sessionToken)
		status(options.OnStatus, StatusOpeningCheckout, "")
		pay, err := client.Pay(ctx, options.SessionToken, sessionToken, amount, options.PaymentSessionID, method, successURL, cancelURL, options.AutoTopUp)
		if err != nil {
			return ProvisionedKey{}, err
		}
		checkoutURL, err := validatedCheckoutURL(pay.Checkout.PayURL)
		if err != nil {
			return ProvisionedKey{}, err
		}
		announceCheckout(options.OnStatus, options.OpenBrowser, options.NoOpen, checkoutURL)
	}

	paidToken := sessionToken
	if phase <= phasePoll {
		status(options.OnStatus, StatusWaitingPayment, "")
		paid, err := pollUntilPaid(ctx, client, sessionToken, options.OnSession)
		if err != nil {
			return ProvisionedKey{}, err
		}
		paidToken = paid.SessionToken
		if paid.Status == SessionStatusExchanging {
			// Another/lost request claimed exchange while we were polling payment.
			// Carry that fact into the provisioning branch so it follows the claim
			// instead of issuing a second single-flight Exchange request.
			phase = phaseWaitExchange
		}
	}

	result := ProvisionedKey{
		BaseURL: endpoints.InferenceBaseURL,
		Model:   firstNonEmpty(options.Model, DefaultModel),
	}
	if options.Exchange {
		status(options.OnStatus, StatusProvisioningKey, "")
		if phase == phaseWaitExchange {
			return ProvisionedKey{}, pollUntilExchangeSettled(ctx, client, sessionToken, options.OnSession)
		}
		exchange, err := client.Exchange(ctx, options.SessionToken, paidToken)
		if err != nil {
			return ProvisionedKey{}, err
		}
		if strings.TrimSpace(exchange.APIKey) == "" {
			return ProvisionedKey{}, fmt.Errorf("aimlapi.com did not return an API key")
		}
		result.APIKey = exchange.APIKey
		result.APIKeyID = exchange.APIKeyID
	}
	return result, nil
}

// resolveTopupSession returns the partner-checkout session token to drive and the
// phase to enter. A fresh run opens a new session at phasePay. A resume inspects
// the existing session and enters the earliest phase that cannot re-charge:
//   - pending_auth    → phasePay: payment never started, so (re-)Pay is safe.
//   - pending_payment → phasePoll: a checkout is already open; poll, never re-Pay.
//   - paid            → phaseExchange: exchange the paid session once.
//   - exchanging      → phaseWaitExchange: another/lost request owns the atomic
//     exchange claim; poll it, never issue a second Exchange request.
//   - exchanged       → terminal: the one-shot raw key is gone; point at dashboard
//     recovery and keep the token so a re-run repeats that guidance, not a charge.
//   - cancelled/expired/failed → terminal: drop the dead token so a re-run is fresh.
func resolveTopupSession(ctx context.Context, client *Client, options StreamTopUpOptions, partnerID, partnerName string, endpoints Endpoints) (string, topupPhase, error) {
	resume := strings.TrimSpace(options.ResumeSessionToken)
	if resume == "" {
		session, err := client.CreateSession(ctx, partnerID, partnerName, BuildPartnerReturnURL(endpoints.VerificationBaseURL))
		if err != nil {
			return "", phasePay, err
		}
		return session.SessionToken, phasePay, nil
	}
	session, err := client.GetSession(ctx, resume)
	if err != nil {
		if isTerminalSessionAPIError(err) {
			reportSession(options.OnSession, "")
		}
		return "", phasePay, err
	}
	switch session.Status {
	case SessionStatusPendingAuth:
		return resume, phasePay, nil
	case SessionStatusPendingPayment:
		return resume, phasePoll, nil
	case SessionStatusPaid:
		return resume, phaseExchange, nil
	case SessionStatusExchanging:
		return resume, phaseWaitExchange, nil
	case SessionStatusExchanged:
		return "", phasePay, fmt.Errorf("session was already exchanged; rotate the key from the aimlapi.com dashboard")
	default:
		reportSession(options.OnSession, "") // drop the dead token → next run starts fresh
		return "", phasePay, fmt.Errorf("payment %s; re-run the top-up to try again", session.Status)
	}
}

func reportSession(callback func(string), sessionToken string) {
	if callback != nil {
		callback(sessionToken)
	}
}

// announceCheckout opens the hosted checkout in the browser (unless suppressed)
// and reports the URL either way, so the TUI can always show the direct link.
func announceCheckout(onStatus func(Status, string), open func(string) error, noOpen bool, checkoutURL string) {
	if noOpen || open == nil {
		status(onStatus, StatusOpeningCheckout, checkoutURL)
		return
	}
	if err := open(checkoutURL); err != nil {
		status(onStatus, StatusOpeningCheckout, "Open manually: "+checkoutURL)
		return
	}
	status(onStatus, StatusOpeningCheckout, checkoutURL)
}

func validatedCheckoutURL(value string) (string, error) {
	checkoutURL := strings.TrimSpace(value)
	parsed, err := url.Parse(checkoutURL)
	if err != nil || !parsed.IsAbs() || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil {
		return "", fmt.Errorf("payment provider did not return a valid HTTPS checkout URL")
	}
	return checkoutURL, nil
}

// StreamTopUpByKeyOptions drives a top-up funded with the API key that already
// owns the account (Path A pasted key, or AIMLAPI_API_KEY). Unlike StreamTopUp it
// needs no email session and never exchanges a key — the caller already holds one,
// so it only funds the wallet: create-session → pay-by-key → wait-for-payment.
type StreamTopUpByKeyOptions struct {
	APIKey           string // raw key that owns the account (required)
	AmountUSD        string // dollars; parsed via ParseAmountUSD (min $20)
	InferenceBaseURL string // validated key endpoint; empty uses ResolveEndpoints
	AutoTopUp        bool   // enroll the account in automatic top-up at checkout time
	PartnerID        string
	PartnerName      string
	NoOpen           bool

	// ResumeSessionToken resumes an existing partner-checkout session instead of
	// opening a new one. PaymentSessionID is the idempotency handle carried across
	// retries so a re-issued pay returns the same checkout, never a second charge.
	ResumeSessionToken string
	PaymentSessionID   string // required; generate once via NewPaymentSessionID, reuse on retry

	HTTPClient  *http.Client
	OpenBrowser func(string) error
	OnStatus    func(Status, string)
	OnSession   func(sessionToken string)
}

// StreamTopUpByKey funds the account behind APIKey via the hosted checkout and
// reports progress through OnStatus. It returns an empty ProvisionedKey: the
// pasted/env key is unchanged, only the balance grows.
func StreamTopUpByKey(ctx context.Context, options StreamTopUpByKeyOptions) (ProvisionedKey, error) {
	endpoints := ResolveEndpoints()
	if baseURL := strings.TrimSpace(options.InferenceBaseURL); baseURL != "" {
		endpoints.InferenceBaseURL = baseURL
	}
	client := NewClient(endpoints, options.HTTPClient)
	partnerID := ResolvePartnerID(options.PartnerID)
	partnerName := firstNonEmpty(options.PartnerName, DefaultPartnerName)
	amount, err := ParseAmountUSD(options.AmountUSD)
	if err != nil {
		return ProvisionedKey{}, err
	}
	if strings.TrimSpace(options.APIKey) == "" {
		return ProvisionedKey{}, fmt.Errorf("an API key is required to top up")
	}
	if strings.TrimSpace(options.PaymentSessionID) == "" {
		return ProvisionedKey{}, fmt.Errorf("a payment session id is required to top up")
	}

	status(options.OnStatus, StatusCreatingSession, "")
	sessionToken, phase, err := resolveByKeySession(ctx, client, options, partnerID, partnerName, endpoints)
	if err != nil {
		return ProvisionedKey{}, err
	}
	reportSession(options.OnSession, sessionToken)

	if phase <= phasePay {
		successURL, cancelURL := BuildPartnerCheckoutReturnURLs(endpoints.PayBaseURL, sessionToken)
		status(options.OnStatus, StatusOpeningCheckout, "")
		pay, err := client.TopUpByKey(ctx, options.APIKey, TopUpByKeyRequest{
			SessionToken:     sessionToken,
			AmountUSDMinor:   amount,
			PaymentSessionID: options.PaymentSessionID,
			AutoTopUp:        options.AutoTopUp,
			SuccessURL:       successURL,
			CancelURL:        cancelURL,
		})
		if err != nil {
			return ProvisionedKey{}, err
		}
		checkoutURL, err := validatedCheckoutURL(pay.Checkout.PayURL)
		if err != nil {
			return ProvisionedKey{}, err
		}
		announceCheckout(options.OnStatus, options.OpenBrowser, options.NoOpen, checkoutURL)
	}

	if phase <= phasePoll {
		status(options.OnStatus, StatusWaitingPayment, "")
		if _, err := pollUntilPaid(ctx, client, sessionToken, options.OnSession); err != nil {
			return ProvisionedKey{}, err
		}
	}
	// No exchange: the account is funded and the caller keeps its existing key.
	return ProvisionedKey{}, nil
}

// resolveByKeySession mirrors resolveTopupSession for the by-key flow. Because
// by-key never exchanges, a paid/exchanging/exchanged session all mean "already
// funded, done" (phaseExchange skips straight past pay + poll to the empty result).
func resolveByKeySession(ctx context.Context, client *Client, options StreamTopUpByKeyOptions, partnerID, partnerName string, endpoints Endpoints) (string, topupPhase, error) {
	resume := strings.TrimSpace(options.ResumeSessionToken)
	if resume == "" {
		session, err := client.CreateSession(ctx, partnerID, partnerName, BuildPartnerReturnURL(endpoints.VerificationBaseURL))
		if err != nil {
			return "", phasePay, err
		}
		return session.SessionToken, phasePay, nil
	}
	session, err := client.GetSession(ctx, resume)
	if err != nil {
		if isTerminalSessionAPIError(err) {
			reportSession(options.OnSession, "")
		}
		return "", phasePay, err
	}
	switch session.Status {
	case SessionStatusPendingAuth:
		return resume, phasePay, nil
	case SessionStatusPendingPayment:
		return resume, phasePoll, nil
	case SessionStatusPaid, SessionStatusExchanging, SessionStatusExchanged:
		return resume, phaseExchange, nil
	default:
		reportSession(options.OnSession, "") // drop the dead token → next run starts fresh
		return "", phasePay, fmt.Errorf("payment %s; re-run the top-up to try again", session.Status)
	}
}
