package tools

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	outputBudgetCategoryMeta                = "output_budget_category"
	outputBudgetOriginalBytesMeta           = "output_budget_original_bytes"
	outputBudgetRetainedBytesMeta           = "output_budget_retained_bytes"
	outputBudgetEstimatedOriginalTokensMeta = "output_budget_estimated_original_tokens"
	outputBudgetEstimatedRetainedTokensMeta = "output_budget_estimated_retained_tokens"
	outputBudgetReasonMeta                  = "output_budget_reason"
	outputBudgetSpillCreatedMeta            = "output_budget_spill_created"
)

// applyRegistryOutputBudget is the common post-redaction semantic budgeting
// boundary for tools that do not already own a deliberate output budget.
func applyRegistryOutputBudget(tool Tool, toolName string, args map[string]any, result Result) Result {
	ceilingTokens := resolveOutputCeilingTokens()
	if ceilingTokens <= 0 {
		return result // preserve ZERO_TOOL_OUTPUT_CEILING_TOKENS=0 semantics
	}

	category := outputCategoryDefault
	if provider, ok := tool.(outputPolicyProvider); ok {
		category = provider.outputCategory(args)
	}
	budget := outputBudget{
		maxEstimatedTokens: ceilingTokens,
		hardMaxBytes:       ceilingTokens * 4,
	}
	budgeted := budgetSemanticOutput(result.Output, category, budget)
	if budgeted.truncated {
		budgeted = attachExistingSpill(toolName, result.Output, budget, budgeted)
	}
	result.Output = budgeted.text
	result.Truncated = result.Truncated || budgeted.truncated
	result.Meta = addOutputBudgetMetadata(result.Meta, budgeted)
	return result
}

// attachExistingSpill reuses Zero's hardened per-user spill directory. output
// is the already-redacted text received by this layer; it may itself be a
// capture-bounded view produced by a subprocess tool.
func attachExistingSpill(toolName, output string, budget outputBudget, current budgetedOutput) budgetedOutput {
	path := spillTruncatedOutput(toolName, output)
	if path == "" {
		return current
	}
	notice := "[zero] full output received by budgeting layer saved to " + path + " (grep or read_file it instead of re-running)"
	reduced := outputBudget{
		maxEstimatedTokens: max(1, budget.maxEstimatedTokens-estimateOutputTokens("\n"+notice)),
		hardMaxBytes:       max(1, budget.hardMaxBytes-len("\n"+notice)),
	}
	base := budgetSemanticOutput(output, current.category, reduced)
	text := strings.TrimRight(base.text, "\n") + "\n" + notice
	if !fitsOutputBudget(text, budget) {
		// An unusually long temp path or tiny configured ceiling can leave no
		// room for the full notice. Keep the safe bounded result; the spill still
		// exists but is intentionally not advertised with a chopped reference.
		return current
	}
	base.text = text
	base.retainedBytes = len(text)
	base.estimatedRetainedTokens = estimateOutputTokens(text)
	base.spillPath = path
	return base
}

func addOutputBudgetMetadata(meta map[string]string, output budgetedOutput) map[string]string {
	if meta == nil {
		meta = map[string]string{}
	}
	meta[outputBudgetCategoryMeta] = string(output.category)
	meta[outputBudgetOriginalBytesMeta] = strconv.Itoa(output.originalBytes)
	meta[outputBudgetRetainedBytesMeta] = strconv.Itoa(output.retainedBytes)
	meta[outputBudgetEstimatedOriginalTokensMeta] = strconv.Itoa(output.estimatedOriginalTokens)
	meta[outputBudgetEstimatedRetainedTokensMeta] = strconv.Itoa(output.estimatedRetainedTokens)
	meta[outputBudgetSpillCreatedMeta] = strconv.FormatBool(output.spillPath != "")
	if output.reason != "" {
		meta[outputBudgetReasonMeta] = output.reason
	}
	if output.truncated {
		// Preserve the existing metadata vocabulary used by callers and tests.
		meta["raw_bytes"] = strconv.Itoa(output.originalBytes)
		meta["emitted_bytes"] = strconv.Itoa(output.retainedBytes)
		meta["estimated_tokens"] = strconv.Itoa(output.estimatedRetainedTokens)
		meta["truncated"] = "true"
		meta["truncation_reason"] = output.reason
	}
	if output.spillPath != "" {
		meta["spill_path"] = output.spillPath
	}
	return meta
}

func outputBudgetDebugString(output budgetedOutput) string {
	return fmt.Sprintf("category=%s original=%d retained=%d truncated=%t reason=%s", output.category, output.originalBytes, output.retainedBytes, output.truncated, output.reason)
}
