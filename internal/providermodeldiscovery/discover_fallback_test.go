package providermodeldiscovery

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
)

func TestDiscoverCatalogFallsBackWhenLiveIDsMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.json":
			_, _ = w.Write([]byte(`{"openai":{"models":{"gpt-4.1":{"id":"gpt-4.1","name":"GPT-4.1","tool_call":true,"reasoning":true,"limit":{"context":1048576}}}}}`))
		case "/v1/models":
			// 200, but live model IDs don't match any curated coding model, so the
			// merge is empty (M11): the result must fall back to the curated catalog,
			// not collapse to an empty list.
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-image-1"},{"id":"text-embedding-3-large"}]}`))
		default:
			t.Fatalf("unexpected request path %q", r.URL.Path)
		}
	}))
	defer server.Close()

	provider := providercatalog.Descriptor{ID: "openai", Transport: providercatalog.TransportOpenAI, DefaultBaseURL: server.URL + "/v1", RequiresAuth: true}
	models, err := DiscoverCatalog(context.Background(), provider, config.ProviderProfile{
		CatalogID: "openai", ProviderKind: config.ProviderKindOpenAI, BaseURL: server.URL + "/v1", APIKey: "sk-live",
	}, Options{HTTPClient: server.Client(), ModelsDevURL: server.URL + "/api.json"})
	if err != nil {
		t.Fatalf("DiscoverCatalog returned error: %v", err)
	}
	if got := strings.Join(modelIDs(models), ","); got != "gpt-4.1" {
		t.Fatalf("models = %q, want curated catalog fallback (gpt-4.1) instead of an empty list", got)
	}
}
