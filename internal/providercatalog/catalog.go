package providercatalog

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

type Transport string

const (
	TransportOpenAI              Transport = "openai"
	TransportAnthropic           Transport = "anthropic"
	TransportGoogle              Transport = "google"
	TransportBedrock             Transport = "bedrock"
	TransportVertex              Transport = "vertex"
	TransportOpenAICompatible    Transport = "openai-compatible"
	TransportOpenAICompat        Transport = TransportOpenAICompatible
	TransportAnthropicCompatible Transport = "anthropic-compatible"
	TransportAnthropicCompat     Transport = TransportAnthropicCompatible
)

type APIFormat string

const (
	APIFormatOpenAIResponses       APIFormat = "responses"
	APIFormatOpenAIChatCompletions APIFormat = "chat-completions"
	APIFormatAnthropicMessages     APIFormat = "messages"
	APIFormatGoogleGenerateContent APIFormat = "generate-content"
	APIFormatBedrockConverse       APIFormat = "bedrock-converse"
	APIFormatVertexGenerateContent APIFormat = "vertex-generate-content"
)

var ErrUnknownProvider = errors.New("unknown provider")

type Descriptor struct {
	ID                  string
	Name                string
	Transport           Transport
	DefaultBaseURL      string
	DefaultModel        string
	AuthEnvVars         []string
	RequiresAuth        bool
	UsesAmbientAuth     bool
	Public              bool
	Local               bool
	SupportedAPIFormats []APIFormat
	Aliases             []string
}

func RuntimeSupported(descriptor Descriptor) bool {
	switch descriptor.Transport {
	case TransportOpenAI, TransportOpenAICompatible, TransportAnthropic, TransportAnthropicCompatible, TransportGoogle:
		return true
	default:
		return false
	}
}

func RuntimeUnsupportedReason(descriptor Descriptor) string {
	if RuntimeSupported(descriptor) {
		return ""
	}
	switch descriptor.Transport {
	case TransportBedrock, TransportVertex:
		return "native adapter not implemented yet"
	default:
		return "provider transport not implemented yet"
	}
}

var descriptors = []Descriptor{
	openAI("openai", "OpenAI", "https://api.openai.com/v1", "gpt-4.1", []string{"OPENAI_API_KEY"}),
	anthropic("anthropic", "Anthropic", "https://api.anthropic.com", "claude-sonnet-4.5", []string{"ANTHROPIC_API_KEY"}),
	google("google", "Google", "https://generativelanguage.googleapis.com", "gemini-2.5-pro", []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, "gemini"),
	localOpenAI("ollama", "Ollama", "http://localhost:11434/v1", "llama3.1"),
	localOpenAI("lmstudio", "LM Studio", "http://localhost:1234/v1", "local-model", "lm-studio", "lm studio"),
	openAICompat("openrouter", "OpenRouter", "https://openrouter.ai/api/v1", "openai/gpt-4.1", []string{"OPENROUTER_API_KEY"}),
	openAICompat("groq", "Groq", "https://api.groq.com/openai/v1", "llama-3.3-70b-versatile", []string{"GROQ_API_KEY"}),
	openAICompat("deepseek", "DeepSeek", "https://api.deepseek.com/v1", "deepseek-chat", []string{"DEEPSEEK_API_KEY"}),
	openAICompat("together", "Together AI", "https://api.together.xyz/v1", "meta-llama/Llama-3.3-70B-Instruct-Turbo", []string{"TOGETHER_API_KEY"}),
	openAICompat("dashscope", "DashScope", "https://dashscope-intl.aliyuncs.com/compatible-mode/v1", "qwen-plus", []string{"DASHSCOPE_API_KEY", "QWEN_API_KEY"}, "qwen"),
	openAICompat("moonshot", "Moonshot AI", "https://api.moonshot.ai/v1", "kimi-k2-0905-preview", []string{"MOONSHOT_API_KEY"}, "kimi"),
	openAICompat("nvidia-nim", "NVIDIA NIM", "https://integrate.api.nvidia.com/v1", "nvidia/llama-3.1-nemotron-70b-instruct", []string{"NVIDIA_API_KEY"}, "nvidia nim"),
	anthropicCompat("minimax", "MiniMax", "https://api.minimax.io/anthropic", "MiniMax-M3", []string{"MINIMAX_API_KEY"}, "mini-max", "mini_max"),
	openAICompat("mistral", "Mistral", "https://api.mistral.ai/v1", "mistral-large-latest", []string{"MISTRAL_API_KEY"}),
	openAICompat("github", "GitHub Models", "https://models.inference.ai.azure.com", "openai/gpt-4.1", []string{"GITHUB_TOKEN"}, "github-models"),
	transportDescriptor("bedrock", "Amazon Bedrock", TransportBedrock, "https://bedrock-runtime.${AWS_REGION}.amazonaws.com", "anthropic.claude-3-5-sonnet-20241022-v2:0", []string{"AWS_ACCESS_KEY_ID", "AWS_PROFILE"}, []APIFormat{APIFormatBedrockConverse}, true),
	transportDescriptor("vertex", "Vertex AI", TransportVertex, "https://aiplatform.googleapis.com", "gemini-2.5-pro", []string{"GOOGLE_APPLICATION_CREDENTIALS"}, []APIFormat{APIFormatVertexGenerateContent}, true),
	openAICompat("xai", "xAI", "https://api.x.ai/v1", "grok-4", []string{"XAI_API_KEY"}),
	openAICompat("venice", "Venice AI", "https://api.venice.ai/api/v1", "qwen-2.5-qwq-32b", []string{"VENICE_API_KEY"}),
	openAICompat("xiaomi-mimo", "Xiaomi MiMo", "https://api.mimo.xiaomi.com/openai/v1", "mimo-vl", []string{"MIMO_API_KEY", "XIAOMI_API_KEY"}, "xiaomi mimo"),
	openAICompat("bankr", "Bankr", "https://api.bankr.bot/v1", "bankr-large", []string{"BANKR_API_KEY"}),
	openAICompat("zai", "Z.ai", "https://open.bigmodel.cn/api/paas/v4", "glm-4.5", []string{"ZAI_API_KEY", "ZHIPU_API_KEY"}),
	openAICompat("gitlawb-opengateway", "GitLawb OpenGateway", "https://gateway.gitlawb.com/v1", "gpt-4.1", []string{"GITLAWB_OPENGATEWAY_API_KEY"}, "gitlawb opengateway"),
	openAICompat("atomic-chat", "Atomic Chat", "https://api.atomic.chat/v1", "gpt-4.1", []string{"ATOMIC_CHAT_API_KEY"}),
	openAICompat("custom-openai-compatible", "Custom OpenAI-compatible", "https://example.invalid/v1", "custom-model", []string{"OPENAI_API_KEY"}, "custom openai compatible"),
	anthropicCompat("custom-anthropic-compatible", "Custom Anthropic-compatible", "https://example.invalid/anthropic", "custom-model", []string{"ANTHROPIC_API_KEY"}, "custom anthropic compatible"),
}

func All() []Descriptor {
	copied := make([]Descriptor, 0, len(descriptors))
	for _, descriptor := range descriptors {
		copied = append(copied, cloneDescriptor(descriptor))
	}
	return copied
}

func IDs() []string {
	ids := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		ids = append(ids, descriptor.ID)
	}
	return ids
}

func Get(id string) (Descriptor, bool) {
	normalized := NormalizeID(id)
	for _, descriptor := range descriptors {
		if descriptor.ID == normalized {
			return cloneDescriptor(descriptor), true
		}
		for _, alias := range descriptor.Aliases {
			if NormalizeID(alias) == normalized {
				return cloneDescriptor(descriptor), true
			}
		}
	}
	return Descriptor{}, false
}

func Require(id string) (Descriptor, error) {
	normalized := NormalizeID(id)
	descriptor, ok := Get(normalized)
	if !ok {
		return Descriptor{}, fmt.Errorf("%w %q", ErrUnknownProvider, normalized)
	}
	return descriptor, nil
}

func ListByTransport(transport Transport) []Descriptor {
	normalized := Transport(NormalizeID(string(transport)))
	items := make([]Descriptor, 0)
	for _, descriptor := range descriptors {
		if descriptor.Transport == normalized {
			items = append(items, cloneDescriptor(descriptor))
		}
	}
	return items
}

func ValidTransport(transport Transport) bool {
	switch Transport(NormalizeID(string(transport))) {
	case TransportOpenAI, TransportAnthropic, TransportGoogle, TransportBedrock, TransportVertex, TransportOpenAICompatible, TransportAnthropicCompatible:
		return true
	default:
		return false
	}
}

func ValidAPIFormat(format APIFormat) bool {
	switch format {
	case APIFormatOpenAIResponses, APIFormatOpenAIChatCompletions, APIFormatAnthropicMessages, APIFormatGoogleGenerateContent, APIFormatBedrockConverse, APIFormatVertexGenerateContent:
		return true
	default:
		return false
	}
}

func NormalizeID(id string) string {
	var builder strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(id)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			builder.WriteRune(r)
			lastDash = false
		default:
			if builder.Len() > 0 && !lastDash {
				builder.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(builder.String(), "-")
}

func openAI(id string, name string, baseURL string, model string, env []string, aliases ...string) Descriptor {
	return Descriptor{
		ID:                  id,
		Name:                name,
		Transport:           TransportOpenAI,
		DefaultBaseURL:      baseURL,
		DefaultModel:        model,
		AuthEnvVars:         env,
		RequiresAuth:        true,
		SupportedAPIFormats: []APIFormat{APIFormatOpenAIResponses, APIFormatOpenAIChatCompletions},
		Aliases:             aliases,
	}
}

func anthropic(id string, name string, baseURL string, model string, env []string, aliases ...string) Descriptor {
	return Descriptor{
		ID:                  id,
		Name:                name,
		Transport:           TransportAnthropic,
		DefaultBaseURL:      baseURL,
		DefaultModel:        model,
		AuthEnvVars:         env,
		RequiresAuth:        true,
		SupportedAPIFormats: []APIFormat{APIFormatAnthropicMessages},
		Aliases:             aliases,
	}
}

func google(id string, name string, baseURL string, model string, env []string, aliases ...string) Descriptor {
	return Descriptor{
		ID:                  id,
		Name:                name,
		Transport:           TransportGoogle,
		DefaultBaseURL:      baseURL,
		DefaultModel:        model,
		AuthEnvVars:         env,
		RequiresAuth:        true,
		SupportedAPIFormats: []APIFormat{APIFormatGoogleGenerateContent},
		Aliases:             aliases,
	}
}

func localOpenAI(id string, name string, baseURL string, model string, aliases ...string) Descriptor {
	descriptor := openAICompat(id, name, baseURL, model, nil, aliases...)
	descriptor.RequiresAuth = false
	descriptor.Local = true
	return descriptor
}

func openAICompat(id string, name string, baseURL string, model string, env []string, aliases ...string) Descriptor {
	return Descriptor{
		ID:                  id,
		Name:                name,
		Transport:           TransportOpenAICompatible,
		DefaultBaseURL:      baseURL,
		DefaultModel:        model,
		AuthEnvVars:         env,
		RequiresAuth:        len(env) > 0,
		SupportedAPIFormats: []APIFormat{APIFormatOpenAIChatCompletions},
		Aliases:             aliases,
	}
}

func anthropicCompat(id string, name string, baseURL string, model string, env []string, aliases ...string) Descriptor {
	return Descriptor{
		ID:                  id,
		Name:                name,
		Transport:           TransportAnthropicCompatible,
		DefaultBaseURL:      baseURL,
		DefaultModel:        model,
		AuthEnvVars:         env,
		RequiresAuth:        len(env) > 0,
		SupportedAPIFormats: []APIFormat{APIFormatAnthropicMessages},
		Aliases:             aliases,
	}
}

func transportDescriptor(id string, name string, transport Transport, baseURL string, model string, env []string, formats []APIFormat, ambient bool) Descriptor {
	return Descriptor{
		ID:                  id,
		Name:                name,
		Transport:           transport,
		DefaultBaseURL:      baseURL,
		DefaultModel:        model,
		AuthEnvVars:         env,
		RequiresAuth:        len(env) > 0 || ambient,
		UsesAmbientAuth:     ambient,
		SupportedAPIFormats: append([]APIFormat{}, formats...),
	}
}

func cloneDescriptor(descriptor Descriptor) Descriptor {
	descriptor.AuthEnvVars = append([]string{}, descriptor.AuthEnvVars...)
	descriptor.SupportedAPIFormats = append([]APIFormat{}, descriptor.SupportedAPIFormats...)
	descriptor.Aliases = append([]string{}, descriptor.Aliases...)
	return descriptor
}
