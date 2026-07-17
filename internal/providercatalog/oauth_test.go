package providercatalog

import "testing"

func TestOAuthProviders(t *testing.T) {
	providers := OAuthProviders()
	if len(providers) != 5 {
		t.Fatalf("OAuthProviders() = %d, want 5 (openrouter, xai, kimi, huggingface, chatgpt)", len(providers))
	}
	byID := map[string]Descriptor{}
	for _, d := range providers {
		if !d.OAuth {
			t.Fatalf("OAuthProviders() returned a non-OAuth descriptor %q", d.ID)
		}
		byID[d.ID] = d
	}
	or, ok := byID["openrouter"]
	if !ok || !or.OAuthMintsKey || or.OAuthDeviceFlow {
		t.Fatalf("openrouter oauth flags wrong: %+v", or)
	}
	xai, ok := byID["xai"]
	if !ok || xai.OAuthMintsKey || !xai.OAuthDeviceFlow {
		t.Fatalf("xai oauth flags wrong: %+v", xai)
	}
	hf, ok := byID["huggingface"]
	if !ok || hf.OAuthMintsKey || !hf.OAuthDeviceFlow {
		t.Fatalf("huggingface oauth flags wrong: %+v", hf)
	}
	cg, ok := byID["chatgpt"]
	if !ok || cg.OAuthMintsKey || cg.OAuthDeviceFlow {
		t.Fatalf("chatgpt oauth flags wrong: %+v", cg)
	}
	kimi, ok := byID["kimi"]
	if !ok || kimi.OAuthMintsKey || !kimi.OAuthDeviceFlow {
		t.Fatalf("kimi oauth flags wrong: %+v", kimi)
	}
}

func TestOAuthProvidersReturnsIndependentClones(t *testing.T) {
	first := OAuthProviders()
	mutated := 0
	for i := range first {
		for j := range first[i].AuthEnvVars {
			first[i].AuthEnvVars[j] = "MUTATED"
			mutated++
		}
	}
	if mutated == 0 {
		t.Fatal("expected at least one OAuth descriptor with AuthEnvVars to mutate")
	}
	// A fresh call must not observe the in-place mutation: like the other catalog
	// accessors, OAuthProviders must hand back clones, not shared backing slices.
	for _, d := range OAuthProviders() {
		for _, env := range d.AuthEnvVars {
			if env == "MUTATED" {
				t.Fatalf("OAuthProviders() leaked a shared AuthEnvVars slice for %q", d.ID)
			}
		}
	}
}

func TestNonOAuthProvidersNotFlagged(t *testing.T) {
	for _, d := range All() {
		switch d.ID {
		case "openrouter", "xai", "kimi", "huggingface", "chatgpt":
			continue
		}
		if d.OAuth {
			t.Fatalf("provider %q should not be flagged OAuth-capable", d.ID)
		}
	}
}
