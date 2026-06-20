package oauth

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestRefreshSingleflightsConcurrentCallers(t *testing.T) {
	// The fake token endpoint counts refreshes; with a single-use refresh token,
	// only ONE concurrent refresh must happen — the rest reuse the rotated token (M7).
	fp := newFakeProvider(t, `{"access_token":"fresh-at","expires_in":3600}`)
	env := map[string]string{
		"ZERO_OAUTH_DEMO_CLIENT_ID": "client",
		"ZERO_OAUTH_DEMO_TOKEN_URL": fp.server.URL + "/token",
	}
	m := managerFor(t, env, nil)
	if err := m.store.Save(ProviderKey("demo"), Token{AccessToken: "stale", RefreshToken: "rt", ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	const N = 10
	var wg sync.WaitGroup
	got := make([]string, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i], errs[i] = m.GetFresh(context.Background(), ProviderKey("demo"))
		}(i)
	}
	wg.Wait()

	for i := range got {
		if errs[i] != nil {
			t.Fatalf("GetFresh[%d]: %v", i, errs[i])
		}
		if got[i] != "fresh-at" {
			t.Errorf("GetFresh[%d] = %q, want fresh-at", i, got[i])
		}
	}
	if hits := fp.tokenHits.Load(); hits != 1 {
		t.Fatalf("expected exactly 1 refresh across %d concurrent callers, got %d", N, hits)
	}
}
