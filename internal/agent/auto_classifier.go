package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// autoPermissionClassifierSystemPrompt is the dedicated system prompt for the
// LLM permission classifier used by PermissionModeAutoClassifier.
//
//go:embed auto_classifier_prompt.md
var autoPermissionClassifierSystemPrompt string

const autoPermissionClassifierTimeout = 10 * time.Second

func defaultAutoPermissionClassifier(provider Provider) AutoPermissionClassifier {
	return func(ctx context.Context, request AutoPermissionClassifierRequest) (AutoPermissionClassifierDecision, error) {
		return classifyAutoPermissionWithProvider(ctx, provider, request)
	}
}

func classifyAutoPermissionWithProvider(ctx context.Context, provider Provider, request AutoPermissionClassifierRequest) (AutoPermissionClassifierDecision, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return AutoPermissionClassifierDecision{}, err
	}
	classifierCtx, cancel := context.WithTimeout(ctx, autoPermissionClassifierTimeout)
	defer cancel()
	systemPrompt := strings.TrimSpace(autoPermissionClassifierSystemPrompt)
	stream, err := provider.StreamCompletion(classifierCtx, zeroruntime.CompletionRequest{
		Messages: []zeroruntime.Message{
			{Role: zeroruntime.MessageRoleSystem, Content: systemPrompt},
			{Role: zeroruntime.MessageRoleUser, Content: string(payload)},
		},
		Tools: nil,
	})
	if err != nil {
		return AutoPermissionClassifierDecision{}, err
	}
	collected := zeroruntime.CollectStream(classifierCtx, stream)
	if collected.Error != "" {
		return AutoPermissionClassifierDecision{}, errors.New(collected.Error)
	}
	if collected.FinishReason != "" {
		return AutoPermissionClassifierDecision{}, errors.New("auto-classifier response ended early: " + collected.FinishReason)
	}
	decision, ok := parseAutoPermissionClassifierDecision(collected.Text)
	if !ok {
		return AutoPermissionClassifierDecision{}, errors.New("invalid auto-classifier response")
	}
	return decision, nil
}

func parseAutoPermissionClassifierDecision(output string) (AutoPermissionClassifierDecision, bool) {
	output = normalizeAutoPermissionClassifierOutput(output)
	if output == "" {
		return AutoPermissionClassifierDecision{}, false
	}
	var raw struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return AutoPermissionClassifierDecision{}, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return AutoPermissionClassifierDecision{}, false
	}
	decision := AutoPermissionClassifierDecision{Reason: strings.TrimSpace(raw.Reason)}
	switch AutoPermissionClassifierAction(strings.TrimSpace(raw.Action)) {
	case AutoPermissionClassifierAllow:
		decision.Action = AutoPermissionClassifierAllow
	case AutoPermissionClassifierPrompt:
		decision.Action = AutoPermissionClassifierPrompt
	default:
		return AutoPermissionClassifierDecision{}, false
	}
	if decision.Reason == "" {
		return AutoPermissionClassifierDecision{}, false
	}
	return decision, true
}

func normalizeAutoPermissionClassifierOutput(output string) string {
	output = strings.TrimSpace(output)
	if !strings.HasPrefix(output, "```") {
		return output
	}
	lines := strings.Split(output, "\n")
	if len(lines) < 2 || strings.TrimSpace(lines[len(lines)-1]) != "```" {
		return output
	}
	return strings.TrimSpace(strings.Join(lines[1:len(lines)-1], "\n"))
}
