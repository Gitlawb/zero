package providermodelcatalog

import (
	"testing"

	"github.com/Gitlawb/zero/internal/providercatalog"
)

func TestModelsAreProviderScoped(t *testing.T) {
	tests := []struct {
		provider string
		want     []string
		notWant  []string
	}{
		{
			provider: "ollama",
			want:     []string{"llama3.1", "qwen2.5-coder:32b"},
			notWant:  []string{"gpt-4.1", "gpt-5", "openai/gpt-4.1"},
		},
		{
			provider: "groq",
			want:     []string{"llama-3.3-70b-versatile", "openai/gpt-oss-120b"},
			notWant:  []string{"gpt-4.1", "claude-sonnet-4.5"},
		},
		{
			provider: "mistral",
			want:     []string{"mistral-large-latest", "codestral-latest"},
			notWant:  []string{"gpt-4.1", "claude-sonnet-4.5"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			descriptor, ok := providercatalog.Get(tt.provider)
			if !ok {
				t.Fatalf("provider %q missing from catalog", tt.provider)
			}
			models := Models(descriptor)
			got := map[string]bool{}
			for _, model := range models {
				got[model.ID] = true
			}
			for _, want := range tt.want {
				if !got[want] {
					t.Fatalf("%s models missing %q; got %#v", tt.provider, want, modelIDs(models))
				}
			}
			for _, notWant := range tt.notWant {
				if got[notWant] {
					t.Fatalf("%s models should not include %q; got %#v", tt.provider, notWant, modelIDs(models))
				}
			}
		})
	}
}

func TestModelsDoNotAliasMutableCatalogState(t *testing.T) {
	descriptor, ok := providercatalog.Get("groq")
	if !ok {
		t.Fatal("provider groq missing from catalog")
	}
	first := Models(descriptor)
	if len(first) == 0 {
		t.Fatal("expected groq models")
	}
	first[0].ID = "mutated"

	second := Models(descriptor)
	if second[0].ID == "mutated" {
		t.Fatal("Models returned aliased mutable catalog state")
	}
}

func modelIDs(models []Model) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}
