package oauth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRefreshSchedulerNoRefreshTokenIsNoop(t *testing.T) {
	m := managerFor(t, map[string]string{"ZERO_OAUTH_DEMO_CLIENT_ID": "c"}, nil)
	// Token without a refresh token => scheduler must exit promptly (no-op).
	if err := m.store.Save(ProviderKey("demo"), Token{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	s := NewRefreshScheduler()
	s.Start(context.Background(), m, ProviderKey("demo"))
	// Stop waits for the goroutine; if it didn't exit on its own this still returns
	// because Stop cancels — but the loop should already be done.
	s.Stop()
}

func TestRefreshSchedulerRestartsAfterStop(t *testing.T) {
	m := managerFor(t, map[string]string{"ZERO_OAUTH_DEMO_CLIENT_ID": "c"}, nil)
	if err := m.store.Save(ProviderKey("demo"), Token{AccessToken: "a", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
	s := NewRefreshScheduler()
	s.Start(context.Background(), m, ProviderKey("demo"))
	s.Stop()
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if started {
		t.Fatal("Stop must reset started so the scheduler is not permanently inert (L14)")
	}
	// A second Start must take effect (previously a no-op forever after the first Stop).
	s.Start(context.Background(), m, ProviderKey("demo"))
	s.mu.Lock()
	restarted := s.started
	s.mu.Unlock()
	if !restarted {
		t.Fatal("scheduler must be startable again after Stop")
	}
	s.Stop()
}

func TestRefreshSchedulerRefreshesBeforeExpiry(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `{"access_token":"refreshed","expires_in":3600}`)
	}))
	defer server.Close()

	store, err := NewStore(StoreOptions{FilePath: filepath.Join(t.TempDir(), "tok.json")})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	m, err := NewManager(ManagerOptions{
		Store: store,
		Env: map[string]string{
			"ZERO_OAUTH_DEMO_CLIENT_ID": "c",
			"ZERO_OAUTH_DEMO_TOKEN_URL": server.URL,
		},
		RefreshBuffer: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// Expiry just past the buffer so the scheduled delay is ~0 and a refresh fires
	// almost immediately.
	if err := store.Save(ProviderKey("demo"), Token{AccessToken: "old", RefreshToken: "rt", ExpiresAt: time.Now().Add(15 * time.Millisecond)}); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	s := NewRefreshScheduler()
	s.Start(context.Background(), m, ProviderKey("demo"))
	defer s.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		stored, _, err := store.Load(ProviderKey("demo"))
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if stored.AccessToken == "refreshed" {
			return // success
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("scheduler did not refresh before expiry (token endpoint hits=%d)", hits.Load())
}
