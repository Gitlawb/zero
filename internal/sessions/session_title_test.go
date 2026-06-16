package sessions

import (
	"testing"
	"time"
)

func newTitleTestStore(t *testing.T) *Store {
	t.Helper()
	now := time.Date(2026, 6, 16, 9, 0, 0, 0, time.UTC)
	return NewStore(StoreOptions{
		RootDir: t.TempDir(),
		Now: func() time.Time {
			now = now.Add(time.Second)
			return now
		},
	})
}

func TestUpdateTitle(t *testing.T) {
	store := newTitleTestStore(t)
	session, err := store.Create(CreateInput{Title: "first message title"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// An append bumps UpdatedAt and EventCount; UpdateTitle must preserve both.
	if _, err := store.AppendEvent(session.SessionID, AppendEventInput{
		Type:    EventMessage,
		Payload: map[string]any{"role": "user", "content": "hi"},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	before, err := store.Get(session.SessionID)
	if err != nil || before == nil {
		t.Fatalf("get before: %v", err)
	}

	updated, err := store.UpdateTitle(session.SessionID, "  Clean Generated Title  ")
	if err != nil {
		t.Fatalf("update title: %v", err)
	}
	if updated.Title != "Clean Generated Title" {
		t.Fatalf("title not trimmed/stored: %q", updated.Title)
	}
	if updated.UpdatedAt != before.UpdatedAt {
		t.Fatalf("UpdatedAt must not change on retitle: before=%q after=%q", before.UpdatedAt, updated.UpdatedAt)
	}
	if updated.EventCount != before.EventCount {
		t.Fatalf("EventCount changed: before=%d after=%d", before.EventCount, updated.EventCount)
	}

	persisted, err := store.Get(session.SessionID)
	if err != nil || persisted == nil {
		t.Fatalf("get after: %v", err)
	}
	if persisted.Title != "Clean Generated Title" {
		t.Fatalf("persisted title = %q, want %q", persisted.Title, "Clean Generated Title")
	}

	// A blank title is rejected and must not erase the existing one.
	if _, err := store.UpdateTitle(session.SessionID, "   "); err == nil {
		t.Fatal("expected a blank title to be rejected")
	}
	if got, _ := store.Get(session.SessionID); got == nil || got.Title != "Clean Generated Title" {
		t.Fatalf("title must survive a rejected blank update, got %#v", got)
	}

	// An unchanged title is a no-op (still succeeds).
	if _, err := store.UpdateTitle(session.SessionID, "Clean Generated Title"); err != nil {
		t.Fatalf("no-op retitle should succeed: %v", err)
	}

	// An invalid session id is rejected.
	if _, err := store.UpdateTitle("../escape", "whatever"); err == nil {
		t.Fatal("expected an invalid session id to be rejected")
	}
}
