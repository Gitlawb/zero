package agent

// BuildSystemPromptPreview returns the same system prompt a run would seed for
// the supplied options. It is intended for non-runtime reporting surfaces that
// need to estimate prompt footprint without starting a provider call.
func BuildSystemPromptPreview(options Options) string {
	return buildSystemPrompt(options)
}
