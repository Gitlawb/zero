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

// selfBudgeting marks a tool that enforces its own deliberate output budget —
// possibly model-raisable (exec_command) — which the registry ceiling must not
// second-guess. The method is unexported on purpose: only tools in this
// package can opt out, so an MCP-served tool can never exempt itself.
type selfBudgeting interface{ managesOutputBudget() }

// The exemption list, kept in one place. Each of these applies its own budget
// before returning: bash (bashOutputBudgetBytes per stream + spill),
// exec_command (model-raisable token budget + spill), read tools (128 KiB),
// search tools (64 KiB).
func (bashTool) managesOutputBudget()             {}
func (execCommandTool) managesOutputBudget()      {}
func (readFileTool) managesOutputBudget()         {}
func (readMinifiedFileTool) managesOutputBudget() {}
func (grepTool) managesOutputBudget()             {}
func (globTool) managesOutputBudget()             {}
func (listDirectoryTool) managesOutputBudget()    {}

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
