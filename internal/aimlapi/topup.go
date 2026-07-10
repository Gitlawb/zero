package aimlapi

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Status string

const (
	StatusCreatingSession Status = "creating-session"
	StatusOpeningCheckout Status = "opening-checkout"
	StatusWaitingPayment  Status = "waiting-payment"
	StatusProvisioningKey Status = "provisioning-key"
)

type ProvisionedKey struct {
	APIKey   string
	APIKeyID string
	BaseURL  string
	Model    string
}

const (
	pollInterval = 3 * time.Second
	pollTimeout  = 20 * time.Minute
)

// Sentinel amount-validation errors so callers can tell an out-of-range amount
// apart from a non-numeric one and surface the right message.
var (
	ErrAmountTooLow  = errors.New("top-up amount is below the minimum")
	ErrAmountTooHigh = errors.New("top-up amount is above the maximum")
)

func ParseAmountUSD(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultAmountUSDMinor, nil
	}
	dollars, err := strconv.ParseFloat(value, 64)
	if err != nil || dollars <= 0 {
		return 0, fmt.Errorf("invalid amount %q; pass a positive USD amount", value)
	}
	minor := int(dollars*100 + 0.5)
	if minor < MinAmountUSDMinor {
		return 0, fmt.Errorf("%w of $%d", ErrAmountTooLow, MinAmountUSDMinor/100)
	}
	if minor > MaxAmountUSDMinor {
		return 0, fmt.Errorf("%w of $%d", ErrAmountTooHigh, MaxAmountUSDMinor/100)
	}
	return minor, nil
}

func pollUntilPaid(ctx context.Context, client *Client, sessionToken string) (PartnerCheckoutSession, error) {
	deadline := time.Now().Add(pollTimeout)
	for time.Now().Before(deadline) {
		session, err := client.GetSession(ctx, sessionToken)
		if err != nil {
			// Context cancellation/deadline is terminal (also covers an in-flight
			// request cancelled mid-poll, whose transport error wraps ctx.Err()).
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return PartnerCheckoutSession{}, err
			}
			// A 4xx API response is terminal. Everything else — a 5xx or a transient
			// transport error (non-APIError, e.g. a dropped connection during the
			// wait) — is retried until the poll deadline.
			var apiErr APIError
			if errors.As(err, &apiErr) && apiErr.Status < 500 {
				return PartnerCheckoutSession{}, err
			}
			if err := sleepContext(ctx, pollInterval); err != nil {
				return PartnerCheckoutSession{}, err
			}
			continue
		}
		switch session.Status {
		case SessionStatusPaid, SessionStatusExchanging:
			return session, nil
		case SessionStatusExchanged:
			return PartnerCheckoutSession{}, fmt.Errorf("session was already exchanged; rotate the key from the aimlapi.com dashboard")
		case SessionStatusCancelled, SessionStatusExpired, SessionStatusFailed:
			return PartnerCheckoutSession{}, fmt.Errorf("payment %s; re-run the top-up to try again", session.Status)
		default:
			if err := sleepContext(ctx, pollInterval); err != nil {
				return PartnerCheckoutSession{}, err
			}
		}
	}
	return PartnerCheckoutSession{}, fmt.Errorf("timed out waiting for payment; re-run once the payment clears")
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func status(callback func(Status, string), value Status, detail string) {
	if callback != nil {
		callback(value, detail)
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
