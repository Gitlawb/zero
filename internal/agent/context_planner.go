package agent

import (
	"strings"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type contextPlannerConfig struct {
	contextWindow  int
	promptCacheKey string
	promptParts    systemPromptParts
}

// contextPlanner is the single seam for constructing model-visible requests.
// Planning is deterministic and does not retrieve content, execute tools, or
// change permissions. The initial implementation deliberately preserves every
// message and tool definition while making composition and cache drift
// inspectable; later selection policies must continue to cross this seam.
type contextPlanner struct {
	config         contextPlannerConfig
	previousPrefix prefixFingerprint
	hasPrevious    bool
}

type contextPlan struct {
	Request           zeroruntime.CompletionRequest
	Breakdown         ContextBreakdown
	PrefixFingerprint prefixFingerprint
}

func newContextPlanner(config contextPlannerConfig) *contextPlanner {
	return &contextPlanner{config: config}
}

// Plan returns a provider request snapshot plus content-free accounting.
// It intentionally performs no relevance filtering: preserving current model
// capability is the baseline contract for future planner policies.
func (planner *contextPlanner) Plan(messages []zeroruntime.Message, toolDefs []zeroruntime.ToolDefinition, reasoningEffort string) contextPlan {
	request := zeroruntime.CompletionRequest{
		Messages:        copyMessages(messages),
		Tools:           toolDefs,
		ReasoningEffort: reasoningEffort,
		PromptCacheKey:  planner.config.promptCacheKey,
	}
	parts := planner.config.promptParts
	if parts.prompt == "" {
		parts.prompt = leadingSystemContent(request.Messages)
	}
	fingerprint := computePrefixFingerprint(buildPromptSubstringsFromParts(parts, request.Tools))
	breakdown := MeasureContext(request.Messages, request.Tools, planner.config.contextWindow)
	breakdown.CompletePrefixHash = fingerprint.CompletePrefixHash
	var previous *prefixFingerprint
	if planner.hasPrevious {
		previous = &planner.previousPrefix
	}
	breakdown.PrefixInvalidationReason = explainPrefixChange(previous, fingerprint)
	planner.previousPrefix = fingerprint
	planner.hasPrevious = true
	return contextPlan{
		Request:           request,
		Breakdown:         breakdown,
		PrefixFingerprint: fingerprint,
	}
}

func leadingSystemContent(messages []zeroruntime.Message) string {
	contents := make([]string, 0, 1)
	for _, message := range messages {
		if message.Role != zeroruntime.MessageRoleSystem {
			break
		}
		contents = append(contents, message.Content)
	}
	return strings.Join(contents, "\n\n")
}

func explainPrefixChange(previous *prefixFingerprint, current prefixFingerprint) string {
	if previous == nil {
		return "initial"
	}
	if previous.CompletePrefixHash == current.CompletePrefixHash {
		return "unchanged"
	}
	reasons := make([]string, 0, 3)
	if previous.SystemPromptHash != current.SystemPromptHash {
		parts := make([]string, 0, 4)
		if previous.BaseInstructionsHash != current.BaseInstructionsHash {
			parts = append(parts, "base_instructions")
		}
		if previous.ConfirmationPolicyHash != current.ConfirmationPolicyHash {
			parts = append(parts, "confirmation_policy")
		}
		if previous.ProjectContextHash != current.ProjectContextHash {
			parts = append(parts, "project_context")
		}
		if previous.SkillsHash != current.SkillsHash {
			parts = append(parts, "skills")
		}
		if len(parts) == 0 {
			parts = append(parts, "system_prompt")
		}
		reasons = append(reasons, parts...)
	}
	if previous.ToolsHash != current.ToolsHash {
		reasons = append(reasons, "tools")
	}
	if previous.SchemaHash != current.SchemaHash {
		reasons = append(reasons, "schema")
	}
	if len(reasons) == 0 {
		return "prefix_changed"
	}
	return strings.Join(reasons, ",")
}
