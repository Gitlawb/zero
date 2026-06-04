package sessions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	MetadataFile = "metadata.json"
	EventsFile   = "events.jsonl"
)

type EventType string

const (
	EventMessage       EventType = "message"
	EventToolCall      EventType = "tool_call"
	EventToolResult    EventType = "tool_result"
	EventProviderUsage EventType = "provider_usage"
	EventError         EventType = "error"
	EventSessionFork   EventType = "session_fork"
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

type Event struct {
	ID        string          `json:"id"`
	SessionID string          `json:"sessionId"`
	Sequence  int             `json:"sequence"`
	Type      EventType       `json:"type"`
	CreatedAt string          `json:"createdAt"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type StoreOptions struct {
	RootDir string
	Now     func() time.Time
	Env     map[string]string
}

type Store struct {
	RootDir      string
	now          func() time.Time
	locksMu      sync.Mutex
	sessionLocks map[string]*sync.Mutex
	idCounter    atomic.Uint64
}

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func NewStore(options StoreOptions) *Store {
	rootDir := strings.TrimSpace(options.RootDir)
	if rootDir == "" {
		rootDir = DefaultRoot(options.Env)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Store{RootDir: rootDir, now: now, sessionLocks: map[string]*sync.Mutex{}}
}

func DefaultRoot(env map[string]string) string {
	dataHome := strings.TrimSpace(envValue(env, "XDG_DATA_HOME"))
	home := strings.TrimSpace(envValue(env, "HOME"))
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = userHome
		}
	}
	base := dataHome
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "zero", "sessions")
}

func ValidSessionID(sessionID string) bool {
	return sessionIDPattern.MatchString(strings.TrimSpace(sessionID))
}

func (store *Store) Create(input CreateInput) (Metadata, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		sessionID = store.createID()
	}
	if !ValidSessionID(sessionID) {
		return Metadata{}, fmt.Errorf("invalid Zero session id %q", input.SessionID)
	}

	timestamp := store.timestamp()
	session := Metadata{
		SessionID:          sessionID,
		Title:              strings.TrimSpace(input.Title),
		Cwd:                strings.TrimSpace(input.Cwd),
		ModelID:            strings.TrimSpace(input.ModelID),
		Provider:           strings.TrimSpace(input.Provider),
		ParentSessionID:    strings.TrimSpace(input.ParentSessionID),
		ForkedFromEventID:  strings.TrimSpace(input.ForkedFromEventID),
		ForkedFromSequence: input.ForkedFromSequence,
		CreatedAt:          timestamp,
		UpdatedAt:          timestamp,
		EventCount:         0,
	}

	if err := os.MkdirAll(store.RootDir, 0o700); err != nil {
		return Metadata{}, fmt.Errorf("create Zero session root: %w", err)
	}
	if err := os.Mkdir(store.sessionPath(sessionID), 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return Metadata{}, fmt.Errorf("zero session already exists: %s", sessionID)
		}
		return Metadata{}, fmt.Errorf("create Zero session directory: %w", err)
	}
	if err := store.writeMetadata(session); err != nil {
		return Metadata{}, err
	}
	file, err := os.OpenFile(store.eventsPath(sessionID), os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Metadata{}, fmt.Errorf("create Zero session events file: %w", err)
	}
	if err := file.Close(); err != nil {
		return Metadata{}, fmt.Errorf("close Zero session events file: %w", err)
	}
	return session, nil
}

func (store *Store) Get(sessionID string) (*Metadata, error) {
	if !ValidSessionID(sessionID) {
		return nil, fmt.Errorf("invalid Zero session id %q", sessionID)
	}
	session, err := store.readMetadata(sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	return &session, nil
}

func (store *Store) List() ([]Metadata, error) {
	if err := os.MkdirAll(store.RootDir, 0o700); err != nil {
		return nil, fmt.Errorf("create Zero session root: %w", err)
	}
	entries, err := os.ReadDir(store.RootDir)
	if err != nil {
		return nil, fmt.Errorf("read Zero session root: %w", err)
	}
	sessions := []Metadata{}
	for _, entry := range entries {
		if !entry.IsDir() || !ValidSessionID(entry.Name()) {
			continue
		}
		session, err := store.Get(entry.Name())
		if err != nil || session == nil {
			continue
		}
		sessions = append(sessions, *session)
	}
	sort.SliceStable(sessions, func(left int, right int) bool {
		if sessions[left].UpdatedAt == sessions[right].UpdatedAt {
			return sessions[left].SessionID < sessions[right].SessionID
		}
		return sessions[left].UpdatedAt > sessions[right].UpdatedAt
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
	if !ValidSessionID(parentSessionID) {
		return Metadata{}, fmt.Errorf("invalid Zero session id %q", parentSessionID)
	}
	parent, err := store.Get(parentSessionID)
	if err != nil {
		return Metadata{}, err
	}
	if parent == nil {
		return Metadata{}, fmt.Errorf("zero session not found: %s", parentSessionID)
	}
	events, err := store.ReadEvents(parentSessionID)
	if err != nil {
		return Metadata{}, err
	}
	var last Event
	if len(events) > 0 {
		last = events[len(events)-1]
	}

	title := strings.TrimSpace(input.Title)
	if title == "" && parent.Title != "" {
		title = parent.Title + " (fork)"
	}
	fork, err := store.Create(CreateInput{
		SessionID:          input.SessionID,
		Title:              title,
		Cwd:                firstNonEmpty(input.Cwd, parent.Cwd),
		ModelID:            firstNonEmpty(input.ModelID, parent.ModelID),
		Provider:           firstNonEmpty(input.Provider, parent.Provider),
		ParentSessionID:    parent.SessionID,
		ForkedFromEventID:  last.ID,
		ForkedFromSequence: last.Sequence,
	})
	if err != nil {
		return Metadata{}, err
	}
	for _, event := range events {
		if _, err := store.AppendEvent(fork.SessionID, AppendEventInput{Type: event.Type, Payload: event.Payload}); err != nil {
			return Metadata{}, err
		}
	}
	if _, err := store.AppendEvent(fork.SessionID, AppendEventInput{
		Type: EventSessionFork,
		Payload: map[string]any{
			"parentSessionId":    parent.SessionID,
			"parentEventCount":   parent.EventCount,
			"copiedEventCount":   len(events),
			"forkedFromEventId":  last.ID,
			"forkedFromSequence": last.Sequence,
		},
	}); err != nil {
		return Metadata{}, err
	}
	loaded, err := store.readMetadata(fork.SessionID)
	if err != nil {
		return Metadata{}, err
	}
	return loaded, nil
}

func (store *Store) AppendEvent(sessionID string, input AppendEventInput) (Event, error) {
	if !ValidSessionID(sessionID) {
		return Event{}, fmt.Errorf("invalid Zero session id %q", sessionID)
	}
	if strings.TrimSpace(string(input.Type)) == "" {
		return Event{}, fmt.Errorf("zero session event type is required")
	}
	lock := store.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	session, err := store.readMetadata(sessionID)
	if err != nil {
		return Event{}, err
	}
	payload, err := rawPayload(input.Payload)
	if err != nil {
		return Event{}, err
	}
	sequence := session.EventCount + 1
	timestamp := store.timestamp()
	event := Event{
		ID:        fmt.Sprintf("%s:%d", sessionID, sequence),
		SessionID: sessionID,
		Sequence:  sequence,
		Type:      input.Type,
		CreatedAt: timestamp,
		Payload:   payload,
	}
	data, err := json.Marshal(event)
	if err != nil {
		return Event{}, fmt.Errorf("encode Zero session event: %w", err)
	}
	file, err := os.OpenFile(store.eventsPath(sessionID), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return Event{}, fmt.Errorf("append Zero session event: %w", err)
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return Event{}, fmt.Errorf("append Zero session event: %w", err)
	}
	if err := file.Close(); err != nil {
		return Event{}, fmt.Errorf("close Zero session event file: %w", err)
	}
	session.UpdatedAt = timestamp
	session.EventCount = sequence
	session.LastEventType = input.Type
	if err := store.writeMetadata(session); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (store *Store) ReadEvents(sessionID string) ([]Event, error) {
	if !ValidSessionID(sessionID) {
		return nil, fmt.Errorf("invalid Zero session id %q", sessionID)
	}
	data, err := os.ReadFile(store.eventsPath(sessionID))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Event{}, nil
		}
		return nil, fmt.Errorf("read Zero session events: %w", err)
	}
	events := []Event{}
	for index, line := range bytes.Split(data, []byte{'\n'}) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return nil, fmt.Errorf("invalid JSON in Zero session %s %s at line %d: %w", sessionID, EventsFile, index+1, err)
		}
		events = append(events, event)
	}
	return events, nil
}

func (store *Store) timestamp() string {
	return store.now().UTC().Format(time.RFC3339)
}

func (store *Store) createID() string {
	timestamp := store.now().UTC()
	return fmt.Sprintf("zero_%s_%d_%d", timestamp.Format("20060102150405"), timestamp.UnixNano(), store.idCounter.Add(1))
}

func (store *Store) sessionLock(sessionID string) *sync.Mutex {
	store.locksMu.Lock()
	defer store.locksMu.Unlock()
	if store.sessionLocks == nil {
		store.sessionLocks = map[string]*sync.Mutex{}
	}
	lock := store.sessionLocks[sessionID]
	if lock == nil {
		lock = &sync.Mutex{}
		store.sessionLocks[sessionID] = lock
	}
	return lock
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
		return Metadata{}, fmt.Errorf("invalid Zero session metadata %s: %w", sessionID, err)
	}
	return session, nil
}

func (store *Store) writeMetadata(session Metadata) error {
	data, err := json.MarshalIndent(session, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Zero session metadata: %w", err)
	}
	path := store.metadataPath(session.SessionID)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write Zero session metadata: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace Zero session metadata: %w", err)
	}
	return nil
}

func rawPayload(payload any) (json.RawMessage, error) {
	if payload == nil {
		return nil, nil
	}
	if raw, ok := payload.(json.RawMessage); ok {
		copied := append(json.RawMessage{}, bytes.TrimSpace(raw)...)
		if len(copied) == 0 {
			return nil, nil
		}
		if !json.Valid(copied) {
			return nil, fmt.Errorf("invalid raw JSON payload")
		}
		return copied, nil
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode Zero session payload: %w", err)
	}
	return data, nil
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
