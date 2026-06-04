package search

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/sessions"
)

func TestSearchSessionsFindsRedactedEventContextAndCachesIndex(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir(), Now: fixedSearchClock("2026-06-04T14:00:00Z")})
	session, err := store.Create(sessions.CreateInput{SessionID: "searchable", Title: "Search", Cwd: "/repo", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]any{"content": "please rotate apiKey=sk-secret1234567890 before deploy"}}); err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}
	if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{Type: sessions.EventToolResult, Payload: map[string]any{"output": "deployment finished"}}); err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}

	result, err := Sessions("rotate deploy", Options{Store: store, Limit: 5, ContextChars: 120})
	if err != nil {
		t.Fatalf("Sessions returned error: %v", err)
	}
	if result.TotalHits != 1 || result.SearchedSessions != 1 {
		t.Fatalf("unexpected result counts: %#v", result)
	}
	if hit := result.Hits[0]; hit.Session.SessionID != "searchable" || hit.Event.Sequence != 1 {
		t.Fatalf("unexpected hit: %#v", hit)
	}
	if strings.Contains(result.Hits[0].Context, "sk-secret") {
		t.Fatalf("search context leaked secret: %#v", result.Hits[0])
	}

	indexPath := filepath.Join(store.RootDir, "searchable", IndexFileName)
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("expected search index to be written: %v", err)
	}
	formatted := FormatResult(result)
	if !strings.Contains(formatted, "Found 1 local session event") || strings.Contains(formatted, "sk-secret") {
		t.Fatalf("unexpected formatted result: %q", formatted)
	}
}

func TestSearchSessionsSupportsFiltersAndEmptyQuery(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir(), Now: fixedSearchClock("2026-06-04T14:30:00Z")})
	one, err := store.Create(sessions.CreateInput{SessionID: "one"})
	if err != nil {
		t.Fatalf("Create one returned error: %v", err)
	}
	two, err := store.Create(sessions.CreateInput{SessionID: "two"})
	if err != nil {
		t.Fatalf("Create two returned error: %v", err)
	}
	if _, err := store.AppendEvent(one.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: "needle in message"}); err != nil {
		t.Fatalf("AppendEvent one returned error: %v", err)
	}
	if _, err := store.AppendEvent(two.SessionID, sessions.AppendEventInput{Type: sessions.EventToolResult, Payload: "needle in tool result"}); err != nil {
		t.Fatalf("AppendEvent two returned error: %v", err)
	}

	result, err := Sessions("needle", Options{Store: store, SessionID: "two", Type: sessions.EventToolResult})
	if err != nil {
		t.Fatalf("Sessions returned error: %v", err)
	}
	if result.TotalHits != 1 || result.Hits[0].Session.SessionID != "two" {
		t.Fatalf("unexpected filtered result: %#v", result)
	}

	empty, err := Sessions("   ", Options{Store: store})
	if err != nil {
		t.Fatalf("empty Sessions returned error: %v", err)
	}
	if empty.TotalHits != 0 || empty.SearchedSessions != 0 {
		t.Fatalf("empty query should not search sessions: %#v", empty)
	}
}

func TestSearchSessionsMatchesMapKeys(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir(), Now: fixedSearchClock("2026-06-04T14:45:00Z")})
	session, err := store.Create(sessions.CreateInput{SessionID: "map_keys"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{Type: sessions.EventError, Payload: map[string]any{"error": "", "status": "success"}}); err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}

	result, err := Sessions("error", Options{Store: store})
	if err != nil {
		t.Fatalf("Sessions returned error: %v", err)
	}
	if result.TotalHits != 1 || result.Hits[0].Event.Type != sessions.EventError {
		t.Fatalf("expected map key search hit, got %#v", result)
	}
}

func fixedSearchClock(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
}
