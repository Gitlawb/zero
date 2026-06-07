package agent

import (
	"encoding/json"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Context budget.
//
// MeasureContext breaks a request's estimated token footprint into the
// categories that actually compete for the window — the system prompt, the
// advertised tool definitions, and the conversation messages — so the agent can
// report context utilization (e.g. "62% used: 4k system, 6k tools, 30k history")
// and reason about what to compact. Counts are estimates (see estimateTokens),
// not a real tokenizer: good for proportions and budget bars, not billing.

// ContextBreakdown is a per-category estimate of a request's token footprint.
type ContextBreakdown struct {
	SystemTokens  int     // leading system messages (prompt + injected context)
	ToolTokens    int     // advertised tool definitions
	MessageTokens int     // non-system conversation messages
	TotalTokens   int     // SystemTokens + ToolTokens + MessageTokens
	ContextWindow int     // model context window; 0 when unknown
	UsedFraction  float64 // TotalTokens / ContextWindow; 0 when window unknown
}

// MeasureContext estimates the per-category token footprint of a request: the
// leading system messages, the advertised tool definitions, and the remaining
// conversation messages.
func MeasureContext(messages []zeroruntime.Message, tools []zeroruntime.ToolDefinition, contextWindow int) ContextBreakdown {
	systemEnd := 0
	for systemEnd < len(messages) && messages[systemEnd].Role == zeroruntime.MessageRoleSystem {
		systemEnd++
	}

	breakdown := ContextBreakdown{
		SystemTokens:  estimateTokens(messages[:systemEnd]),
		MessageTokens: estimateTokens(messages[systemEnd:]),
		ToolTokens:    estimateToolTokens(tools),
		ContextWindow: contextWindow,
	}
	breakdown.TotalTokens = breakdown.SystemTokens + breakdown.ToolTokens + breakdown.MessageTokens
	if contextWindow > 0 {
		breakdown.UsedFraction = float64(breakdown.TotalTokens) / float64(contextWindow)
	}
	return breakdown
}

// estimateToolTokens approximates the token footprint of advertised tool
// definitions (name + description + JSON schema), matching estimateTokens'
// ~4-chars/token heuristic so all categories use the same scale.
func estimateToolTokens(tools []zeroruntime.ToolDefinition) int {
	total := 0
	for _, tool := range tools {
		total += len(tool.Name) / 4
		total += len(tool.Description) / 4
		if len(tool.Parameters) > 0 {
			if encoded, err := json.Marshal(tool.Parameters); err == nil {
				total += len(encoded) / 4
			}
		}
		total += 4 // per-tool overhead
	}
	return total
}
