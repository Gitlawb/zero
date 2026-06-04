package search

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/sessions"
)

const (
	IndexFileName  = "search-index.json"
	indexSchema    = 1
	defaultLimit   = 20
	defaultContext = 80
)

type Options struct {
	Store        *sessions.Store
	RootDir      string
	Limit        int
	ContextChars int
	SessionID    string
	Type         sessions.EventType
	Reindex      bool
	Now          func() time.Time
}

type EventSummary struct {
	ID        string             `json:"id"`
	Sequence  int                `json:"sequence"`
	Type      sessions.EventType `json:"type"`
	CreatedAt string             `json:"createdAt"`
}

type Hit struct {
	Session sessions.Metadata `json:"session"`
	Event   EventSummary      `json:"event"`
	Context string            `json:"context"`
	Match   Match             `json:"match"`
}

type Match struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type Result struct {
	Query            string `json:"query"`
	NormalizedQuery  string `json:"normalizedQuery"`
	RootDir          string `json:"rootDir"`
	SearchedSessions int    `json:"searchedSessions"`
	TotalHits        int    `json:"totalHits"`
	Hits             []Hit  `json:"hits"`
}

type Index struct {
	SchemaVersion     int          `json:"schemaVersion"`
	SessionID         string       `json:"sessionId"`
	SessionUpdatedAt  string       `json:"sessionUpdatedAt"`
	SessionEventCount int          `json:"sessionEventCount"`
	GeneratedAt       string       `json:"generatedAt"`
	Entries           []IndexEntry `json:"entries"`
}

type IndexEntry struct {
	SessionID string             `json:"sessionId"`
	EventID   string             `json:"eventId"`
	Sequence  int                `json:"sequence"`
	Type      sessions.EventType `json:"type"`
	CreatedAt string             `json:"createdAt"`
	Text      string             `json:"text"`
}

func Sessions(query string, options Options) (Result, error) {
	normalized := NormalizeQuery(query)
	store := options.Store
	if store == nil {
		store = sessions.NewStore(sessions.StoreOptions{RootDir: options.RootDir})
	}
	limit := normalizeLimit(options.Limit)
	contextChars := normalizeContext(options.ContextChars)
	if normalized == "" || limit == 0 {
		return Result{Query: query, NormalizedQuery: normalized, RootDir: store.RootDir, Hits: []Hit{}}, nil
	}
	sessionList, err := resolveSessions(store, strings.TrimSpace(options.SessionID))
	if err != nil {
		return Result{}, err
	}
	terms := splitTerms(normalized)
	now := options.Now
	if now == nil {
		now = time.Now
	}
	result := Result{
		Query:            query,
		NormalizedQuery:  normalized,
		RootDir:          store.RootDir,
		SearchedSessions: len(sessionList),
		Hits:             []Hit{},
	}
	for _, session := range sessionList {
		index, err := LoadIndex(store, session, LoadOptions{Reindex: options.Reindex, Now: now})
		if err != nil {
			return Result{}, err
		}
		for _, entry := range index.Entries {
			if options.Type != "" && entry.Type != options.Type {
				continue
			}
			match, ok := findMatch(entry.Text, normalized, terms)
			if !ok {
				continue
			}
			result.Hits = append(result.Hits, Hit{
				Session: session,
				Event: EventSummary{
					ID:        entry.EventID,
					Sequence:  entry.Sequence,
					Type:      entry.Type,
					CreatedAt: entry.CreatedAt,
				},
				Context: buildContext(entry.Text, match.Start, match.End, contextChars),
				Match:   match,
			})
			if len(result.Hits) >= limit {
				result.TotalHits = len(result.Hits)
				return result, nil
			}
		}
	}
	result.TotalHits = len(result.Hits)
	return result, nil
}

type LoadOptions struct {
	Reindex bool
	Now     func() time.Time
}

func LoadIndex(store *sessions.Store, session sessions.Metadata, options LoadOptions) (Index, error) {
	if !options.Reindex {
		index, ok := readIndex(store, session)
		if ok && current(index, session) {
			return index, nil
		}
	}
	return RebuildIndex(store, session, options.Now)
}

func RebuildIndex(store *sessions.Store, session sessions.Metadata, now func() time.Time) (Index, error) {
	if now == nil {
		now = time.Now
	}
	events, err := store.ReadEvents(session.SessionID)
	if err != nil {
		return Index{}, err
	}
	index := Index{
		SchemaVersion:     indexSchema,
		SessionID:         session.SessionID,
		SessionUpdatedAt:  session.UpdatedAt,
		SessionEventCount: session.EventCount,
		GeneratedAt:       now().UTC().Format(time.RFC3339),
		Entries:           make([]IndexEntry, 0, len(events)),
	}
	for _, event := range events {
		index.Entries = append(index.Entries, IndexEntry{
			SessionID: session.SessionID,
			EventID:   event.ID,
			Sequence:  event.Sequence,
			Type:      event.Type,
			CreatedAt: event.CreatedAt,
			Text:      ExtractText(redactedPayload(event.Payload)),
		})
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return Index{}, fmt.Errorf("encode Zero search index: %w", err)
	}
	if err := os.WriteFile(indexPath(store, session.SessionID), append(data, '\n'), 0o600); err != nil {
		return Index{}, fmt.Errorf("write Zero search index: %w", err)
	}
	return index, nil
}

func FormatResult(result Result) string {
	query := redaction.RedactString(strings.TrimSpace(result.Query), redaction.Options{})
	if len(result.Hits) == 0 {
		return fmt.Sprintf("No local session events matched %q. Searched %s.", query, count(result.SearchedSessions, "session"))
	}
	lines := []string{fmt.Sprintf("Found %s for %q:", count(result.TotalHits, "local session event"), query)}
	for index, hit := range result.Hits {
		title := ""
		if hit.Session.Title != "" {
			title = " - " + redaction.RedactString(hit.Session.Title, redaction.Options{})
		}
		lines = append(lines, fmt.Sprintf("%d. %s #%d %s%s", index+1, redaction.RedactString(hit.Session.SessionID, redaction.Options{}), hit.Event.Sequence, redaction.RedactString(string(hit.Event.Type), redaction.Options{}), title))
		context := strings.Join(strings.Fields(redaction.RedactString(hit.Context, redaction.Options{})), " ")
		if context != "" {
			lines = append(lines, "   "+context)
		}
		details := []string{}
		if hit.Session.Cwd != "" {
			details = append(details, "cwd: "+redaction.RedactString(hit.Session.Cwd, redaction.Options{}))
		}
		if hit.Session.ModelID != "" {
			details = append(details, "model: "+redaction.RedactString(hit.Session.ModelID, redaction.Options{}))
		}
		if hit.Session.Provider != "" {
			details = append(details, "provider: "+redaction.RedactString(hit.Session.Provider, redaction.Options{}))
		}
		details = append(details, "updated: "+redaction.RedactString(hit.Session.UpdatedAt, redaction.Options{}))
		lines = append(lines, "   "+strings.Join(details, " | "))
	}
	return strings.Join(lines, "\n")
}

func RedactResult(result Result) Result {
	options := redaction.Options{}
	redacted := result
	redacted.Query = redaction.RedactString(redacted.Query, options)
	redacted.NormalizedQuery = redaction.RedactString(redacted.NormalizedQuery, options)
	redacted.RootDir = redaction.RedactString(redacted.RootDir, options)
	redacted.Hits = make([]Hit, len(result.Hits))
	for index, hit := range result.Hits {
		redacted.Hits[index] = Hit{
			Session: redactMetadata(hit.Session, options),
			Event:   redactEventSummary(hit.Event, options),
			Context: redaction.RedactString(hit.Context, options),
			Match:   hit.Match,
		}
	}
	return redacted
}

func redactMetadata(session sessions.Metadata, options redaction.Options) sessions.Metadata {
	session.SessionID = redaction.RedactString(session.SessionID, options)
	session.Title = redaction.RedactString(session.Title, options)
	session.Cwd = redaction.RedactString(session.Cwd, options)
	session.ModelID = redaction.RedactString(session.ModelID, options)
	session.Provider = redaction.RedactString(session.Provider, options)
	session.ParentSessionID = redaction.RedactString(session.ParentSessionID, options)
	session.ForkedFromEventID = redaction.RedactString(session.ForkedFromEventID, options)
	session.CreatedAt = redaction.RedactString(session.CreatedAt, options)
	session.UpdatedAt = redaction.RedactString(session.UpdatedAt, options)
	session.LastEventType = sessions.EventType(redaction.RedactString(string(session.LastEventType), options))
	return session
}

func redactEventSummary(event EventSummary, options redaction.Options) EventSummary {
	event.ID = redaction.RedactString(event.ID, options)
	event.Type = sessions.EventType(redaction.RedactString(string(event.Type), options))
	event.CreatedAt = redaction.RedactString(event.CreatedAt, options)
	return event
}

func NormalizeQuery(query string) string {
	return strings.ToLower(strings.Join(strings.Fields(query), " "))
}

func ExtractText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64, float32, int, int64, bool:
		return fmt.Sprint(typed)
	case []any:
		parts := []string{}
		for _, item := range typed {
			if text := ExtractText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		parts := []string{}
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, key)
			item := typed[key]
			if text := ExtractText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, " ")
	default:
		return fmt.Sprint(typed)
	}
}

func resolveSessions(store *sessions.Store, sessionID string) ([]sessions.Metadata, error) {
	if sessionID == "" {
		return store.List()
	}
	session, err := store.Get(sessionID)
	if err != nil || session == nil {
		return []sessions.Metadata{}, err
	}
	return []sessions.Metadata{*session}, nil
}

func readIndex(store *sessions.Store, session sessions.Metadata) (Index, bool) {
	data, err := os.ReadFile(indexPath(store, session.SessionID))
	if err != nil {
		return Index{}, false
	}
	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return Index{}, false
	}
	return index, true
}

func current(index Index, session sessions.Metadata) bool {
	return index.SchemaVersion == indexSchema &&
		index.SessionID == session.SessionID &&
		index.SessionUpdatedAt == session.UpdatedAt &&
		index.SessionEventCount == session.EventCount &&
		len(index.Entries) == session.EventCount
}

func redactedPayload(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return redaction.RedactString(string(raw), redaction.Options{})
	}
	return redaction.RedactValue(payload, redaction.Options{})
}

func findMatch(text string, query string, terms []string) (Match, bool) {
	normalizedText := strings.ToLower(text)
	if index := strings.Index(normalizedText, query); index >= 0 {
		return Match{Start: index, End: index + len(query)}, true
	}
	first := -1
	last := -1
	for _, term := range terms {
		index := strings.Index(normalizedText, term)
		if index < 0 {
			return Match{}, false
		}
		if first < 0 || index < first {
			first = index
		}
		if end := index + len(term); end > last {
			last = end
		}
	}
	return Match{Start: first, End: last}, first >= 0
}

func buildContext(text string, start int, end int, contextChars int) string {
	left := start - contextChars
	if left < 0 {
		left = 0
	}
	right := end + contextChars
	if right > len(text) {
		right = len(text)
	}
	return strings.TrimSpace(text[left:right])
}

func splitTerms(query string) []string {
	if query == "" {
		return nil
	}
	return strings.Fields(query)
}

func normalizeLimit(limit int) int {
	if limit == 0 {
		return defaultLimit
	}
	if limit < 0 {
		return 0
	}
	return limit
}

func normalizeContext(contextChars int) int {
	if contextChars == 0 {
		return defaultContext
	}
	if contextChars < 0 {
		return 0
	}
	return contextChars
}

func indexPath(store *sessions.Store, sessionID string) string {
	return filepath.Join(store.RootDir, sessionID, IndexFileName)
}

func count(value int, label string) string {
	suffix := "s"
	if value == 1 {
		suffix = ""
	}
	return fmt.Sprintf("%d %s%s", value, label, suffix)
}
