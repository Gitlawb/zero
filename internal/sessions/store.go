package sessions

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	MetadataFile = "metadata.json"
	EventsFile   = "events.jsonl"
)

type EventType string

const (
	EventMessage     EventType = "message"
	EventToolCall    EventType = "tool_call"
	EventToolResult  EventType = "tool_result"
	EventUsage       EventType = "provider_usage"
	EventError       EventType = "error"
	EventSessionFork EventType = "session_fork"
)

type Metadata struct {
	SessionID          string    `json:"sessionId"`
	Title              string    `json:"title,omitempty"`
	Cwd                string    `json:"cwd,omitempty"`
	ModelID            string    `json:"modelId,omitempty"`
	Provider           string    `json:"provider,omitempty"`
	ParentSessionID    string    `json:"parentSessionId,omitempty"`
	ForkedFromEventID  string    `json:"forkedFromEventId,omitempty"`
	ForkedFromSequence int       `json:"forkedFromSequence,omitempty"`
	CreatedAt          string    `json:"createdAt"`
	UpdatedAt          string    `json:"updatedAt"`
	EventCount         int       `json:"eventCount"`
	LastEventType      EventType `json:"lastEventType,omitempty"`
}

type Event struct {
	ID        string    `json:"id"`
	SessionID string    `json:"sessionId"`
	Sequence  int       `json:"sequence"`
	Type      EventType `json:"type"`
	CreatedAt string    `json:"createdAt"`
	Payload   any       `json:"payload,omitempty"`
}

type DefaultRootOptions struct {
	Env map[string]string
}

type StoreOptions struct {
	RootDir string
	Now     func() time.Time
}

type Store struct {
	RootDir string
	now     func() time.Time
}

type CreateInput struct {
	SessionID          string
	Title              string
	Cwd                string
	ModelID            string
	Provider           string
	ParentSessionID    string
	ForkedFromEventID  string
	ForkedFromSequence int
}

type ForkInput struct {
	SessionID string
	Title     string
	Cwd       string
	ModelID   string
	Provider  string
}

type AppendEventInput struct {
	Type    EventType
	Payload any
}

func DefaultRoot(options DefaultRootOptions) (string, error) {
	dataHome := strings.TrimSpace(envValue(options.Env, "XDG_DATA_HOME"))
	if dataHome == "" {
		home := strings.TrimSpace(envValue(options.Env, "HOME"))
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return "", err
			}
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "zero", "sessions"), nil
}

func NewStore(options StoreOptions) *Store {
	root := options.RootDir
	if root == "" {
		if resolved, err := DefaultRoot(DefaultRootOptions{}); err == nil {
			root = resolved
		}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{RootDir: root, now: now}
}

func (store *Store) Create(input CreateInput) (Metadata, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		sessionID = createSessionID(store.now())
	}
	if !isValidSessionID(sessionID) {
		return Metadata{}, fmt.Errorf("Invalid Zero session id")
	}

	createdAt := store.timestamp()
	session := Metadata{
		SessionID:          sessionID,
		Title:              input.Title,
		Cwd:                input.Cwd,
		ModelID:            input.ModelID,
		Provider:           input.Provider,
		ParentSessionID:    input.ParentSessionID,
		ForkedFromEventID:  input.ForkedFromEventID,
		ForkedFromSequence: input.ForkedFromSequence,
		CreatedAt:          createdAt,
		UpdatedAt:          createdAt,
		EventCount:         0,
	}

	if err := os.MkdirAll(store.RootDir, 0o700); err != nil {
		return Metadata{}, err
	}
	if err := os.Mkdir(store.sessionPath(sessionID), 0o700); err != nil {
		if os.IsExist(err) {
			return Metadata{}, fmt.Errorf("Zero session already exists: %s", sessionID)
		}
		return Metadata{}, err
	}
	if err := store.writeMetadata(session); err != nil {
		return Metadata{}, err
	}
	file, err := os.OpenFile(store.eventsPath(sessionID), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return session, nil
		}
		return Metadata{}, err
	}
	if err := file.Close(); err != nil {
		return Metadata{}, err
	}
	return session, nil
}

func (store *Store) Get(sessionID string) (*Metadata, error) {
	if !isValidSessionID(sessionID) {
		return nil, fmt.Errorf("Invalid Zero session id")
	}
	session, err := store.readMetadata(sessionID)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (store *Store) List() ([]Metadata, error) {
	if err := os.MkdirAll(store.RootDir, 0o700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(store.RootDir)
	if err != nil {
		return nil, err
	}

	sessions := []Metadata{}
	for _, entry := range entries {
		if !entry.IsDir() || !isValidSessionID(entry.Name()) {
			continue
		}
		session, err := store.Get(entry.Name())
		if err != nil {
			return nil, err
		}
		if session != nil {
			sessions = append(sessions, *session)
		}
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].UpdatedAt == sessions[j].UpdatedAt {
			return sessions[i].SessionID < sessions[j].SessionID
		}
		return sessions[i].UpdatedAt > sessions[j].UpdatedAt
	})
	return sessions, nil
}

func (store *Store) Latest() (*Metadata, error) {
	sessions, err := store.List()
	if err != nil || len(sessions) == 0 {
		return nil, err
	}
	return &sessions[0], nil
}

func (store *Store) Fork(parentSessionID string, input ForkInput) (Metadata, error) {
	parent, err := store.Get(parentSessionID)
	if err != nil {
		return Metadata{}, err
	}
	if parent == nil {
		return Metadata{}, fmt.Errorf("Zero session not found: %s", parentSessionID)
	}

	parentEvents, err := store.ReadEvents(parentSessionID)
	if err != nil {
		return Metadata{}, err
	}
	lastEventID := ""
	lastSequence := 0
	if len(parentEvents) > 0 {
		last := parentEvents[len(parentEvents)-1]
		lastEventID = last.ID
		lastSequence = last.Sequence
	}

	title := input.Title
	if title == "" && parent.Title != "" {
		title = parent.Title + " (fork)"
	}
	fork, err := store.Create(CreateInput{
		SessionID:          input.SessionID,
		Title:              title,
		Cwd:                firstNonEmpty(input.Cwd, parent.Cwd),
		ModelID:            firstNonEmpty(input.ModelID, parent.ModelID),
		Provider:           firstNonEmpty(input.Provider, parent.Provider),
		ParentSessionID:    parentSessionID,
		ForkedFromEventID:  lastEventID,
		ForkedFromSequence: lastSequence,
	})
	if err != nil {
		return Metadata{}, err
	}

	for _, event := range parentEvents {
		if _, err := store.AppendEvent(fork.SessionID, AppendEventInput{Type: event.Type, Payload: cloneJSONValue(event.Payload)}); err != nil {
			return Metadata{}, err
		}
	}
	if _, err := store.AppendEvent(fork.SessionID, AppendEventInput{
		Type: EventSessionFork,
		Payload: map[string]any{
			"parentSessionId":    parentSessionID,
			"parentEventCount":   parent.EventCount,
			"copiedEventCount":   len(parentEvents),
			"forkedFromEventId":  lastEventID,
			"forkedFromSequence": lastSequence,
		},
	}); err != nil {
		return Metadata{}, err
	}

	updated, err := store.readMetadata(fork.SessionID)
	if err != nil {
		return Metadata{}, err
	}
	return updated, nil
}

func (store *Store) AppendEvent(sessionID string, input AppendEventInput) (Event, error) {
	if !isValidSessionID(sessionID) {
		return Event{}, fmt.Errorf("Invalid Zero session id")
	}
	session, err := store.readMetadata(sessionID)
	if err != nil {
		return Event{}, err
	}
	sequence := session.EventCount + 1
	createdAt := store.timestamp()
	event := Event{
		ID:        fmt.Sprintf("%s:%d", sessionID, sequence),
		SessionID: sessionID,
		Sequence:  sequence,
		Type:      input.Type,
		CreatedAt: createdAt,
		Payload:   input.Payload,
	}
	data, err := json.Marshal(event)
	if err != nil {
		return Event{}, err
	}
	file, err := os.OpenFile(store.eventsPath(sessionID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Event{}, err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return Event{}, err
	}
	if err := file.Close(); err != nil {
		return Event{}, err
	}

	session.UpdatedAt = createdAt
	session.EventCount = sequence
	session.LastEventType = input.Type
	if err := store.writeMetadata(session); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (store *Store) ReadEvents(sessionID string) ([]Event, error) {
	if !isValidSessionID(sessionID) {
		return nil, fmt.Errorf("Invalid Zero session id")
	}
	data, err := os.ReadFile(store.eventsPath(sessionID))
	if os.IsNotExist(err) {
		return []Event{}, nil
	}
	if err != nil {
		return nil, err
	}
	events := []Event{}
	for index, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var event Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("Invalid JSON in Zero session %s %s at line %d", sessionID, EventsFile, index+1)
		}
		events = append(events, event)
	}
	return events, nil
}

func (store *Store) timestamp() string {
	return store.now().UTC().Format(time.RFC3339)
}

func (store *Store) sessionPath(sessionID string) string {
	return filepath.Join(store.RootDir, sessionID)
}

func (store *Store) metadataPath(sessionID string) string {
	return filepath.Join(store.sessionPath(sessionID), MetadataFile)
}

func (store *Store) eventsPath(sessionID string) string {
	return filepath.Join(store.sessionPath(sessionID), EventsFile)
}

func (store *Store) readMetadata(sessionID string) (Metadata, error) {
	data, err := os.ReadFile(store.metadataPath(sessionID))
	if err != nil {
		return Metadata{}, err
	}
	var session Metadata
	if err := json.Unmarshal(data, &session); err != nil {
		return Metadata{}, err
	}
	return session, nil
}

func (store *Store) writeMetadata(session Metadata) error {
	if err := os.MkdirAll(store.sessionPath(session.SessionID), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tempPath := store.metadataPath(session.SessionID) + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tempPath, store.metadataPath(session.SessionID))
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func isValidSessionID(value string) bool {
	return sessionIDPattern.MatchString(value)
}

func createSessionID(now time.Time) string {
	random := make([]byte, 4)
	if _, err := rand.Read(random); err != nil {
		copy(random, []byte{byte(now.Nanosecond()), byte(now.Second()), byte(now.Minute()), byte(now.Hour())})
	}
	return fmt.Sprintf("zero_%s_%s", now.UTC().Format("20060102150405"), hex.EncodeToString(random))
}

func cloneJSONValue(value any) any {
	if value == nil {
		return nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var cloned any
	if err := json.Unmarshal(data, &cloned); err != nil {
		return value
	}
	return cloned
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
