package providermodelcatalog

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/providercatalog"
)

func TestParseModelsDevProviderScopesAndMapsMetadata(t *testing.T) {
	body := []byte(`{
		"openai": {
			"models": {
				"gpt-4.1": {
					"id": "gpt-4.1",
					"name": "GPT-4.1",
					"tool_call": true,
					"reasoning": false,
					"limit": {"context": 1048576, "output": 32768},
					"cost": {"input": 2, "output": 8},
					"modalities": {"input": ["text", "image"], "output": ["text"]}
				},
				"gpt-image-1": {
					"id": "gpt-image-1",
					"name": "GPT Image",
					"modalities": {"input": ["text", "image"], "output": ["image"]}
				},
				"text-embedding-3-large": {
					"id": "text-embedding-3-large",
					"name": "Embedding model"
				},
				"whisper-1": {
					"id": "whisper-1",
					"name": "Whisper"
				}
			}
		},
		"anthropic": {
			"models": {
				"claude-sonnet-4.5": {"id": "claude-sonnet-4.5"}
			}
		}
	}`)

	models, err := ParseModelsDevProvider(body, "openai")
	if err != nil {
		t.Fatalf("ParseModelsDevProvider returned error: %v", err)
	}
	if got := strings.Join(modelIDs(models), ","); got != "gpt-4.1" {
		t.Fatalf("models = %#v, want only coding-capable OpenAI model", got)
	}
	model := models[0]
	if model.ID != "gpt-4.1" || model.Description != "GPT-4.1" {
		t.Fatalf("model identity = %#v, want GPT-4.1 metadata", model)
	}
	if model.ContextWindow != 1048576 || !model.ToolCall || model.Reasoning {
		t.Fatalf("model capabilities = %#v, want context/tools without reasoning", model)
	}
	if model.InputCost != 2 || model.OutputCost != 8 {
		t.Fatalf("model cost = %#v, want input/output pricing", model)
	}
	if strings.Join(model.InputModalities, ",") != "text,image" || strings.Join(model.OutputModalities, ",") != "text" {
		t.Fatalf("model modalities = %#v/%#v, want text,image -> text", model.InputModalities, model.OutputModalities)
	}
	if model.Source != "models.dev" {
		t.Fatalf("model source = %q, want models.dev", model.Source)
	}
}

func TestParseOpenGatewayCatalogSupportsRichModelJSON(t *testing.T) {
	body := []byte(`{
		"models": [
			{
				"id": "minimax-m3",
				"name": "MiniMax M3",
				"description": "agentic coding route",
				"context_window": 262144,
				"tool_call": true,
				"reasoning": true,
				"tags": ["coding", "free"],
				"cost": {"input": 0, "output": 0}
			},
			{
				"id": "image-route",
				"name": "Image Route",
				"modalities": {"input": ["text"], "output": ["image"]}
			}
		]
	}`)

	models, err := ParseOpenGatewayCatalog(body)
	if err != nil {
		t.Fatalf("ParseOpenGatewayCatalog returned error: %v", err)
	}
	if got := strings.Join(modelIDs(models), ","); got != "minimax-m3" {
		t.Fatalf("models = %#v, want one gateway coding model", got)
	}
	model := models[0]
	if model.ID != "minimax-m3" || model.Description != "agentic coding route" {
		t.Fatalf("gateway model = %#v, want rich description", model)
	}
	if model.ContextWindow != 262144 || !model.ToolCall || !model.Reasoning {
		t.Fatalf("gateway capabilities = %#v, want context/tools/reasoning", model)
	}
	if strings.Join(model.Tags, ",") != "coding,free" {
		t.Fatalf("gateway tags = %#v, want coding/free", model.Tags)
	}
	if model.Source != "opengateway" {
		t.Fatalf("gateway source = %q, want opengateway", model.Source)
	}
}

func TestModelsDevProviderIDMapsZeroAliases(t *testing.T) {
	tests := map[string]string{
		"github":       "github-models",
		"moonshot":     "moonshotai",
		"nvidia-nim":   "nvidia",
		"xiaomi-mimo":  "xiaomi",
		"dashscope":    "alibaba",
		"ollama-cloud": "ollama-cloud",
		"zai-cn":       "zai",
		"minimaxi-cn":  "minimax",
	}
	for zeroID, want := range tests {
		provider, ok := providercatalog.Get(zeroID)
		if !ok {
			t.Fatalf("provider %q missing from catalog", zeroID)
		}
		if got := ModelsDevProviderID(provider); got != want {
			t.Fatalf("ModelsDevProviderID(%q) = %q, want %q", zeroID, got, want)
		}
	}
}
