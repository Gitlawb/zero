package aimlapi

import (
	"net/url"
	"os"
	"strings"
)

type Endpoints struct {
	AuthBaseURL      string
	AppBaseURL       string
	InferenceBaseURL string
	PayBaseURL       string
	// VerificationBaseURL is the aimlapi web-app frontend base. The browser is sent
	// back here after the partner checkout completes (see BuildPartnerReturnURL), so
	// it must point at the same environment's front as the one the CLI is talking to
	// to land the user there. Empty falls back to DefaultReturnURL.
	VerificationBaseURL string
}

const (
	DefaultPartnerID   = "part_62yQoGYDq4Yqnrj2R1iGrDNJ"
	DefaultPartnerName = "Gitlawb"
	// DefaultReturnURL is the https fallback the browser is sent to after the
	// checkout / OAuth consent finishes when no frontend base is known. It must be
	// a real web page the browser can open — NOT a custom scheme like
	// zero://aimlapi/complete, which fails with "the scheme does not have a
	// registered handler" because the CLI registers no OS protocol handler. The
	// CLI learns of success by polling; this URL is purely the browser's landing.
	DefaultReturnURL = "https://aimlapi.com/app"
	DefaultModel     = "anthropic/claude-sonnet-5"

	MinAmountUSDMinor     = 2000
	MaxAmountUSDMinor     = 1000000
	DefaultAmountUSDMinor = 2500
)

func ResolveEndpoints() Endpoints {
	return Endpoints{
		AuthBaseURL:         envOrDefault("AIMLAPI_AUTH_URL", "https://auth.aimlapi.com"),
		AppBaseURL:          envOrDefault("AIMLAPI_APP_URL", "https://app.aimlapi.com"),
		InferenceBaseURL:    envOrDefault("AIMLAPI_INFERENCE_URL", "https://api.aimlapi.com/v1"),
		PayBaseURL:          envOrDefault("AIMLAPI_PAY_URL", "https://pay.aimlapi.com"),
		VerificationBaseURL: envOrDefault("AIMLAPI_VERIFICATION_BASE_URL", "https://aimlapi.com/app"),
	}
}

func BuildPartnerCheckoutReturnURLs(appBaseURL string, sessionToken string) (successURL string, cancelURL string) {
	base := strings.TrimRight(strings.TrimSpace(appBaseURL), "/")
	token := url.QueryEscape(sessionToken)
	return base + "/checkout?checkout=success&partnerCheckout=1&sessionToken=" + token,
		base + "/checkout?checkout=cancel&partnerCheckout=1&sessionToken=" + token
}

// BuildPartnerReturnURL returns the https page the browser is sent back to once
// the partner checkout or OAuth consent completes — the aimlapi web app the user
// opened the flow from. frontendBaseURL is normally endpoints.VerificationBaseURL
// (the front that hosts the login/consent page), so the return follows the same
// environment. AIMLAPI_RETURN_URL overrides it outright; an empty base falls back
// to DefaultReturnURL. It is deliberately NOT a custom scheme (zero://…): no
// browser can hand a custom scheme off without an OS-registered handler, which a
// CLI does not install.
func BuildPartnerReturnURL(frontendBaseURL string) string {
	if override := strings.TrimSpace(os.Getenv("AIMLAPI_RETURN_URL")); override != "" {
		return override
	}
	if base := strings.TrimRight(strings.TrimSpace(frontendBaseURL), "/"); base != "" {
		return base
	}
	return DefaultReturnURL
}

func envOrDefault(name string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
