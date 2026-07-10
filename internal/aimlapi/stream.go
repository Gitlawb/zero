package aimlapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
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
	Method       PaymentMethod
	AutoTopUp    bool // enroll the account in automatic top-up at checkout time
	Exchange     bool // exchange the paid session into a new key (new accounts)
	Model        string
	PartnerID    string
	PartnerName  string
	NoOpen       bool

	HTTPClient  *http.Client
	OpenBrowser func(string) error
	OnStatus    func(Status, string)
}

// StreamTopUp performs the browser-checkout top-up and reports progress via
// OnStatus.
func StreamTopUp(ctx context.Context, options StreamTopUpOptions) (ProvisionedKey, error) {
	endpoints := ResolveEndpoints()
	client := NewClient(endpoints, options.HTTPClient)
	partnerID := firstNonEmpty(options.PartnerID, os.Getenv("AIMLAPI_PARTNER_ID"), DefaultPartnerID)
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

	status(options.OnStatus, StatusCreatingSession, "")
	session, err := client.CreateSession(ctx, partnerID, partnerName, BuildPartnerReturnURL(endpoints.VerificationBaseURL))
	if err != nil {
		return ProvisionedKey{}, err
	}
	successURL, cancelURL := BuildPartnerCheckoutReturnURLs(endpoints.PayBaseURL, session.SessionToken)

	status(options.OnStatus, StatusOpeningCheckout, "")
	pay, err := client.Pay(ctx, options.SessionToken, session.SessionToken, amount, method, successURL, cancelURL, options.AutoTopUp)
	if err != nil {
		return ProvisionedKey{}, err
	}
	checkoutURL := strings.TrimSpace(pay.Checkout.PayURL)
	if checkoutURL == "" {
		return ProvisionedKey{}, fmt.Errorf("payment provider did not return a checkout URL")
	}
	if options.NoOpen || options.OpenBrowser == nil {
		status(options.OnStatus, StatusOpeningCheckout, checkoutURL)
	} else if err := options.OpenBrowser(checkoutURL); err != nil {
		status(options.OnStatus, StatusOpeningCheckout, "Open manually: "+checkoutURL)
	} else {
		status(options.OnStatus, StatusOpeningCheckout, checkoutURL)
	}

	status(options.OnStatus, StatusWaitingPayment, "")
	paid, err := pollUntilPaid(ctx, client, session.SessionToken)
	if err != nil {
		return ProvisionedKey{}, err
	}

	result := ProvisionedKey{
		BaseURL: endpoints.InferenceBaseURL,
		Model:   firstNonEmpty(options.Model, DefaultModel),
	}
	if options.Exchange {
		status(options.OnStatus, StatusProvisioningKey, "")
		exchange, err := client.Exchange(ctx, options.SessionToken, paid.SessionToken)
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
