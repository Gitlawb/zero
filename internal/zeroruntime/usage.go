package zeroruntime

import "fmt"

// NormalizeUsage converts provider token aliases into the shared runtime shape.
func NormalizeUsage(input TokenUsage) (Usage, error) {
	inputTokens, err := providerAlias(input.InputTokens, input.PromptTokens, "input tokens")
	if err != nil {
		return Usage{}, err
	}

	outputTokens, err := providerAlias(input.OutputTokens, input.CompletionTokens, "output tokens")
	if err != nil {
		return Usage{}, err
	}

	cachedInputTokens, err := nonNegative(input.CachedInputTokens, "cached input tokens")
	if err != nil {
		return Usage{}, err
	}
	if cachedInputTokens > inputTokens {
		cachedInputTokens = inputTokens
	}

	reasoningTokens, err := nonNegative(input.ReasoningTokens, "reasoning tokens")
	if err != nil {
		return Usage{}, err
	}

	return Usage{
		InputTokens:       inputTokens,
		OutputTokens:      outputTokens,
		PromptTokens:      inputTokens,
		CompletionTokens:  outputTokens,
		CachedInputTokens: cachedInputTokens,
		ReasoningTokens:   reasoningTokens,
	}, nil
}

func providerAlias(primary int, alias int, label string) (int, error) {
	if _, err := nonNegative(primary, label); err != nil {
		return 0, err
	}
	if _, err := nonNegative(alias, label+" alias"); err != nil {
		return 0, err
	}
	if primary != 0 {
		return primary, nil
	}
	return alias, nil
}

func nonNegative(value int, label string) (int, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s must be non-negative", label)
	}
	return value, nil
}
