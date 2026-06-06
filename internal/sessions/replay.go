package sessions

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Gitlawb/zero/internal/redaction"
)

type EventRef struct {
	ID        string    `json:"id"`
	Sequence  int       `json:"sequence"`
	Type      EventType `json:"type"`
	CreatedAt string    `json:"createdAt,omitempty"`
}

type RewindOptions struct {
	TargetSequence int
	TargetEventID  string
	KeepTarget     bool
}

type RewindPlan struct {
	SessionID       string     `json:"sessionId"`
	TargetSequence  int        `json:"targetSequence"`
	TargetEventID   string     `json:"targetEventId"`
	KeepTarget      bool       `json:"keepTarget"`
	KeptCount       int        `json:"keptCount"`
	DroppedCount    int        `json:"droppedCount"`
	LastKeptEventID string     `json:"lastKeptEventId,omitempty"`
	KeptEvents      []EventRef `json:"keptEvents"`
	DroppedEvents   []EventRef `json:"droppedEvents"`
}

type CompactionOptions struct {
	PreserveLast   int
	MaxPromptChars int
}

type CompactionPlan struct {
	SessionID         string     `json:"sessionId"`
	PreserveLast      int        `json:"preserveLast"`
	CompactableCount  int        `json:"compactableCount"`
	PreservedCount    int        `json:"preservedCount"`
	CompactableEvents []EventRef `json:"compactableEvents"`
	PreservedEvents   []EventRef `json:"preservedEvents"`
	SummaryPrompt     string     `json:"summaryPrompt"`
	PromptChars       int        `json:"promptChars"`
	Truncated         bool       `json:"truncated,omitempty"`
}

const defaultCompactionPreserveLast = 8
const defaultCompactionMaxPromptChars = 8000

func (store *Store) PlanRewind(sessionID string, options RewindOptions) (RewindPlan, error) {
	if !ValidSessionID(sessionID) {
		return RewindPlan{}, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		return RewindPlan{}, err
	}
	if len(events) == 0 {
		return RewindPlan{}, fmt.Errorf("zero session %s has no events to rewind", sessionID)
	}
	targetIndex, err := findRewindTarget(events, options)
	if err != nil {
		return RewindPlan{}, err
	}
	target := events[targetIndex]
	cutoff := targetIndex
	if options.KeepTarget {
		cutoff = targetIndex + 1
	}
	kept := events[:cutoff]
	dropped := events[cutoff:]
	plan := RewindPlan{
		SessionID:      sessionID,
		TargetSequence: target.Sequence,
		TargetEventID:  target.ID,
		KeepTarget:     options.KeepTarget,
		KeptCount:      len(kept),
		DroppedCount:   len(dropped),
		KeptEvents:     eventRefs(kept),
		DroppedEvents:  eventRefs(dropped),
	}
	if len(kept) > 0 {
		plan.LastKeptEventID = kept[len(kept)-1].ID
	}
	return plan, nil
}

func findRewindTarget(events []Event, options RewindOptions) (int, error) {
	targetEventID := strings.TrimSpace(options.TargetEventID)
	if targetEventID == "" && options.TargetSequence <= 0 {
		return -1, fmt.Errorf("rewind target event id or sequence is required")
	}
	if targetEventID != "" && options.TargetSequence > 0 {
		for index, event := range events {
			if event.ID == targetEventID {
				if event.Sequence != options.TargetSequence {
					return -1, fmt.Errorf("conflicting rewind target selectors: event %s has sequence %d, not %d", targetEventID, event.Sequence, options.TargetSequence)
				}
				return index, nil
			}
		}
		return -1, fmt.Errorf("rewind target event %s was not found", targetEventID)
	}
	for index, event := range events {
		if targetEventID != "" && event.ID == targetEventID {
			return index, nil
		}
		if options.TargetSequence > 0 && event.Sequence == options.TargetSequence {
			return index, nil
		}
	}
	if targetEventID != "" {
		return -1, fmt.Errorf("rewind target event %s was not found", targetEventID)
	}
	return -1, fmt.Errorf("rewind target sequence %d was not found", options.TargetSequence)
}

func (store *Store) PlanCompaction(sessionID string, options CompactionOptions) (CompactionPlan, error) {
	if !ValidSessionID(sessionID) {
		return CompactionPlan{}, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	events, err := store.ReadEvents(sessionID)
	if err != nil {
		return CompactionPlan{}, err
	}
	preserveLast := options.PreserveLast
	if preserveLast <= 0 {
		preserveLast = defaultCompactionPreserveLast
	}
	maxPromptChars := options.MaxPromptChars
	if maxPromptChars <= 0 {
		maxPromptChars = defaultCompactionMaxPromptChars
	}
	split := len(events) - preserveLast
	if split < 0 {
		split = 0
	}
	compactable := events[:split]
	preserved := events[split:]
	prompt, truncated := buildCompactionPrompt(compactable, maxPromptChars)
	return CompactionPlan{
		SessionID:         sessionID,
		PreserveLast:      preserveLast,
		CompactableCount:  len(compactable),
		PreservedCount:    len(preserved),
		CompactableEvents: eventRefs(compactable),
		PreservedEvents:   eventRefs(preserved),
		SummaryPrompt:     prompt,
		PromptChars:       len(prompt),
		Truncated:         truncated,
	}, nil
}

func buildCompactionPrompt(events []Event, maxChars int) (string, bool) {
	if len(events) == 0 {
		return "No compactable Zero session events.", false
	}
	lines := []string{
		"Summarize these Zero session events for future context.",
		"Preserve user intent, tool outcomes, important files, blockers, and follow-up state.",
	}
	for _, event := range events {
		lines = append(lines, fmt.Sprintf("%d %s %s", event.Sequence, event.Type, shapedPayloadPreview(event)))
	}
	prompt := strings.Join(lines, "\n")
	if maxChars > 0 && len(prompt) > maxChars {
		if maxChars <= len("\n[truncated]") {
			return prompt[:maxChars], true
		}
		return prompt[:maxChars-len("\n[truncated]")] + "\n[truncated]", true
	}
	return prompt, false
}

func shapedPayloadPreview(event Event) string {
	switch event.Type {
	case EventPermission:
		return permissionPayloadPreview(event.Payload)
	case EventToolCall:
		return toolPayloadPreview(event.Payload, []string{"id", "name", "toolName"})
	case EventToolResult:
		return toolPayloadPreview(event.Payload, []string{"id", "name", "toolName", "status"})
	default:
		return payloadPreview(event.Payload)
	}
}

func permissionPayloadPreview(payload json.RawMessage) string {
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		return payloadPreview(payload)
	}
	shaped := map[string]any{}
	for _, key := range []string{"action", "name", "toolName", "permission", "permissionMode", "sideEffect", "grantMatched"} {
		if field, ok := value[key]; ok {
			shaped[key] = field
		}
	}
	if risk, ok := value["risk"].(map[string]any); ok {
		if level, ok := risk["level"]; ok {
			shaped["riskLevel"] = level
		}
	}
	return marshalPreview(shaped)
}

func toolPayloadPreview(payload json.RawMessage, allowedKeys []string) string {
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		return payloadPreview(payload)
	}
	shaped := map[string]any{}
	for _, key := range allowedKeys {
		if field, ok := value[key]; ok {
			shaped[key] = field
		}
	}
	if len(shaped) == 0 {
		shaped["payload"] = "redacted"
	}
	return marshalPreview(shaped)
}

func payloadPreview(payload json.RawMessage) string {
	if len(payload) == 0 {
		return "{}"
	}
	value := strings.Join(strings.Fields(string(payload)), " ")
	value = redaction.RedactString(value, redaction.Options{})
	if len(value) > 240 {
		return value[:240] + "..."
	}
	return value
}

func marshalPreview(value map[string]any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return payloadPreview(data)
}

func eventRefs(events []Event) []EventRef {
	refs := make([]EventRef, 0, len(events))
	for _, event := range events {
		refs = append(refs, EventRef{
			ID:        event.ID,
			Sequence:  event.Sequence,
			Type:      event.Type,
			CreatedAt: event.CreatedAt,
		})
	}
	return refs
}
