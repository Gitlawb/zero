package tools

import (
	"strings"
)

const humanOutcomePreviewBytes = 8 * 1024

// finalizeToolOutcome is the single seam between tool execution and its three
// consumers: provider context, human presentation, and recoverable artifacts.
// boundaryOutput must already be redacted. It is the text seen immediately
// before command reduction and semantic budgeting.
func finalizeToolOutcome(result Result, boundaryOutput string) Result {
	previous := result.Outcome
	human := result.Display
	if human.Preview == "" && result.Meta["command_output_reduced"] == "true" {
		human.Preview = boundedHumanOutcomePreview(boundaryOutput, result.Meta["spill_path"])
	}
	// A tool-provided success preview (for example, a prospective file diff)
	// must not hide the actual error. Command reduction is different: its
	// preview is the redacted execution output containing that same failure plus
	// the evidence omitted from the model view.
	if result.Status == StatusError && result.Meta["command_output_reduced"] != "true" {
		human.Preview = ""
	}

	originalBytes := len(boundaryOutput)
	originalTokens := estimateOutputTokens(boundaryOutput)
	if previous.Finalized() {
		originalBytes = previous.Diagnostics.OriginalBytes
		originalTokens = previous.Diagnostics.EstimatedOriginalTokens
	}
	modelBytes := len(result.Output)
	modelTokens := estimateOutputTokens(result.Output)

	var artifact *ToolArtifact
	if previous.Finalized() {
		artifact = previous.Artifact
	}
	if path := strings.TrimSpace(result.Meta["spill_path"]); path != "" {
		if artifact == nil || artifact.Path != path {
			artifact = &ToolArtifact{
				Path:               path,
				CompleteAtBoundary: result.Meta["command_output_reduced"] == "true" || result.Meta[outputBudgetSpillCreatedMeta] == "true",
			}
		}
	}

	result.Display = human
	result.Outcome = ToolOutcome{
		ModelView: result.Output,
		HumanView: human,
		Artifact:  artifact,
		Diagnostics: OutcomeDiagnostics{
			Category:                result.Meta[outputBudgetCategoryMeta],
			OriginalBytes:           originalBytes,
			ModelBytes:              modelBytes,
			EstimatedOriginalTokens: originalTokens,
			EstimatedModelTokens:    modelTokens,
			Truncated:               result.Truncated,
			Redacted:                result.Redacted,
			Reason:                  result.Meta["truncation_reason"],
		},
		finalized: true,
	}
	return result
}

func boundedHumanOutcomePreview(output string, artifactPath string) string {
	if len(output) <= humanOutcomePreviewBytes {
		return output
	}
	marker := "\n[zero] human preview shortened"
	if strings.TrimSpace(artifactPath) != "" {
		marker += "; exact output: " + artifactPath
	}
	marker += "\n"
	contentBudget := max(0, humanOutcomePreviewBytes-len(marker))
	return utf8Prefix(output, contentBudget*3/5) + marker + utf8Suffix(output, contentBudget*2/5)
}
