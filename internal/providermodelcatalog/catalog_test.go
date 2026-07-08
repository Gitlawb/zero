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
			provider: "ollama-cloud",
			want:     []string{"qwen3-coder:480b", "gpt-oss:120b"},
			notWant:  []string{"llama3.1", "gpt-4.1", "openai/gpt-4.1"},
		},
		{
			provider: "ollama",
			want:     []string{"llama3.1", "qwen2.5-coder:32b"},
			notWant:  []string{"qwen3-coder:480b", "gpt-4.1", "gpt-5", "openai/gpt-4.1"},
		},
		{
			provider: "groq",
			want:     []string{"llama-3.3-70b-versatile", "openai/gpt-oss-120b"},
			notWant:  []string{"gpt-4.1", "claude-sonnet-4.5"},
		},
		{
			provider: "chatgpt",
			want:     []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark"},
			notWant:  []string{"gpt-5", "gpt-4.1", "openai/gpt-4.1"},
		},
		{
			provider: "mistral",
			want:     []string{"mistral-large-latest", "codestral-latest"},
			notWant:  []string{"gpt-4.1", "claude-sonnet-4.5"},
		},
		{
			provider: "minimaxi-cn",
			want:     []string{"MiniMax-M3", "MiniMax-M2.1"},
			notWant:  []string{"gpt-4.1", "claude-sonnet-4.5"},
		},
		{
			provider: "zai-cn",
			want:     []string{"glm-4.5", "glm-4.6", "glm-4.5-air", "glm-z1-air"},
			notWant:  []string{"gpt-4.1", "claude-sonnet-4.5"},
		},
		{
			provider: "gitlawb-opengateway",
			want:     []string{"mimo-v2.5-pro", "tencent/hy3"},
			notWant:  []string{"openai/gpt-4.1", "claude-sonnet-4.5"},
		},
		{
			provider: "opencode-go-anthropic-compatible",
			want:     []string{"minimax-m3", "minimax-m2.7", "qwen3.7-plus", "qwen3.7-max"},
			notWant:  []string{"gpt-4.1", "claude-sonnet-4.5", "deepseek-chat"},
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

// Pins the contract that MiniMax CN exposes exactly the same model IDs as the
// international provider; a divergence here is almost always a mistake.
func TestMiniMaxCNModelsMirrorInternational(t *testing.T) {
	intl, ok := providercatalog.Get("minimax")
	if !ok {
		t.Fatal("provider minimax missing from catalog")
	}
	cn, ok := providercatalog.Get("minimaxi-cn")
	if !ok {
		t.Fatal("provider minimaxi-cn missing from catalog")
	}
	intlModels := Models(intl)
	cnModels := Models(cn)
	if len(intlModels) != len(cnModels) {
		t.Fatalf("model count mismatch: international=%d china=%d", len(intlModels), len(cnModels))
	}
	intlIDs := map[string]string{}
	for _, m := range intlModels {
		intlIDs[m.ID] = m.Description
	}
	for _, m := range cnModels {
		wantDesc, ok := intlIDs[m.ID]
		if !ok {
			t.Fatalf("china provider exposes %q which is not in the international catalog", m.ID)
		}
		if m.Description != wantDesc {
			t.Fatalf("model %q description diverged: international=%q china=%q", m.ID, wantDesc, m.Description)
		}
	}
}

// Same contract as TestMiniMaxCNModelsMirrorInternational, for Z.ai: the
// international (api.z.ai) and China (open.bigmodel.cn) endpoints expose the
// same model lineup.
func TestZaiCNModelsMirrorInternational(t *testing.T) {
	intl, ok := providercatalog.Get("zai")
	if !ok {
		t.Fatal("provider zai missing from catalog")
	}
	cn, ok := providercatalog.Get("zai-cn")
	if !ok {
		t.Fatal("provider zai-cn missing from catalog")
	}
	intlModels := Models(intl)
	cnModels := Models(cn)
	if len(intlModels) != len(cnModels) {
		t.Fatalf("model count mismatch: international=%d china=%d", len(intlModels), len(cnModels))
	}
	intlIDs := map[string]string{}
	for _, m := range intlModels {
		intlIDs[m.ID] = m.Description
	}
	for _, m := range cnModels {
		wantDesc, ok := intlIDs[m.ID]
		if !ok {
			t.Fatalf("china provider exposes %q which is not in the international catalog", m.ID)
		}
		if m.Description != wantDesc {
			t.Fatalf("model %q description diverged: international=%q china=%q", m.ID, wantDesc, m.Description)
		}
	}
}
