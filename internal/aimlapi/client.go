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

type PaymentMethod string

const (
	PaymentMethodCard   PaymentMethod = "card"
	PaymentMethodCrypto PaymentMethod = "crypto"
)

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

type AuthResult struct {
	Token string `json:"token"`
	Exp   int64  `json:"exp"`
}

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

type PaymentSession struct {
	ProviderSessionID string `json:"providerSessionId"`
	PayURL            string `json:"payUrl"`
}

type PayResult struct {
	Checkout        PaymentSession         `json:"checkout"`
	PartnerCheckout PartnerCheckoutSession `json:"partnerCheckout"`
}

type ExchangeResult struct {
	APIKey   string `json:"apiKey"`
	APIKeyID string `json:"apiKeyId"`
}

type APIError struct {
	Message string
	Status  int
	Body    string
}

func (e APIError) Error() string {
	if strings.TrimSpace(e.Body) != "" {
		return fmt.Sprintf("%s: HTTP %d: %s", e.Message, e.Status, strings.TrimSpace(e.Body))
	}
	return fmt.Sprintf("%s: HTTP %d", e.Message, e.Status)
}

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

func (c *Client) GetSession(ctx context.Context, sessionToken string) (PartnerCheckoutSession, error) {
	var result PartnerCheckoutSession
	err := c.request(ctx, http.MethodGet, strings.TrimRight(c.endpoints.AppBaseURL, "/")+"/v3/partner-checkout/sessions/"+urlPathEscape(sessionToken), "", nil, &result)
	return result, err
}

func (c *Client) Pay(ctx context.Context, bearer string, sessionToken string, amountUSDMinor int, method PaymentMethod, successURL string, cancelURL string, autoTopUp bool) (PayResult, error) {
	body := map[string]any{
		"amountUsdMinor": amountUSDMinor,
		"method":         method,
	}
	if strings.TrimSpace(successURL) != "" {
		body["successUrl"] = strings.TrimSpace(successURL)
	}
	if strings.TrimSpace(cancelURL) != "" {
		body["cancelUrl"] = strings.TrimSpace(cancelURL)
	}
	// Only sent when enabled: the field enrolls the account in automatic top-up.
	// The gateway ValidationPipe does not whitelist, so it passes through today and
	// is honoured once the backend adds support (see AIMLAPI-AUTOTOPUP-TZ.md).
	if autoTopUp {
		body["autoTopUp"] = true
	}
	var result PayResult
	err := c.request(ctx, http.MethodPost, strings.TrimRight(c.endpoints.AppBaseURL, "/")+"/v3/partner-checkout/sessions/"+urlPathEscape(sessionToken)+"/pay", bearer, body, &result)
	return result, err
}

func (c *Client) Exchange(ctx context.Context, bearer string, sessionToken string) (ExchangeResult, error) {
	var result ExchangeResult
	err := c.request(ctx, http.MethodPost, strings.TrimRight(c.endpoints.AppBaseURL, "/")+"/v3/partner-checkout/sessions/"+urlPathEscape(sessionToken)+"/exchange", bearer, nil, &result)
	return result, err
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
	if out == nil || len(text) == 0 {
		return nil
	}
	if err := json.Unmarshal(text, out); err != nil {
		return APIError{Message: method + " " + endpoint + " returned non-JSON body", Status: response.StatusCode, Body: string(text)}
	}
	return nil
}

func urlPathEscape(value string) string {
	return url.PathEscape(value)
}
