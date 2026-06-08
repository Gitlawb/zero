package sessions

import (
	"encoding/json"
	"fmt"
	"strings"
)

type EnsureSpecImplementationInput struct {
	Title               string
	Cwd                 string
	ModelID             string
	Provider            string
	SpecID              string
	SpecFilePath        string
	SpecDraftModelID    string
	SpecDraftReasoning  string
	SpecUserComment     string
	SpecSourceSessionID string
	RootSessionID       string
	Prompt              string
}

func (store *Store) FindSpecImplementation(specID string, sourceSessionID string) (*Metadata, error) {
	specID = strings.TrimSpace(specID)
	sourceSessionID = strings.TrimSpace(sourceSessionID)
	if specID == "" || sourceSessionID == "" {
		return nil, nil
	}
	if !ValidSessionID(sourceSessionID) {
		return nil, fmt.Errorf("invalid zero session id %q", sourceSessionID)
	}
	items, err := store.List()
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.SessionKind == SessionKindSpecImpl && item.SpecID == specID && item.SpecSourceSessionID == sourceSessionID {
			matched := item
			return &matched, nil
		}
	}
	return nil, nil
}

func (store *Store) EnsureSpecImplementation(input EnsureSpecImplementationInput) (Metadata, []Event, error) {
	specID := strings.TrimSpace(input.SpecID)
	sourceSessionID := strings.TrimSpace(input.SpecSourceSessionID)
	prompt := input.Prompt
	if specID == "" {
		return Metadata{}, nil, fmt.Errorf("zero spec id is required")
	}
	if sourceSessionID == "" {
		return Metadata{}, nil, fmt.Errorf("zero spec source session id is required")
	}
	if !ValidSessionID(sourceSessionID) {
		return Metadata{}, nil, fmt.Errorf("invalid zero session id %q", sourceSessionID)
	}
	if strings.TrimSpace(prompt) == "" {
		return Metadata{}, nil, fmt.Errorf("zero spec implementation prompt is required")
	}
	source, err := store.Get(sourceSessionID)
	if err != nil {
		return Metadata{}, nil, err
	}
	if source == nil {
		return Metadata{}, nil, fmt.Errorf("zero session not found: %s", sourceSessionID)
	}

	unlock, err := store.lockSession(sourceSessionID)
	if err != nil {
		return Metadata{}, nil, err
	}
	defer unlock()

	impl, err := store.FindSpecImplementation(specID, sourceSessionID)
	if err != nil {
		return Metadata{}, nil, err
	}
	if impl == nil {
		created, err := store.Create(CreateInput{
			SessionKind:         SessionKindSpecImpl,
			Title:               input.Title,
			Cwd:                 input.Cwd,
			ModelID:             input.ModelID,
			Provider:            input.Provider,
			ParentSessionID:     sourceSessionID,
			RootSessionID:       firstNonEmpty(input.RootSessionID, sourceSessionID),
			SpecID:              specID,
			SpecFilePath:        input.SpecFilePath,
			SpecStatus:          SpecStatusApproved,
			SpecDraftModelID:    input.SpecDraftModelID,
			SpecDraftReasoning:  input.SpecDraftReasoning,
			SpecUserComment:     input.SpecUserComment,
			SpecSourceSessionID: sourceSessionID,
		})
		if err != nil {
			return Metadata{}, nil, err
		}
		impl = &created
	}

	events, err := store.ReadEvents(impl.SessionID)
	if err != nil {
		return Metadata{}, nil, err
	}
	if !eventsContainUserPrompt(events, prompt) {
		if _, err := store.AppendEvent(impl.SessionID, AppendEventInput{
			Type: EventMessage,
			Payload: map[string]any{
				"role":    "user",
				"content": prompt,
			},
		}); err != nil {
			return Metadata{}, nil, err
		}
		events, err = store.ReadEvents(impl.SessionID)
		if err != nil {
			return Metadata{}, nil, err
		}
	}

	loaded, err := store.Get(impl.SessionID)
	if err != nil {
		return Metadata{}, nil, err
	}
	if loaded == nil {
		return Metadata{}, nil, fmt.Errorf("zero session not found: %s", impl.SessionID)
	}
	return *loaded, events, nil
}

func eventsContainUserPrompt(events []Event, prompt string) bool {
	for _, event := range events {
		if event.Type != EventMessage {
			continue
		}
		var payload struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		if payload.Role == "user" && payload.Content == prompt {
			return true
		}
	}
	return false
}
