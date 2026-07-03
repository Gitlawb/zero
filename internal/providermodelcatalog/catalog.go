package providermodelcatalog

import (
	"strings"

	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/providercatalog"
)

type Model struct {
	ID               string
	Description      string
	ContextWindow    int
	ToolCall         bool
	Reasoning        bool
	InputModalities  []string
	OutputModalities []string
	InputCost        float64
	OutputCost       float64
	Tags             []string
	Source           string
}

// Shared by both "minimax" and "minimaxi-cn"; Models() rebuilds a fresh slice
// per call, so the shared backing slice cannot be mutated by callers.
var minimaxCuratedModels = []Model{
	{ID: "MiniMax-M3", Description: "catalog default"},
	{ID: "MiniMax-M2.1", Description: "agentic coding model"},
}

// Shared by both "zai" (international, api.z.ai) and "zai-cn" (China,
// open.bigmodel.cn); same model lineup on both endpoints.
var zaiCuratedModels = []Model{
	{ID: "glm-4.5", Description: "catalog default"},
	{ID: "glm-4.5-air", Description: "fast model"},
	{ID: "glm-4.6", Description: "latest general model"},
	{ID: "glm-z1-air", Description: "reasoning model"},
}

var curatedModels = map[string][]Model{
	"ollama-cloud": {
		{ID: "qwen3-coder:480b", Description: "catalog default"},
		{ID: "gpt-oss:120b", Description: "agentic coding model"},
		{ID: "deepseek-v4-pro", Description: "coding model"},
		{ID: "minimax-m3", Description: "agentic coding model"},
		{ID: "kimi-k2.6", Description: "coding model"},
		{ID: "devstral-2:123b", Description: "coding model"},
	},
	"ollama": {
		{ID: "llama3.1", Description: "catalog default"},
		{ID: "qwen2.5-coder:32b", Description: "local coding model"},
		{ID: "deepseek-coder-v2:16b", Description: "local coding model"},
		{ID: "codellama:13b", Description: "local coding model"},
	},
	"lmstudio": {
		{ID: "local-model", Description: "catalog default"},
		{ID: "qwen2.5-coder-32b-instruct", Description: "local coding model"},
		{ID: "deepseek-coder-v2-lite-instruct", Description: "local coding model"},
		{ID: "llama-3.1-8b-instruct", Description: "local chat model"},
	},
	"groq": {
		{ID: "llama-3.3-70b-versatile", Description: "catalog default"},
		{ID: "openai/gpt-oss-120b", Description: "large open-weight model"},
		{ID: "openai/gpt-oss-20b", Description: "fast open-weight model"},
		{ID: "deepseek-r1-distill-llama-70b", Description: "reasoning model"},
		{ID: "qwen/qwen3-32b", Description: "coding-capable model"},
	},
	"openrouter": {
		{ID: "openai/gpt-4.1", Description: "catalog default"},
		{ID: "anthropic/claude-sonnet-4.5", Description: "coding model"},
		{ID: "google/gemini-2.5-pro", Description: "long-context model"},
		{ID: "minimax/minimax-m2.1", Description: "agentic coding model"},
		{ID: "deepseek/deepseek-chat", Description: "coding model"},
	},
	"chatgpt": {
		{ID: "gpt-5.5", Description: "recommended Codex model"},
		{ID: "gpt-5.4", Description: "strong Codex model"},
		{ID: "gpt-5.4-mini", Description: "fast Codex model"},
		{ID: "gpt-5.3-codex-spark", Description: "research preview fast Codex model"},
	},
	"deepseek": {
		{ID: "deepseek-chat", Description: "catalog default"},
		{ID: "deepseek-reasoner", Description: "reasoning model"},
	},
	"together": {
		{ID: "meta-llama/Llama-3.3-70B-Instruct-Turbo", Description: "catalog default"},
		{ID: "Qwen/Qwen2.5-Coder-32B-Instruct", Description: "coding model"},
		{ID: "deepseek-ai/DeepSeek-R1", Description: "reasoning model"},
		{ID: "meta-llama/Llama-4-Maverick-17B-128E-Instruct-FP8", Description: "multimodal model"},
	},
	"dashscope": {
		{ID: "qwen-plus", Description: "catalog default"},
		{ID: "qwen-max", Description: "strong general model"},
		{ID: "qwen-coder-plus", Description: "coding model"},
		{ID: "qwen3-coder-plus", Description: "coding model"},
	},
	"moonshot": {
		{ID: "kimi-k2-0905-preview", Description: "catalog default"},
		{ID: "kimi-k2-turbo-preview", Description: "fast coding model"},
		{ID: "moonshot-v1-128k", Description: "long-context model"},
	},
	"nvidia-nim": {
		{ID: "nvidia/llama-3.1-nemotron-70b-instruct", Description: "catalog default"},
		{ID: "meta/llama-3.1-70b-instruct", Description: "general model"},
		{ID: "mistralai/mixtral-8x7b-instruct-v0.1", Description: "mixture model"},
	},
	"minimax":     minimaxCuratedModels,
	"minimaxi-cn": minimaxCuratedModels,
	"mistral": {
		{ID: "mistral-large-latest", Description: "catalog default"},
		{ID: "codestral-latest", Description: "coding model"},
		{ID: "mistral-medium-latest", Description: "balanced model"},
		{ID: "ministral-8b-latest", Description: "small fast model"},
		{ID: "magistral-medium-latest", Description: "reasoning model"},
	},
	"github": {
		{ID: "openai/gpt-4.1", Description: "catalog default"},
		{ID: "openai/gpt-4o", Description: "multimodal model"},
		{ID: "openai/o3-mini", Description: "reasoning model"},
		{ID: "mistral-ai/codestral-2501", Description: "coding model"},
	},
	"xai": {
		{ID: "grok-4", Description: "catalog default"},
		{ID: "grok-3", Description: "general model"},
		{ID: "grok-3-mini", Description: "fast reasoning model"},
		{ID: "grok-code-fast-1", Description: "coding model"},
	},
	"venice": {
		{ID: "qwen-2.5-qwq-32b", Description: "catalog default"},
		{ID: "llama-3.3-70b", Description: "general model"},
		{ID: "deepseek-r1-671b", Description: "reasoning model"},
	},
	"xiaomi-mimo": {
		{ID: "mimo-vl", Description: "catalog default"},
		{ID: "mimo-v2.5-pro-ultraspeed", Description: "fast model"},
	},
	"bankr": {
		{ID: "bankr-large", Description: "catalog default"},
	},
	"zai":    zaiCuratedModels,
	"zai-cn": zaiCuratedModels,
	// OpenGateway smart-routes by model id across its upstream providers
	// (see /health: xiaomi-mimo, minimax, qwen, google, nvidia, z-ai). These are
	// the curated coding defaults; the gateway accepts any model its upstreams
	// expose, so users can also type an id the picker doesn't list.
	"gitlawb-opengateway": {
		{ID: "mimo-v2.5-pro", Description: "catalog default (Xiaomi MiMo)"},
		{ID: "mimo-v2.5-pro-ultraspeed", Description: "fast model (Xiaomi MiMo)"},
		{ID: "MiniMax-M3", Description: "MiniMax model"},
		{ID: "qwen-plus", Description: "Qwen model"},
		{ID: "gemini-2.5-pro", Description: "long-context model (Google)"},
		{ID: "glm-4.6", Description: "Z.ai model"},
		{ID: "nvidia/llama-3.1-nemotron-70b-instruct", Description: "NVIDIA NIM model"},
	},
	"atomic-chat": {
		{ID: "gpt-4.1", Description: "catalog default"},
		{ID: "gpt-4o-mini", Description: "fast model"},
	},
	"custom-openai-compatible": {
		{ID: "custom-model", Description: "custom endpoint model"},
	},
	"custom-anthropic-compatible": {
		{ID: "custom-model", Description: "custom endpoint model"},
	},
}

func Models(provider providercatalog.Descriptor) []Model {
	if models, ok := curatedModels[provider.ID]; ok {
		return dedupeModels(provider.DefaultModel, models)
	}
	models := registryModels(provider)
	if len(models) > 0 {
		return dedupeModels(provider.DefaultModel, models)
	}
	return dedupeModels(provider.DefaultModel, nil)
}

func registryModels(provider providercatalog.Descriptor) []Model {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return nil
	}
	models := []Model{}
	for _, entry := range registry.List(modelregistry.ListOptions{}) {
		if !modelMatchesProvider(entry, provider) {
			continue
		}
		models = append(models, Model{
			ID:            entry.ID,
			Description:   entry.DisplayName,
			ContextWindow: entry.ContextLimits.ContextWindow,
		})
		if len(models) >= 8 {
			break
		}
	}
	return models
}

func dedupeModels(defaultModel string, models []Model) []Model {
	result := []Model{}
	seen := map[string]bool{}
	add := func(model Model) {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" || seen[model.ID] {
			return
		}
		if model.Description == "" {
			model.Description = "catalog model"
		}
		seen[model.ID] = true
		result = append(result, model)
	}
	add(Model{ID: defaultModel, Description: "catalog default"})
	for _, model := range models {
		add(model)
	}
	return result
}

func modelMatchesProvider(model modelregistry.ModelEntry, provider providercatalog.Descriptor) bool {
	switch provider.Transport {
	case providercatalog.TransportOpenAI:
		return model.Provider == modelregistry.ProviderOpenAI
	case providercatalog.TransportAnthropic, providercatalog.TransportAnthropicCompatible:
		return model.Provider == modelregistry.ProviderAnthropic
	case providercatalog.TransportGoogle:
		return model.Provider == modelregistry.ProviderGoogle
	default:
		return false
	}
}
