package aimlapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStreamTopUpByKeyFundsWithoutExchange(t *testing.T) {
	var topupCalls, exchangeCalls int
	var topupBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/partner-checkout/sessions"):
			_ = json.NewEncoder(w).Encode(PartnerCheckoutSession{SessionToken: "pcs_tok", Status: SessionStatusPendingAuth})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/v2/billing/topup"):
			topupCalls++
			_ = json.NewDecoder(r.Body).Decode(&topupBody)
			_ = json.NewEncoder(w).Encode(TopUpByKeyResult{Checkout: PaymentSession{PayURL: "https://pay/checkout"}})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/partner-checkout/sessions/"):
			_ = json.NewEncoder(w).Encode(PartnerCheckoutSession{SessionToken: "pcs_tok", Status: SessionStatusPaid})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exchange"):
			exchangeCalls++
			t.Errorf("by-key must never exchange a key")
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("AIMLAPI_APP_URL", server.URL)
	t.Setenv("AIMLAPI_INFERENCE_URL", server.URL+"/v1")

	var opened string
	result, err := StreamTopUpByKey(context.Background(), StreamTopUpByKeyOptions{
		APIKey:           "sk-user",
		AmountUSD:        "20",
		AutoTopUp:        true,
		PaymentSessionID: "pay-1",
		OpenBrowser:      func(u string) error { opened = u; return nil },
	})
	if err != nil {
		t.Fatalf("StreamTopUpByKey = %v", err)
	}
	if result.APIKey != "" {
		t.Fatalf("by-key must not mint a key, got %q", result.APIKey)
	}
	if topupCalls != 1 || exchangeCalls != 0 {
		t.Fatalf("topup=%d exchange=%d, want 1/0", topupCalls, exchangeCalls)
	}
	if opened != "https://pay/checkout" {
		t.Fatalf("opened checkout = %q", opened)
	}
	if topupBody["autoTopUp"] != true || topupBody["paymentSessionId"] != "pay-1" {
		t.Fatalf("topup body missing autoTopUp/paymentSessionId: %#v", topupBody)
	}
}

// resumeServer is a partner-checkout backend fake that fails the test if the
// re-charging endpoints (create-session / pay) are ever hit, and serves a
// scripted GetSession status sequence so a resume can be driven deterministically.
type resumeServer struct {
	t          *testing.T
	statuses   []string // GetSession status per call, last value repeats
	getCalls   int
	payCalls   int
	createCall int
	exchanged  int
}

func (rs *resumeServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/partner-checkout/sessions"):
			rs.createCall++
			rs.t.Errorf("resume must not create a new session")
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/pay"):
			rs.payCalls++
			rs.t.Errorf("resume must not re-pay (double charge)")
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/exchange"):
			rs.exchanged++
			_ = json.NewEncoder(w).Encode(ExchangeResult{APIKey: "sk-resumed", APIKeyID: "key_1"})
		case r.Method == http.MethodGet:
			status := rs.statuses[len(rs.statuses)-1]
			if rs.getCalls < len(rs.statuses) {
				status = rs.statuses[rs.getCalls]
			}
			rs.getCalls++
			_ = json.NewEncoder(w).Encode(PartnerCheckoutSession{SessionToken: "pc_tok", Status: SessionStatus(status)})
		default:
			rs.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	})
}

func TestStreamTopUpResumePaidExchangesWithoutRecharge(t *testing.T) {
	rs := &resumeServer{t: t, statuses: []string{"paid"}}
	server := httptest.NewServer(rs.handler())
	defer server.Close()
	t.Setenv("AIMLAPI_APP_URL", server.URL)

	var seen string
	result, err := StreamTopUp(context.Background(), StreamTopUpOptions{
		SessionToken:       "bearer",
		ResumeSessionToken: "pc_tok",
		AmountUSD:          "20",
		Exchange:           true,
		NoOpen:             true,
		OnSession:          func(token string) { seen = token },
	})
	if err != nil {
		t.Fatalf("StreamTopUp resume = %v", err)
	}
	if result.APIKey != "sk-resumed" {
		t.Fatalf("APIKey = %q, want recovered key", result.APIKey)
	}
	if rs.exchanged != 1 {
		t.Fatalf("exchange calls = %d, want 1", rs.exchanged)
	}
	if seen != "pc_tok" {
		t.Fatalf("OnSession token = %q, want retained pc_tok", seen)
	}
}

func TestStreamTopUpResumePendingPaymentPollsNeverRepays(t *testing.T) {
	// First GetSession (resume resolution) sees the still-open checkout; the next
	// (poll) sees it settle. Pay must never be called — the fake fails the test if
	// it is.
	rs := &resumeServer{t: t, statuses: []string{"pending_payment", "paid"}}
	server := httptest.NewServer(rs.handler())
	defer server.Close()
	t.Setenv("AIMLAPI_APP_URL", server.URL)

	result, err := StreamTopUp(context.Background(), StreamTopUpOptions{
		SessionToken:       "bearer",
		ResumeSessionToken: "pc_tok",
		AmountUSD:          "20",
		Exchange:           true,
		NoOpen:             true,
	})
	if err != nil {
		t.Fatalf("StreamTopUp resume = %v", err)
	}
	if result.APIKey != "sk-resumed" {
		t.Fatalf("APIKey = %q, want recovered key", result.APIKey)
	}
	if rs.payCalls != 0 {
		t.Fatalf("pay calls = %d, want 0", rs.payCalls)
	}
}

func TestStreamTopUpPollingDeadSessionClearsRetainedToken(t *testing.T) {
	rs := &resumeServer{t: t, statuses: []string{"pending_payment", "expired"}}
	server := httptest.NewServer(rs.handler())
	defer server.Close()
	t.Setenv("AIMLAPI_APP_URL", server.URL)

	seen := []string{}
	_, err := StreamTopUp(context.Background(), StreamTopUpOptions{
		SessionToken:       "bearer",
		ResumeSessionToken: "pc_tok",
		AmountUSD:          "20",
		Exchange:           true,
		NoOpen:             true,
		OnSession:          func(token string) { seen = append(seen, token) },
	})
	if err == nil || !strings.Contains(err.Error(), "payment expired") {
		t.Fatalf("err = %v, want expired payment error", err)
	}
	if len(seen) == 0 || seen[len(seen)-1] != "" {
		t.Fatalf("OnSession events = %#v, want terminal clear", seen)
	}
}

func TestStreamTopUpResumeExchangedReturnsRecoveryError(t *testing.T) {
	rs := &resumeServer{t: t, statuses: []string{"exchanged"}}
	server := httptest.NewServer(rs.handler())
	defer server.Close()
	t.Setenv("AIMLAPI_APP_URL", server.URL)

	_, err := StreamTopUp(context.Background(), StreamTopUpOptions{
		SessionToken:       "bearer",
		ResumeSessionToken: "pc_tok",
		AmountUSD:          "20",
		Exchange:           true,
		NoOpen:             true,
	})
	if err == nil || !strings.Contains(err.Error(), "already exchanged") {
		t.Fatalf("err = %v, want an already-exchanged recovery error", err)
	}
	if rs.exchanged != 0 {
		t.Fatalf("exchange calls = %d, want 0 (no second key)", rs.exchanged)
	}
}

func TestStreamTopUpResumeExchangingPollsWithoutSecondExchange(t *testing.T) {
	rs := &resumeServer{t: t, statuses: []string{"exchanging", "exchanged"}}
	server := httptest.NewServer(rs.handler())
	defer server.Close()
	t.Setenv("AIMLAPI_APP_URL", server.URL)

	_, err := StreamTopUp(context.Background(), StreamTopUpOptions{
		SessionToken:       "bearer",
		ResumeSessionToken: "pc_tok",
		AmountUSD:          "20",
		Exchange:           true,
		NoOpen:             true,
	})
	if err == nil || !strings.Contains(err.Error(), "already exchanged") {
		t.Fatalf("err = %v, want dashboard recovery guidance", err)
	}
	if rs.exchanged != 0 {
		t.Fatalf("exchange calls = %d, want 0 while an exchange claim is in flight", rs.exchanged)
	}
	if rs.createCall != 0 || rs.payCalls != 0 {
		t.Fatalf("resume created/paid a new checkout: create=%d pay=%d", rs.createCall, rs.payCalls)
	}
}

func TestStreamTopUpPollObservesExchangingWithoutSecondExchange(t *testing.T) {
	rs := &resumeServer{t: t, statuses: []string{"pending_payment", "exchanging", "exchanged"}}
	server := httptest.NewServer(rs.handler())
	defer server.Close()
	t.Setenv("AIMLAPI_APP_URL", server.URL)

	_, err := StreamTopUp(context.Background(), StreamTopUpOptions{
		SessionToken:       "bearer",
		ResumeSessionToken: "pc_tok",
		AmountUSD:          "20",
		Exchange:           true,
		NoOpen:             true,
	})
	if err == nil || !strings.Contains(err.Error(), "already exchanged") {
		t.Fatalf("err = %v, want dashboard recovery guidance", err)
	}
	if rs.exchanged != 0 {
		t.Fatalf("exchange calls = %d, want 0 after poll observed an existing claim", rs.exchanged)
	}
}
