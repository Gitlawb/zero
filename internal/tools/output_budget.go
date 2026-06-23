package tools

import (
	"fmt"
	"strconv"
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
