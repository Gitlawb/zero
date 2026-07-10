package aimlapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// Additional client methods backing the interactive TUI onboarding flow. They
// target the endpoints shipped in the aimlapi backend:
//   - GET  {inference}/billing/balance            (Path A: validate key + balance)
//   - PATCH{auth}/v1/auth/account                 (Path B: does the account exist?)
//   - POST {auth}/v1/auth/sign-in/code(/verify)   (Path B existing: passwordless login)
//   - POST {auth}/v1/auth/account/passwordless    (Path B new: email-only signup)
//   - POST {app}/v1/keys                          (mint a key once we hold a session)

type BalanceResult struct {
	Balance             float64 `json:"balance"`
	LowBalance          bool    `json:"lowBalance"`
	LowBalanceThreshold float64 `json:"lowBalanceThreshold"`
}

type CheckResult struct {
	// "sign-in" (account exists) or "sign-up" (no account yet).
	Action   string  `json:"action"`
	Provider *string `json:"provider"`
}

type CreatedKey struct {
	Key string `json:"key"`
	ID  string `json:"id"`
}

func (c *Client) authURL(path string) string {
	return strings.TrimRight(c.endpoints.AuthBaseURL, "/") + path
}

func (c *Client) appURL(path string) string {
	return strings.TrimRight(c.endpoints.AppBaseURL, "/") + path
}

func (c *Client) inferenceURL(path string) string {
	return strings.TrimRight(c.endpoints.InferenceBaseURL, "/") + path
}

// GetBalance doubles as key validation: a 401 means the key is invalid, a 2xx
// returns the wallet balance. Auth is the raw API key itself.
func (c *Client) GetBalance(ctx context.Context, apiKey string) (BalanceResult, error) {
	var result BalanceResult
	err := c.request(ctx, http.MethodGet, c.inferenceURL("/billing/balance"), apiKey, nil, &result)
	return result, err
}

func (c *Client) CheckAccount(ctx context.Context, email string) (CheckResult, error) {
	var result CheckResult
	err := c.request(ctx, http.MethodPatch, c.authURL("/v1/auth/account"), "", map[string]any{"email": email}, &result)
	return result, err
}

func (c *Client) SendSignInCode(ctx context.Context, email string) error {
	return c.request(ctx, http.MethodPost, c.authURL("/v1/auth/sign-in/code"), "", map[string]any{"email": email}, nil)
}

func (c *Client) VerifySignInCode(ctx context.Context, email string, code string) (AuthResult, error) {
	var result AuthResult
	err := c.request(ctx, http.MethodPost, c.authURL("/v1/auth/sign-in/code/verify"), "", map[string]any{"email": email, "code": code}, &result)
	return result, err
}

func (c *Client) CreatePasswordlessAccount(ctx context.Context, email string) (AuthResult, error) {
	var result AuthResult
	err := c.request(ctx, http.MethodPost, c.authURL("/v1/auth/account/passwordless"), "", map[string]any{"email": email}, &result)
	return result, err
}

// CreateKey mints a fresh API key for a session-holding user.
func (c *Client) CreateKey(ctx context.Context, bearer string, name string) (CreatedKey, error) {
	body := map[string]any{}
	if strings.TrimSpace(name) != "" {
		body["name"] = strings.TrimSpace(name)
	}
	var result CreatedKey
	err := c.request(ctx, http.MethodPost, c.appURL("/v1/keys"), bearer, body, &result)
	if err == nil && strings.TrimSpace(result.Key) == "" {
		err = fmt.Errorf("aimlapi.com did not return an API key")
	}
	return result, err
}
