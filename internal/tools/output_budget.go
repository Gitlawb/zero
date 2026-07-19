package tools

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	readOutputBudgetBytes   = 128 * 1024
	searchOutputBudgetBytes = 64 * 1024
)

// outputCategory selects the deterministic retention policy used for a tool
// result. It is deliberately internal: categories are an implementation detail
// of the tools boundary, not part of Zero's public tool protocol.
type outputCategory string

const (
	outputCategoryDefault outputCategory = "default"
	outputCategoryFile    outputCategory = "file"
	outputCategorySearch  outputCategory = "search"
	outputCategoryTest    outputCategory = "test"
	outputCategoryProcess outputCategory = "process"
	outputCategoryDiff    outputCategory = "diff"
	outputCategoryWorker  outputCategory = "worker"
)

// outputBudget combines a provider-neutral estimated-token target with the
// existing byte ceiling. The byte ceiling is always authoritative: semantic
// retention may use fewer bytes, but can never use more.
type outputBudget struct {
	maxEstimatedTokens int
	hardMaxBytes       int
}

// budgetedOutput describes the text retained by a semantic output policy. The
// original size is the output received by this layer; it does not claim to be
// every byte an underlying subprocess may have produced before its own capture
// limits were applied.
type budgetedOutput struct {
	text                    string
	originalBytes           int
	retainedBytes           int
	estimatedOriginalTokens int
	estimatedRetainedTokens int
	truncated               bool
	category                outputCategory
	reason                  string
	spillPath               string
}

// outputPolicyProvider optionally assigns a semantic category to a tool call.
// Tools that do not implement it use outputCategoryDefault.
type outputPolicyProvider interface {
	outputCategory(args map[string]any) outputCategory
}

// estimateOutputTokens is a deterministic, provider-neutral estimate used only
// for output budgeting. It is not exact provider tokenization. ASCII non-space
// text uses the repository's established four-bytes-per-token approximation;
// each byte of non-ASCII UTF-8 is counted as a token, intentionally
// overestimating multilingual text and emoji rather than letting them bypass a
// budget. Existing hard byte ceilings remain the final safety limit.
func estimateOutputTokens(value string) int {
	asciiNonSpace, nonASCIIBytes := outputTokenComponents(value)
	return (asciiNonSpace+3)/4 + nonASCIIBytes
}

func outputTokenComponents(value string) (asciiNonSpace int, nonASCIIBytes int) {
	for index := 0; index < len(value); {
		r, size := utf8.DecodeRuneInString(value[index:])
		if r == utf8.RuneError && size == 1 {
			// Invalid input is charged conservatively one token per byte. Budget
			// slicing itself remains rune-safe for valid UTF-8 tool output.
			nonASCIIBytes++
			index++
			continue
		}
		if r < utf8.RuneSelf {
			switch r {
			case ' ', '\t', '\n', '\r', '\f', '\v':
			default:
				asciiNonSpace++
			}
		} else {
			nonASCIIBytes += size
		}
		index += size
	}
	return asciiNonSpace, nonASCIIBytes
}

const defaultSemanticTruncationNotice = "\n[zero] output truncated\n"

// budgetDefaultOutput applies the safe fallback policy: retain a rune-safe head
// and tail around a stable truncation notice. Small output is returned
// byte-identically. Spill creation is intentionally handled by the shared
// boundary integration so every semantic policy uses the existing spill path.
func budgetDefaultOutput(output string, budget outputBudget) budgetedOutput {
	result := budgetedOutput{
		text:                    output,
		originalBytes:           len(output),
		retainedBytes:           len(output),
		estimatedOriginalTokens: estimateOutputTokens(output),
		estimatedRetainedTokens: estimateOutputTokens(output),
		category:                outputCategoryDefault,
	}
	if fitsOutputBudget(output, budget) {
		return result
	}

	reason := outputBudgetReason(result.originalBytes, result.estimatedOriginalTokens, budget)
	maxContentBytes := len(output)
	if budget.hardMaxBytes > 0 {
		maxContentBytes = min(maxContentBytes, max(0, budget.hardMaxBytes-len(defaultSemanticTruncationNotice)))
	}

	// Find the largest deterministic head+tail window that satisfies both the
	// estimate and the hard byte ceiling. fitsOutputBudget is monotonic as this
	// window grows, so binary search avoids repeatedly trimming one rune at a time.
	low, high := 0, maxContentBytes
	best := ""
	if fitsOutputBudget(defaultSemanticTruncationNotice, budget) {
		best = defaultSemanticTruncationNotice
	}
	for low <= high {
		window := low + (high-low)/2
		candidate := defaultHeadTail(output, window)
		if fitsOutputBudget(candidate, budget) {
			best = candidate
			low = window + 1
		} else {
			high = window - 1
		}
	}

	result.text = best
	result.retainedBytes = len(best)
	result.estimatedRetainedTokens = estimateOutputTokens(best)
	result.truncated = true
	result.reason = reason
	return result
}

func defaultHeadTail(output string, contentBytes int) string {
	if contentBytes <= 0 {
		return defaultSemanticTruncationNotice
	}
	headBytes := contentBytes / 2
	tailBytes := contentBytes - headBytes
	return utf8Prefix(output, headBytes) + defaultSemanticTruncationNotice + utf8Suffix(output, tailBytes)
}

func fitsOutputBudget(output string, budget outputBudget) bool {
	if budget.hardMaxBytes > 0 && len(output) > budget.hardMaxBytes {
		return false
	}
	return budget.maxEstimatedTokens <= 0 || estimateOutputTokens(output) <= budget.maxEstimatedTokens
}

func outputBudgetReason(originalBytes int, estimatedTokens int, budget outputBudget) string {
	overTokens := budget.maxEstimatedTokens > 0 && estimatedTokens > budget.maxEstimatedTokens
	overBytes := budget.hardMaxBytes > 0 && originalBytes > budget.hardMaxBytes
	switch {
	case overTokens && overBytes:
		return "token_and_byte_budget"
	case overTokens:
		return "estimated_token_budget"
	default:
		return "hard_byte_ceiling"
	}
}

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
	capture  *boundedBuffer
	rawBytes int
	maxBytes int
	hint     string
}

func newOutputBudgetBuilder(maxBytes int, hint string) *outputBudgetBuilder {
	return &outputBudgetBuilder{maxBytes: maxBytes, hint: hint}
}

// newHeadTailOutputBudgetBuilder bounds capture while retaining both ends of
// an input for a later semantic policy. It is used for registry-driven file
// reads; direct Tool.Run calls keep the legacy prefix-only builder above.
func newHeadTailOutputBudgetBuilder(maxBytes int, hint string) *outputBudgetBuilder {
	headBytes := maxBytes / 2
	return &outputBudgetBuilder{
		capture:  newBoundedBuffer(headBytes, maxBytes-headBytes),
		maxBytes: maxBytes,
		hint:     hint,
	}
}

func (builder *outputBudgetBuilder) WriteString(value string) {
	builder.rawBytes += len(value)
	if builder.capture != nil {
		_, _ = builder.capture.Write([]byte(value))
		return
	}
	if builder.maxBytes <= 0 {
		builder.builder.WriteString(value)
		return
	}
	if builder.builder.Len() >= builder.maxBytes {
		return
	}
	remaining := builder.maxBytes - builder.builder.Len()
	builder.builder.WriteString(utf8Prefix(value, remaining))
}

// applyLegacyByteBudgetToResult preserves direct Tool.Run behavior for callers
// that intentionally bypass Registry.RunWithOptions. Agent/MCP execution uses
// the registry's post-redaction semantic boundary instead.
func applyLegacyByteBudgetToResult(result Result, maxBytes int, hint string) Result {
	budgeted := applyOutputBudget(result.Output, maxBytes, hint)
	result.Output = budgeted.Output
	result.Truncated = result.Truncated || budgeted.Truncated
	if result.Meta == nil {
		result.Meta = map[string]string{}
	}
	for key, value := range outputBudgetMeta(budgeted) {
		result.Meta[key] = value
	}
	if budgeted.Truncated {
		result.Meta["truncated"] = "true"
		result.Meta["truncation_reason"] = "byte_budget"
	}
	return result
}

func (builder *outputBudgetBuilder) Result() outputBudgetResult {
	output := builder.builder.String()
	if builder.capture != nil {
		output = builder.capture.retained()
	}
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
	if builder.capture != nil {
		headBytes := budget / 2
		result.Output = utf8Prefix(output, headBytes) + marker + utf8Suffix(output, budget-headBytes)
	} else {
		result.Output = utf8Prefix(output, budget) + marker
	}
	result.Truncated = true
	result.EmittedBytes = len(result.Output)
	return result
}
