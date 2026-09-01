package aimlapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// PaymentMethod selects how a partner-checkout top-up is paid for.
type PaymentMethod string

const (
	PaymentMethodCard   PaymentMethod = "card"
	PaymentMethodCrypto PaymentMethod = "crypto"
)

// SessionStatus is the lifecycle state of a partner-checkout session, as reported
// by the aimlapi.com backend while polling for payment.
type SessionStatus string

const (
	SessionStatusPendingAuth    SessionStatus = "pending_auth"
	SessionStatusPendingPayment SessionStatus = "pending_payment"
	SessionStatusPaid           SessionStatus = "paid"
	SessionStatusExchanging     SessionStatus = "exchanging"
	SessionStatusExchanged      SessionStatus = "exchanged"
	SessionStatusCancelled      SessionStatus = "cancelled"
	SessionStatusExpired        SessionStatus = "expired"
	SessionStatusFailed         SessionStatus = "failed"
)

// AuthResult is the bearer token minted by a passwordless sign-in / sign-up.
type AuthResult struct {
	Token string `json:"token"`
	Exp   int64  `json:"exp"`
}

// PartnerCheckoutSession is a partner-attributed top-up session; its SessionToken
// addresses it on later pay/exchange/poll calls.
type PartnerCheckoutSession struct {
	ID             string        `json:"id"`
	SessionToken   string        `json:"sessionToken"`
	PartnerID      string        `json:"partnerId"`
	PartnerName    *string       `json:"partnerName"`
	UserID         *int64        `json:"userId"`
	AmountUSDMinor *int64        `json:"amountUsdMinor"`
	Status         SessionStatus `json:"status"`
	IssuedKeyID    *string       `json:"issuedKeyId"`
	ReturnURL      *string       `json:"returnUrl"`
}

// PaymentSession is the hosted payment-provider checkout; PayURL is the page the
// user opens to pay.
type PaymentSession struct {
	ProviderSessionID string `json:"providerSessionId"`
	PayURL            string `json:"payUrl"`
}

// PayResult pairs the hosted checkout with the updated partner-checkout session
// returned when a top-up payment is initiated.
type PayResult struct {
	Checkout        PaymentSession         `json:"checkout"`
	PartnerCheckout PartnerCheckoutSession `json:"partnerCheckout"`
}

// ExchangeResult holds the API key minted when a paid session is exchanged.
type ExchangeResult struct {
	APIKey   string `json:"apiKey"`
	APIKeyID string `json:"apiKeyId"`
}

// TopUpByKeyRequest funds a partner-checkout session with the API key that owns
// the account (Path A / env key), instead of an email session.
type TopUpByKeyRequest struct {
	SessionToken     string
	AmountUSDMinor   int
	PaymentSessionID string // idempotency: same id → same checkout, never a second charge
	AutoTopUp        bool   // enroll the account in automatic top-up at checkout (card, saved off-session)
	SuccessURL       string
	CancelURL        string
}

// TopUpByKeyResult is the hosted checkout returned by TopUpByKey.
type TopUpByKeyResult struct {
	Checkout PaymentSession `json:"checkout"`
}

// APIError is a non-2xx HTTP response from the aimlapi.com API. Status carries the
// code so callers can branch (e.g. 401 = bad key, 5xx = retryable).
type APIError struct {
	Message string
	Status  int
	Body    string
}

// Error renders the method, endpoint, status, and (when present) the response body.
func (e APIError) Error() string {
	if strings.TrimSpace(e.Body) != "" {
		return fmt.Sprintf("%s: HTTP %d: %s", e.Message, e.Status, strings.TrimSpace(e.Body))
	}
	return fmt.Sprintf("%s: HTTP %d", e.Message, e.Status)
}

// Client talks to the aimlapi.com auth, app, and inference APIs for the onboarding
// and partner-checkout top-up flows.
type Client struct {
	endpoints  Endpoints
	httpClient *http.Client
}

func NewClient(endpoints Endpoints, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &Client{endpoints: endpoints, httpClient: httpClient}
}

// CreateSession opens a partner-checkout session attributed to partnerID; the
// returned SessionToken addresses it on the pay/exchange/poll calls.
func (c *Client) CreateSession(ctx context.Context, partnerID string, partnerName string, returnURL string) (PartnerCheckoutSession, error) {
	body := map[string]any{"partnerId": partnerID}
	if strings.TrimSpace(partnerName) != "" {
		body["partnerName"] = strings.TrimSpace(partnerName)
	}
	if strings.TrimSpace(returnURL) != "" {
		body["returnUrl"] = strings.TrimSpace(returnURL)
	}
	var result PartnerCheckoutSession
	err := c.request(ctx, http.MethodPost, strings.TrimRight(c.endpoints.AppBaseURL, "/")+"/v3/partner-checkout/sessions", "", body, &result)
	return result, err
}

// GetSession fetches the current state of a partner-checkout session; used to poll
// for payment completion.
func (c *Client) GetSession(ctx context.Context, sessionToken string) (PartnerCheckoutSession, error) {
	var result PartnerCheckoutSession
	err := c.request(ctx, http.MethodGet, strings.TrimRight(c.endpoints.AppBaseURL, "/")+"/v3/partner-checkout/sessions/"+urlPathEscape(sessionToken), "", nil, &result)
	return result, err
}

// Pay initiates payment for a session and returns the hosted checkout URL.
// autoTopUp enrolls the account in automatic top-up at checkout time.
func (c *Client) Pay(ctx context.Context, bearer string, sessionToken string, amountUSDMinor int, paymentSessionID string, method PaymentMethod, successURL string, cancelURL string, autoTopUp bool) (PayResult, error) {
	body := map[string]any{
		"amountUsdMinor":   amountUSDMinor,
		"paymentSessionId": paymentSessionID,
		"method":           method,
	}
	if strings.TrimSpace(successURL) != "" {
		body["successUrl"] = strings.TrimSpace(successURL)
	}
	if strings.TrimSpace(cancelURL) != "" {
		body["cancelUrl"] = strings.TrimSpace(cancelURL)
	}
	// Only sent when enabled: enrolls the account in automatic top-up. The backend
	// honours this field at checkout time.
	if autoTopUp {
		body["autoTopUp"] = true
	}
	var result PayResult
	err := c.request(ctx, http.MethodPost, strings.TrimRight(c.endpoints.AppBaseURL, "/")+"/v3/partner-checkout/sessions/"+urlPathEscape(sessionToken)+"/pay", bearer, body, &result)
	return result, err
}

// Exchange converts a paid session into a freshly minted API key (new-account flow).
func (c *Client) Exchange(ctx context.Context, bearer string, sessionToken string) (ExchangeResult, error) {
	var result ExchangeResult
	err := c.request(ctx, http.MethodPost, strings.TrimRight(c.endpoints.AppBaseURL, "/")+"/v3/partner-checkout/sessions/"+urlPathEscape(sessionToken)+"/exchange", bearer, nil, &result)
	return result, err
}

// TopUpByKey funds a partner-checkout session using the raw API key that owns the
// account (Path A / env key), returning the hosted checkout. It binds the session
// to the key's account server-side, so no email session is needed and no key is
// exchanged. req.PaymentSessionID makes a retry idempotent (same id → same
// checkout, never a second charge).
func (c *Client) TopUpByKey(ctx context.Context, apiKey string, req TopUpByKeyRequest) (TopUpByKeyResult, error) {
	body := map[string]any{
		"sessionToken":     req.SessionToken,
		"amountUsdMinor":   req.AmountUSDMinor,
		"paymentSessionId": req.PaymentSessionID,
	}
	if req.AutoTopUp {
		body["autoTopUp"] = true
	}
	if strings.TrimSpace(req.SuccessURL) != "" {
		body["successUrl"] = strings.TrimSpace(req.SuccessURL)
	}
	if strings.TrimSpace(req.CancelURL) != "" {
		body["cancelUrl"] = strings.TrimSpace(req.CancelURL)
	}
	var result TopUpByKeyResult
	err := c.request(ctx, http.MethodPost, c.billingV2URL("/billing/topup"), apiKey, body, &result)
	return result, err
}

// billingV2URL builds a v2 billing API URL from the inference base. The top-up
// endpoint lives next to the balance endpoint on the API gateway, one version up
// (".../v1" → ".../v2/billing/..."), so it re-anchors the version while keeping any
// host/prefix an override supplied.
func (c *Client) billingV2URL(path string) string {
	base := strings.TrimRight(strings.TrimSpace(c.endpoints.InferenceBaseURL), "/")
	base = strings.TrimSuffix(base, "/v1")
	return strings.TrimRight(base, "/") + "/v2" + path
}

func (c *Client) request(ctx context.Context, method string, endpoint string, bearer string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(bearer) != "" {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("network request to %s failed: %w", endpoint, err)
	}
	defer response.Body.Close()
	text, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if readErr != nil {
		return readErr
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return APIError{Message: method + " " + endpoint, Status: response.StatusCode, Body: string(text)}
	}
	if out == nil {
		return nil
	}
	if len(bytes.TrimSpace(text)) == 0 {
		return APIError{Message: method + " " + endpoint + " returned empty body", Status: response.StatusCode}
	}
	if err := json.Unmarshal(text, out); err != nil {
		return APIError{Message: method + " " + endpoint + " returned non-JSON body", Status: response.StatusCode, Body: string(text)}
	}
	return nil
}

func urlPathEscape(value string) string {
	return url.PathEscape(value)
}
