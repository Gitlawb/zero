package tools

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	readOutputBudgetBytes   = 128 * 1024
	searchOutputBudgetBytes = 64 * 1024
)

type outputBudgetResult struct {
	Output       string
	Truncated    bool
	RawBytes     int
	EmittedBytes int
}

func applyOutputBudget(output string, maxBytes int, hint string) outputBudgetResult {
	result := outputBudgetResult{
		Output:       output,
		RawBytes:     len(output),
		EmittedBytes: len(output),
	}
	if maxBytes <= 0 || len(output) <= maxBytes {
		return result
	}

	marker := fmt.Sprintf("\n\n[truncated: output exceeded %d bytes; %s]", maxBytes, hint)
	budget := maxBytes - len(marker)
	if budget < 0 {
		budget = 0
	}
	result.Output = utf8Prefix(output, budget) + marker
	result.Truncated = true
	result.EmittedBytes = len(result.Output)
	return result
}

func outputBudgetMeta(result outputBudgetResult) map[string]string {
	return map[string]string{
		"raw_bytes":        strconv.Itoa(result.RawBytes),
		"emitted_bytes":    strconv.Itoa(result.EmittedBytes),
		"estimated_tokens": strconv.Itoa(estimatedTokensFromBytes(result.EmittedBytes)),
	}
}

func estimatedTokensFromBytes(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	return (bytes + 3) / 4
}

type outputBudgetBuilder struct {
	builder  strings.Builder
	rawBytes int
	maxBytes int
	hint     string
}

func newOutputBudgetBuilder(maxBytes int, hint string) *outputBudgetBuilder {
	return &outputBudgetBuilder{maxBytes: maxBytes, hint: hint}
}

func (builder *outputBudgetBuilder) WriteString(value string) {
	builder.rawBytes += len(value)
	if builder.maxBytes <= 0 || builder.builder.Len() >= builder.maxBytes {
		return
	}
	remaining := builder.maxBytes - builder.builder.Len()
	builder.builder.WriteString(utf8Prefix(value, remaining))
}

func (builder *outputBudgetBuilder) Result() outputBudgetResult {
	output := builder.builder.String()
	result := outputBudgetResult{
		Output:       output,
		RawBytes:     builder.rawBytes,
		EmittedBytes: len(output),
	}
	if builder.maxBytes <= 0 || builder.rawBytes <= builder.maxBytes {
		return result
	}

	marker := fmt.Sprintf("\n\n[truncated: output exceeded %d bytes; %s]", builder.maxBytes, builder.hint)
	budget := builder.maxBytes - len(marker)
	if budget < 0 {
		budget = 0
	}
	result.Output = utf8Prefix(output, budget) + marker
	result.Truncated = true
	result.EmittedBytes = len(result.Output)
	return result
}
