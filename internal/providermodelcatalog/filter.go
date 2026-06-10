package providermodelcatalog

import "strings"

func IsCodingModel(model Model) bool {
	if IsKnownNonCodingModelID(model.ID) {
		return false
	}
	if len(model.OutputModalities) > 0 && !containsFold(model.OutputModalities, "text") {
		return false
	}
	if hasCodingTag(model.Tags) || model.ToolCall || model.Reasoning {
		return true
	}
	return LooksLikeCodingModelID(model.ID) || LooksLikeCodingModelID(model.Description)
}

func LooksLikeCodingModelID(id string) bool {
	normalized := strings.ToLower(strings.TrimSpace(id))
	if normalized == "" || IsKnownNonCodingModelID(normalized) {
		return false
	}
	for _, prefix := range []string{"o1", "o3", "o4", "o5"} {
		if normalized == prefix || strings.HasPrefix(normalized, prefix+"-") {
			return true
		}
	}
	for _, term := range []string{
		"gpt", "claude", "sonnet", "opus", "haiku", "gemini", "gemma",
		"llama", "qwen", "deepseek", "kimi", "moonshot", "minimax",
		"mistral", "codestral", "devstral", "magistral", "ministral",
		"grok", "glm", "command", "nemotron", "mixtral", "coder",
		"code", "chat", "instruct", "reasoner", "reasoning", "mimo",
		"maverick", "scout", "bankr",
	} {
		if strings.Contains(normalized, term) {
			return true
		}
	}
	return false
}

func IsKnownNonCodingModelID(id string) bool {
	normalized := strings.ToLower(strings.TrimSpace(id))
	if normalized == "" {
		return false
	}
	for _, term := range []string{
		"audio", "dall-e", "deep-research", "embedding", "image",
		"moderation", "realtime", "rerank", "sora", "speech",
		"transcribe", "translate", "tts", "whisper",
	} {
		if strings.Contains(normalized, term) {
			return true
		}
	}
	return false
}

func hasCodingTag(tags []string) bool {
	for _, tag := range tags {
		normalized := strings.ToLower(strings.TrimSpace(tag))
		switch normalized {
		case "agentic", "chat", "code", "coder", "coding", "instruct", "reasoning", "tools":
			return true
		}
	}
	return false
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}
