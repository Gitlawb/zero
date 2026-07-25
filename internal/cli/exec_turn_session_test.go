package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providers/openai"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// TestRunExecOptimizedSessionUnderGate proves the end-to-end wiring: with
// ZERO_OPENAI_TURN_SESSION on and a real official-OpenAI provider, a headless
// run streams through the optimized session — the server sees the prewarm HEAD
// probe in addition to the turn's POST.
func TestRunExecOptimizedSessionUnderGate(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("ZERO_OPENAI_TURN_SESSION", "1")
	cwd := t.TempDir()

	var heads, posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodHead:
			heads.Add(1)
			w.WriteHeader(http.StatusMethodNotAllowed)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/chat/completions"):
			posts.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"}}]}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "say done"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return cwd, nil },
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{
				ActiveProvider: "official-openai",
				Provider: config.ProviderProfile{
					Name:         "official-openai",
					ProviderKind: config.ProviderKindOpenAI,
					BaseURL:      server.URL,
					APIKey:       "sk-exec-test",
					Model:        "pr8-exec-model",
				},
			}, nil
		},
		newProvider: func(profile config.ProviderProfile) (zeroruntime.Provider, error) {
			// Pin a keep-alive transport so the probe fires on every platform:
			// the shared default transport disables keep-alives on macOS, where
			// the session correctly skips the prewarm.
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.DisableKeepAlives = false
			return openai.New(openai.Options{
				APIKey:     profile.APIKey,
				BaseURL:    profile.BaseURL,
				Model:      profile.Model,
				HTTPClient: &http.Client{Transport: transport},
			})
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	if got := posts.Load(); got < 1 {
		t.Fatalf("server saw %d chat-completions POSTs, want >= 1", got)
	}
	// The prewarm probe is asynchronous; it fires well before the model turn
	// finishes, but poll rather than assuming scheduling order. The deadline
	// exceeds the production prewarmTimeout (3s) so a slow probe cannot flake
	// this assertion.
	deadline := time.Now().Add(5 * time.Second)
	for heads.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := heads.Load(); got != 1 {
		t.Fatalf("server saw %d prewarm HEAD probes, want exactly 1", got)
	}
}

// TestRunExecGateOnFallsBackForFakeProvider proves fallback safety: with the
// gate on but a provider that is not the concrete *openai.Provider, the run
// proceeds on the default path exactly as today.
func TestRunExecGateOnFallsBackForFakeProvider(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("ZERO_OPENAI_TURN_SESSION", "1")
	cwd := t.TempDir()

	var builds int
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"exec", "--model", "claude-haiku-4.5", "hello"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) { return cwd, nil },
		resolveConfig: func(_ string, overrides config.Overrides) (config.ResolvedConfig, error) {
			model := "claude-haiku-4.5"
			if overrides.Provider.Model != "" {
				model = overrides.Provider.Model
			}
			cfg := execResolvedConfig()
			cfg.Provider.ProviderKind = config.ProviderKindAnthropic
			cfg.Provider.Model = model
			cfg.MaxTurns = 3
			return cfg, nil
		},
		newProvider: func(config.ProviderProfile) (zeroruntime.Provider, error) {
			builds++
			return &escalatingExecProvider{}, nil
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("exitCode = %d stdout=%s stderr=%s", exitCode, stdout.String(), stderr.String())
	}
	if builds != 1 {
		t.Fatalf("newProvider called %d times, want 1 (default path, no optimized session)", builds)
	}
}
