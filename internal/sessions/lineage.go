package sessions

import (
	"fmt"
	"sort"
	"strings"
)

func (store *Store) CreateChild(parentSessionID string, input ChildInput) (Metadata, error) {
	if !ValidSessionID(parentSessionID) {
		return Metadata{}, fmt.Errorf("invalid zero session id %q", parentSessionID)
	}
	parent, err := store.Get(parentSessionID)
	if err != nil {
		return Metadata{}, err
	}
	if parent == nil {
		return Metadata{}, fmt.Errorf("zero session not found: %s", parentSessionID)
	}
	parentEvents, err := store.ReadEvents(parent.SessionID)
	if err != nil {
		return Metadata{}, err
	}
	var lastParentEvent Event
	if len(parentEvents) > 0 {
		lastParentEvent = parentEvents[len(parentEvents)-1]
	}

	child, err := store.Create(CreateInput{
		SessionID:           input.SessionID,
		SessionKind:         SessionKindChild,
		Title:               childTitle(input.Title, input.AgentName, parent.Title),
		Cwd:                 firstNonEmpty(input.Cwd, parent.Cwd),
		ModelID:             firstNonEmpty(input.ModelID, parent.ModelID),
		Provider:            firstNonEmpty(input.Provider, parent.Provider),
		Tag:                 input.Tag,
		Depth:               input.Depth,
		ParentSessionID:     parent.SessionID,
		RootSessionID:       firstNonEmpty(parent.RootSessionID, parent.SessionID),
		AgentName:           strings.TrimSpace(input.AgentName),
		TaskID:              strings.TrimSpace(input.TaskID),
		SpawnedFromEventID:  lastParentEvent.ID,
		SpawnedFromSequence: lastParentEvent.Sequence,
	})
	if err != nil {
		return Metadata{}, err
	}

	payload := childLinkPayload(parent, child, input, lastParentEvent)
	if _, err := store.AppendEvent(parent.SessionID, AppendEventInput{Type: EventSessionChild, Payload: payload}); err != nil {
		return Metadata{}, err
	}
	if _, err := store.AppendEvent(child.SessionID, AppendEventInput{Type: EventSessionChild, Payload: payload}); err != nil {
		return Metadata{}, err
	}
	loaded, err := store.readMetadata(child.SessionID)
	if err != nil {
		return Metadata{}, err
	}
	return loaded, nil
}

func (store *Store) ListChildren(parentSessionID string) ([]Metadata, error) {
	if !ValidSessionID(parentSessionID) {
		return nil, fmt.Errorf("invalid zero session id %q", parentSessionID)
	}
	parent, err := store.Get(parentSessionID)
	if err != nil {
		return nil, err
	}
	if parent == nil {
		return nil, fmt.Errorf("zero session not found: %s", parentSessionID)
	}
	all, err := store.List()
	if err != nil {
		return nil, err
	}
	children := []Metadata{}
	for _, session := range all {
		if session.ParentSessionID == parentSessionID && session.SessionKind == SessionKindChild {
			children = append(children, session)
		}
	}
	sort.SliceStable(children, func(left int, right int) bool {
		if children[left].SpawnedFromSequence == children[right].SpawnedFromSequence {
			return children[left].UpdatedAt > children[right].UpdatedAt
		}
		return children[left].SpawnedFromSequence > children[right].SpawnedFromSequence
	})
	return children, nil
}

func (store *Store) Lineage(sessionID string) ([]Metadata, error) {
	if !ValidSessionID(sessionID) {
		return nil, fmt.Errorf("invalid zero session id %q", sessionID)
	}
	lineage := []Metadata{}
	seen := map[string]bool{}
	currentID := sessionID
	for currentID != "" {
		if seen[currentID] {
			return nil, fmt.Errorf("cycle in zero session lineage at %s", currentID)
		}
		seen[currentID] = true
		session, err := store.Get(currentID)
		if err != nil {
			return nil, err
		}
		if session == nil {
			return nil, fmt.Errorf("zero session not found: %s", currentID)
		}
		lineage = append(lineage, *session)
		currentID = session.ParentSessionID
	}
	for left, right := 0, len(lineage)-1; left < right; left, right = left+1, right-1 {
		lineage[left], lineage[right] = lineage[right], lineage[left]
	}
	return lineage, nil
}

func (store *Store) Tree(rootSessionID string) (TreeNode, error) {
	if !ValidSessionID(rootSessionID) {
		return TreeNode{}, fmt.Errorf("invalid zero session id %q", rootSessionID)
	}
	return store.tree(rootSessionID, map[string]bool{})
}

func (store *Store) tree(sessionID string, seen map[string]bool) (TreeNode, error) {
	if seen[sessionID] {
		return TreeNode{}, fmt.Errorf("cycle in zero session tree at %s", sessionID)
	}
	seen[sessionID] = true
	session, err := store.Get(sessionID)
	if err != nil {
		return TreeNode{}, err
	}
	if session == nil {
		return TreeNode{}, fmt.Errorf("zero session not found: %s", sessionID)
	}
	children, err := store.ListChildren(sessionID)
	if err != nil {
		return TreeNode{}, err
	}
	node := TreeNode{Session: *session, Children: make([]TreeNode, 0, len(children))}
	for _, child := range children {
		childNode, err := store.tree(child.SessionID, seen)
		if err != nil {
			return TreeNode{}, err
		}
		node.Children = append(node.Children, childNode)
	}
	delete(seen, sessionID)
	return node, nil
}

func childTitle(title string, agentName string, parentTitle string) string {
	if trimmed := strings.TrimSpace(title); trimmed != "" {
		return trimmed
	}
	if trimmed := strings.TrimSpace(agentName); trimmed != "" {
		return trimmed + " child session"
	}
	if trimmed := strings.TrimSpace(parentTitle); trimmed != "" {
		return trimmed + " (child)"
	}
	return "Zero child session"
}

func childLinkPayload(parent *Metadata, child Metadata, input ChildInput, lastParentEvent Event) map[string]any {
	return map[string]any{
		"parentSessionId":     parent.SessionID,
		"childSessionId":      child.SessionID,
		"rootSessionId":       child.RootSessionID,
		"agentName":           strings.TrimSpace(input.AgentName),
		"taskId":              strings.TrimSpace(input.TaskID),
		"tag":                 strings.TrimSpace(input.Tag),
		"depth":               input.Depth,
		"prompt":              strings.TrimSpace(input.Prompt),
		"spawnedFromEventId":  lastParentEvent.ID,
		"spawnedFromSequence": lastParentEvent.Sequence,
	}
}
