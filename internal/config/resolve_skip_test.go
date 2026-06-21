package config

import "testing"

// A single unresolvable, NON-active provider (e.g. one whose catalogID preset this
// build doesn't ship) must not crash resolution — it's dropped, the rest survive.
func TestNormalizeProvidersSkipsUnresolvableNonActive(t *testing.T) {
	good := ProviderProfile{Name: "good", ProviderKind: ProviderKindOpenAICompatible, BaseURL: "https://api.example.com/v1", Model: "m"}
	bad := ProviderProfile{Name: "badcat", CatalogID: "definitely-not-a-real-catalog-xyz", Model: "m"}

	got, active, err := normalizeProviders([]ProviderProfile{good, bad}, "good")
	if err != nil {
		t.Fatalf("a bad NON-active provider must not fail resolution, got: %v", err)
	}
	if active.Name != "good" {
		t.Fatalf("active = %q, want good", active.Name)
	}
	if len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("the unresolvable provider should be dropped, got %#v", got)
	}
}

// But an unresolvable ACTIVE provider IS fatal — the run can't proceed without it.
func TestNormalizeProvidersFatalWhenActiveUnresolvable(t *testing.T) {
	bad := ProviderProfile{Name: "badcat", CatalogID: "definitely-not-a-real-catalog-xyz", Model: "m"}
	if _, _, err := normalizeProviders([]ProviderProfile{bad}, "badcat"); err == nil {
		t.Fatal("an unresolvable ACTIVE provider must be a fatal error")
	}
}
