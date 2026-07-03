package providerio

import "testing"

func TestHeadersForCatalogLeavesOtherProvidersUnchanged(t *testing.T) {
	headers := map[string]string{"X-Trace": "test"}
	got := HeadersForCatalog("groq", headers)

	if got["X-Trace"] != "test" {
		t.Fatalf("X-Trace = %q, want test", got["X-Trace"])
	}
	if got["X-AIMLAPI-Partner-ID"] != "" {
		t.Fatalf("non-AIMLAPI headers include attribution: %#v", got)
	}

	got["X-Trace"] = "updated"
	if headers["X-Trace"] != "updated" {
		t.Fatal("non-AIMLAPI path should return the original header map")
	}
}

func TestHeadersForCatalogCopiesAndOverridesAIMLAPIHeaders(t *testing.T) {
	headers := map[string]string{
		"X-Trace":                       "test",
		"X-AIMLAPI-Integration-Repo":    "spoofed/repo",
		"X-AIMLAPI-Integration-Version": "spoofed-version",
	}
	got := HeadersForCatalog(" AIMLAPI ", headers)

	for header, want := range map[string]string{
		"X-Trace":                       "test",
		"X-AIMLAPI-Partner-ID":          "Gitlawb",
		"X-AIMLAPI-Integration-Repo":    "Gitlawb/zero",
		"X-AIMLAPI-Integration-Version": "1.0.0",
	} {
		if got[header] != want {
			t.Fatalf("%s = %q, want %q", header, got[header], want)
		}
	}
	if headers["X-AIMLAPI-Integration-Repo"] != "spoofed/repo" {
		t.Fatal("AIMLAPI header injection mutated the caller's map")
	}
}
