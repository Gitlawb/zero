package aimlapi

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Status is a progress phase reported by StreamTopUp through its OnStatus callback.
type Status string

const (
	StatusCreatingSession Status = "creating-session"
	StatusOpeningCheckout Status = "opening-checkout"
	StatusWaitingPayment  Status = "waiting-payment"
	StatusProvisioningKey Status = "provisioning-key"
)

// ProvisionedKey is the outcome of a top-up: the (optionally minted) API key plus
// the inference base URL and model to write into the provider profile.
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

// ParseAmountUSD parses a dollar string into USD minor units (cents), enforcing the
// min/max top-up bounds. An empty value yields the default amount.
func ParseAmountUSD(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return DefaultAmountUSDMinor, nil
	}
	dollars, err := strconv.ParseFloat(value, 64)
	// NaN/±Inf parse without error and slip past the min/max comparisons below
	// (every ordered comparison with NaN is false), so reject them explicitly
	// before the cents conversion turns them into a garbage minor-unit amount.
	if err != nil || math.IsNaN(dollars) || math.IsInf(dollars, 0) || dollars <= 0 {
		return 0, fmt.Errorf("invalid amount %q; pass a positive USD amount", value)
	}
	minor := int(dollars*100 + 0.5)
	if minor < MinAmountUSDMinor {
		return 0, fmt.Errorf("minimum top-up is $%d", MinAmountUSDMinor/100)
	}
	if minor > MaxAmountUSDMinor {
		return 0, fmt.Errorf("maximum top-up is $%d", MaxAmountUSDMinor/100)
	}
	return minor, nil
}

// NewPaymentSessionID returns a random UUIDv4-shaped idempotency id for a top-up.
// The caller generates it once per top-up intent and reuses it on retry so the
// backend (and Stripe) coalesce retries onto the same checkout instead of a
// second charge.
func NewPaymentSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func pollUntilPaid(ctx context.Context, client *Client, sessionToken string, onSession func(string)) (PartnerCheckoutSession, error) {
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
			if isTerminalSessionAPIError(err) {
				reportSession(onSession, "")
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
			// The checkout cannot recover from these states. Drop the retained token
			// (and the TUI's paired by-key idempotency ID) now so the very next retry
			// creates a fresh payment intent.
			reportSession(onSession, "")
			return PartnerCheckoutSession{}, fmt.Errorf("payment %s; re-run the top-up to try again", session.Status)
		default:
			if err := sleepContext(ctx, pollInterval); err != nil {
				return PartnerCheckoutSession{}, err
			}
		}
	}
	return PartnerCheckoutSession{}, fmt.Errorf("timed out waiting for payment; re-run once the payment clears")
}

func isTerminalSessionAPIError(err error) bool {
	var apiErr APIError
	return errors.As(err, &apiErr) && apiErr.Status >= http.StatusBadRequest && apiErr.Status < http.StatusInternalServerError
}

// pollUntilExchangeSettled follows an exchange claim that was already in flight
// when a retry resumed the session. Exchange is single-flight and the raw key is
// returned only to the winning request, so issuing Exchange again is never safe.
// Once the claim completes, direct the user to the dashboard recovery path.
func pollUntilExchangeSettled(ctx context.Context, client *Client, sessionToken string, onSession func(string)) error {
	deadline := time.Now().Add(pollTimeout)
	for time.Now().Before(deadline) {
		session, err := client.GetSession(ctx, sessionToken)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if isTerminalSessionAPIError(err) {
				reportSession(onSession, "")
				return err
			}
			if err := sleepContext(ctx, pollInterval); err != nil {
				return err
			}
			continue
		}
		switch session.Status {
		case SessionStatusExchanged:
			return fmt.Errorf("session was already exchanged; rotate the key from the aimlapi.com dashboard")
		case SessionStatusExchanging:
			if err := sleepContext(ctx, pollInterval); err != nil {
				return err
			}
		case SessionStatusCancelled, SessionStatusExpired, SessionStatusFailed:
			reportSession(onSession, "")
			return fmt.Errorf("key provisioning %s; rotate the key from the aimlapi.com dashboard", session.Status)
		default:
			return fmt.Errorf("key provisioning returned to %s; re-run the top-up", session.Status)
		}
	}
	return fmt.Errorf("timed out waiting for key provisioning; retry to check the same session")
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
