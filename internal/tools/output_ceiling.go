package tools

import (
	"os"
	"strconv"
	"strings"
)

// Universal output ceiling. Every tool result leaving the registry boundary is
// capped at a single token-denominated budget unless the tool manages its own
// deliberate budget (selfBudgeting below). This is the safety net for tools
// with no cap of their own — web_fetch bodies, skill files, MCP server tools,
// browser snapshots, anything added later — so no single call can flood the
// context window with output that is then re-billed on every following turn.
// Truncation keeps head+tail and spills the full text to disk (re-readable via
// grep/read_file), so nothing is lost, only deferred.

// defaultOutputCeilingTokens is the per-call ceiling in estimated tokens
// (bytes/4). 16k tokens = 64 KiB — deliberately equal to the search-tool
// budget: generous enough for a large fetched document, small enough that one
// call cannot eat a third of a small context window.
const defaultOutputCeilingTokens = 16_000

// outputCeilingEnv overrides the ceiling (in tokens). Zero or negative
// disables the ceiling entirely; unset or unparsable keeps the default.
const outputCeilingEnv = "ZERO_TOOL_OUTPUT_CEILING_TOKENS"

// selfBudgeting marks a tool with a deliberate capture-aware output budget —
// possibly model-raisable (exec_command). The registry applies semantic
// retention within that explicit budget rather than replacing it with the
// default ceiling. The method is unexported so an MCP-served tool cannot opt
// into this path.
type selfBudgeting interface{ managesOutputBudget() }

// The list is kept in one place. Shell/process tools retain their established
// capture-aware budgets: bash (per-stream bounded capture) and exec_command
// (model-raisable bounded session output). File/search tools use the standard
// shared post-redaction boundary.
func (bashTool) managesOutputBudget()        {}
func (execCommandTool) managesOutputBudget() {}

func resolveOutputCeilingTokens() int {
	raw := strings.TrimSpace(os.Getenv(outputCeilingEnv))
	if raw == "" {
		return defaultOutputCeilingTokens
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return defaultOutputCeilingTokens
	}
	return parsed
}

// enforceOutputCeiling truncates an over-ceiling result to head+tail within
// the token budget, spilling the full (already redaction-scrubbed) output to
// disk with a recovery hint. Runs after scrubResultSecrets so the transcript
// and the spill file agree on what was hidden.
func enforceOutputCeiling(toolName string, result Result) Result {
	ceiling := resolveOutputCeilingTokens()
	switch toolName {
	case "read_file", "read_minified_file":
		ceiling = readOutputBudgetBytes / 4
	case "grep", "glob", "list_directory":
		ceiling = searchOutputBudgetBytes / 4
	}
	if ceiling <= 0 {
		return result
	}
	if len(result.Output) <= ceiling*4 {
		return result
	}
	rawBytes := len(result.Output)
	truncated, _ := truncateExecOutputSpill(result.Output, ceiling, toolName)
	result.Output = truncated
	result.Truncated = true
	if result.Meta == nil {
		result.Meta = map[string]string{}
	}
	result.Meta["raw_bytes"] = strconv.Itoa(rawBytes)
	result.Meta["emitted_bytes"] = strconv.Itoa(len(result.Output))
	result.Meta["estimated_tokens"] = strconv.Itoa(estimatedTokensFromBytes(len(result.Output)))
	return result
}
