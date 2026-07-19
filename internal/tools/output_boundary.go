package tools

import (
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
	budget := registryOutputBudget(toolName)
	if budget.maxEstimatedTokens <= 0 && budget.hardMaxBytes <= 0 {
		return result // preserve ZERO_TOOL_OUTPUT_CEILING_TOKENS=0 semantics
	}

	category := resolveOutputCategory(tool, toolName, args)
	budgeted := budgetSemanticOutput(result.Output, category, budget)
	if budgeted.truncated {
		budgeted = attachExistingSpill(toolName, result.Output, budget, budgeted)
	}
	if result.Truncated {
		budgeted.truncated = true
		if budgeted.reason == "" {
			budgeted.reason = result.Meta["truncation_reason"]
			if budgeted.reason == "" {
				budgeted.reason = "upstream_tool_budget"
			}
		}
		if budgeted.spillPath == "" {
			budgeted.spillPath = result.Meta["spill_path"]
		}
	}
	result.Output = budgeted.text
	result.Truncated = result.Truncated || budgeted.truncated
	result.Meta = addOutputBudgetMetadata(result.Meta, budgeted)
	return result
}

// applySelfManagedOutputBudget applies semantic retention to tools that retain
// output with their own capture-aware limits. Their explicit budgets remain
// authoritative, rather than being replaced by the registry default ceiling.
func applySelfManagedOutputBudget(tool Tool, toolName string, args map[string]any, result Result) Result {
	budget := selfManagedOutputBudget(toolName, args)
	if budget.maxEstimatedTokens <= 0 && budget.hardMaxBytes <= 0 {
		return annotateSelfBudgetedOutput(tool, toolName, args, result)
	}

	category := resolveOutputCategory(tool, toolName, args)
	budgeted := budgetSemanticOutput(result.Output, category, budget)
	if result.Truncated && !budgeted.truncated {
		budgeted.truncated = true
		budgeted.reason = result.Meta["truncation_reason"]
		if budgeted.reason == "" {
			budgeted.reason = "upstream_tool_budget"
		}
	}
	if budgeted.truncated {
		budgeted = attachExistingSpill(toolName, result.Output, budget, budgeted)
	}
	result.Output = budgeted.text
	result.Truncated = result.Truncated || budgeted.truncated
	result.Meta = addOutputBudgetMetadata(result.Meta, budgeted)
	return result
}

func selfManagedOutputBudget(toolName string, args map[string]any) outputBudget {
	switch toolName {
	case "bash":
		// bash previously exposed at most 32 KiB from each stream. Keep that
		// combined 64 KiB ceiling while selecting its contents semantically.
		return outputBudget{maxEstimatedTokens: (bashOutputBudgetBytes * 2) / 4, hardMaxBytes: bashOutputBudgetBytes * 2}
	case ExecCommandToolName:
		maxOutputTokens := defaultMaxOutputTokens
		if parsed, err := intArg(args, "max_output_tokens", defaultMaxOutputTokens, 1, maxExecOutputTokenRequest); err == nil {
			maxOutputTokens = parsed
		}
		return outputBudget{maxEstimatedTokens: maxOutputTokens, hardMaxBytes: maxOutputTokens * 4}
	default:
		return outputBudget{}
	}
}

// RebudgetAfterHook reapplies the registry's redaction and output limits after
// an afterTool hook appends model-visible feedback to a completed result. The
// initial registry pass has already run; this second pass is limited to the
// newly combined result so hooks cannot bypass the established safety ceiling.
func (registry *Registry) RebudgetAfterHook(toolName string, args map[string]any, result Result) Result {
	result = scrubResultSecrets(result)
	tool, _ := registry.Get(toolName)
	if _, ok := tool.(selfBudgeting); ok {
		// Match the primary registry boundary: self-managed tools keep their
		// call-specific capture/output budget instead of being tightened or
		// loosened to the generic registry ceiling after hook feedback.
		return applySelfManagedOutputBudget(tool, toolName, args, result)
	}
	result = applyRegistryOutputBudget(tool, toolName, args, result)
	return enforceOutputCeiling(toolName, result)
}

func registryOutputBudget(toolName string) outputBudget {
	switch toolName {
	case "read_file", "read_minified_file":
		return outputBudget{maxEstimatedTokens: readOutputBudgetBytes / 4, hardMaxBytes: readOutputBudgetBytes}
	case "grep", "glob", "list_directory":
		return outputBudget{maxEstimatedTokens: searchOutputBudgetBytes / 4, hardMaxBytes: searchOutputBudgetBytes}
	default:
		ceilingTokens := resolveOutputCeilingTokens()
		if ceilingTokens <= 0 {
			return outputBudget{}
		}
		return outputBudget{maxEstimatedTokens: ceilingTokens, hardMaxBytes: ceilingTokens * 4}
	}
}

func resolveOutputCategory(tool Tool, toolName string, args map[string]any) outputCategory {
	if provider, ok := tool.(outputPolicyProvider); ok {
		if category := provider.outputCategory(args); category != "" {
			return category
		}
	}
	switch toolName {
	case "Task", "swarm_collect":
		return outputCategoryWorker
	case "apply_patch":
		return outputCategoryDiff
	case "write_stdin":
		return outputCategoryProcess
	default:
		return outputCategoryDefault
	}
}

// annotateSelfBudgetedOutput records the same compact metadata for tools whose
// existing capture/budget implementation remains authoritative in PR11. It
// does not re-budget their text or claim that raw_bytes represents every byte a
// subprocess produced beyond its established capture limits.
func annotateSelfBudgetedOutput(tool Tool, toolName string, args map[string]any, result Result) Result {
	retainedBytes := len(result.Output)
	originalBytes := retainedBytes
	if parsed, err := strconv.Atoi(result.Meta["raw_bytes"]); err == nil && parsed > originalBytes {
		originalBytes = parsed
	}
	retainedTokens := estimateOutputTokens(result.Output)
	originalTokens := retainedTokens
	if originalBytes > retainedBytes {
		originalTokens = max(originalTokens, estimatedTokensFromBytes(originalBytes))
	}
	reason := result.Meta["truncation_reason"]
	if result.Truncated && reason == "" {
		reason = "upstream_tool_budget"
	}
	observed := budgetedOutput{
		text:                    result.Output,
		originalBytes:           originalBytes,
		retainedBytes:           retainedBytes,
		estimatedOriginalTokens: originalTokens,
		estimatedRetainedTokens: retainedTokens,
		truncated:               result.Truncated,
		category:                resolveOutputCategory(tool, toolName, args),
		reason:                  reason,
		spillPath:               result.Meta["spill_path"],
	}
	result.Meta = addOutputBudgetMetadata(result.Meta, observed)
	return result
}

func shellOutputCategory(command string) outputCategory {
	normalized := strings.ToLower(strings.TrimSpace(command))
	if containsAny(normalized,
		"go test", "pytest", "python -m pytest", "cargo test", "npm test", "npm run test",
		"pnpm test", "yarn test", "bun test", "dotnet test", "mvn test", "gradle test", "phpunit") {
		return outputCategoryTest
	}
	if containsAny(normalized, "git diff", "git show", "git format-patch", " diff -u", "diff --git") || strings.HasPrefix(normalized, "diff ") {
		return outputCategoryDiff
	}
	return outputCategoryProcess
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
		current.spillPath = path
		return current
	}
	base.text = text
	base.retainedBytes = len(text)
	base.estimatedRetainedTokens = estimateOutputTokens(text)
	base.truncated = true
	if base.reason == "" {
		base.reason = current.reason
	}
	base.category = current.category
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
	// These established fields describe the final model-visible output. Refresh
	// them on every pass because redaction and after-tool hooks may change the
	// text even when this pass does not newly truncate it.
	meta["emitted_bytes"] = strconv.Itoa(output.retainedBytes)
	meta["estimated_tokens"] = strconv.Itoa(output.estimatedRetainedTokens)
	if output.reason != "" {
		meta[outputBudgetReasonMeta] = output.reason
	}
	if output.truncated {
		// Preserve the existing metadata vocabulary used by callers and tests.
		if _, exists := meta["raw_bytes"]; !exists {
			meta["raw_bytes"] = strconv.Itoa(output.originalBytes)
		}
		meta["truncated"] = "true"
		meta["truncation_reason"] = output.reason
	}
	if output.spillPath != "" {
		meta["spill_path"] = output.spillPath
	}
	return meta
}
