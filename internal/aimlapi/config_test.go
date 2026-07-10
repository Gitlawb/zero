package aimlapi

import "testing"

func TestResolveEndpointsDefaults(t *testing.T) {
	t.Setenv("AIMLAPI_AUTH_URL", "")
	t.Setenv("AIMLAPI_APP_URL", "")
	t.Setenv("AIMLAPI_INFERENCE_URL", "")
	t.Setenv("AIMLAPI_PAY_URL", "")

	endpoints := ResolveEndpoints()
	if endpoints.AuthBaseURL != "https://auth.aimlapi.com" ||
		endpoints.AppBaseURL != "https://app.aimlapi.com" ||
		endpoints.InferenceBaseURL != "https://api.aimlapi.com/v1" ||
		endpoints.PayBaseURL != "https://pay.aimlapi.com" {
		t.Fatalf("unexpected endpoints: %#v", endpoints)
	}
}

func TestBuildPartnerReturnURL(t *testing.T) {
	t.Setenv("AIMLAPI_RETURN_URL", "")
	// The frontend base (the web app the flow was opened from) is returned as-is,
	// trailing slash trimmed — an https page the browser can actually land on.
	if got := BuildPartnerReturnURL("https://aimlapi.com/app/"); got != "https://aimlapi.com/app" {
		t.Fatalf("return URL = %q, want the frontend base", got)
	}
	// An empty base falls back to the https default, never a custom scheme.
	if got := BuildPartnerReturnURL(""); got != DefaultReturnURL {
		t.Fatalf("empty base return URL = %q, want %q", got, DefaultReturnURL)
	}

	t.Setenv("AIMLAPI_RETURN_URL", "https://example.test/done")
	if got := BuildPartnerReturnURL("https://aimlapi.com/app"); got != "https://example.test/done" {
		t.Fatalf("env override return URL = %q", got)
	}
}
