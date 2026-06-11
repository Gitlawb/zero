package sessions

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestStorePlansRewindBySequence(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: sequenceClock([]time.Time{
		time.Date(2026, 6, 6, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 6, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 6, 6, 10, 0, 2, 0, time.UTC),
		time.Date(2026, 6, 6, 10, 0, 3, 0, time.UTC),
		time.Date(2026, 6, 6, 10, 0, 4, 0, time.UTC),
	})})
	session, err := store.Create(CreateInput{SessionID: "rewind", Title: "Rewind"})
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"first", "second", "third", "fourth"} {
		if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": content}}); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := store.PlanRewind(session.SessionID, RewindOptions{TargetSequence: 2, KeepTarget: true})

	if err != nil {
		t.Fatal(err)
	}
	if plan.SessionID != session.SessionID || plan.TargetSequence != 2 || plan.TargetEventID != "rewind:2" {
		t.Fatalf("unexpected rewind target: %#v", plan)
	}
	if plan.KeptCount != 2 || plan.DroppedCount != 2 || plan.LastKeptEventID != "rewind:2" {
		t.Fatalf("unexpected rewind counts: %#v", plan)
	}
	if len(plan.KeptEvents) != 2 || len(plan.DroppedEvents) != 2 || plan.DroppedEvents[0].ID != "rewind:3" {
		t.Fatalf("unexpected rewind refs: %#v", plan)
	}
}

func TestStorePlansRewindByEventIDAndCanExcludeTarget(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "rewindid"})
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range []EventType{EventMessage, EventToolCall, EventToolResult} {
		if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: eventType, Payload: map[string]string{"type": string(eventType)}}); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := store.PlanRewind(session.SessionID, RewindOptions{TargetEventID: "rewindid:2", KeepTarget: false})

	if err != nil {
		t.Fatal(err)
	}
	if plan.KeptCount != 1 || plan.DroppedCount != 2 || plan.LastKeptEventID != "rewindid:1" {
		t.Fatalf("unexpected exclude-target rewind plan: %#v", plan)
	}
}

func TestStorePlanRewindRejectsMissingTargets(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "missingtarget"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": "one"}}); err != nil {
		t.Fatal(err)
	}

	_, err = store.PlanRewind(session.SessionID, RewindOptions{})

	if err == nil || !strings.Contains(err.Error(), "rewind target") {
		t.Fatalf("expected target error, got %v", err)
	}
}

func TestStorePlanRewindRejectsConflictingTargets(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "conflicttarget"})
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"one", "two"} {
		if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": content}}); err != nil {
			t.Fatal(err)
		}
	}

	_, err = store.PlanRewind(session.SessionID, RewindOptions{TargetEventID: "conflicttarget:1", TargetSequence: 2})

	if err == nil || !strings.Contains(err.Error(), "conflicting rewind target selectors") {
		t.Fatalf("expected conflicting target error, got %v", err)
	}
}

func TestStorePlansCompactionWindow(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "compact"})
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": content}}); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := store.PlanCompaction(session.SessionID, CompactionOptions{PreserveLast: 2, MaxPromptChars: 500})

	if err != nil {
		t.Fatal(err)
	}
	if plan.SessionID != session.SessionID || plan.CompactableCount != 3 || plan.PreservedCount != 2 {
		t.Fatalf("unexpected compaction counts: %#v", plan)
	}
	if len(plan.CompactableEvents) != 3 || len(plan.PreservedEvents) != 2 || plan.PreservedEvents[0].ID != "compact:4" {
		t.Fatalf("unexpected compaction refs: %#v", plan)
	}
	if !strings.Contains(plan.SummaryPrompt, "alpha") || strings.Contains(plan.SummaryPrompt, "epsilon") {
		t.Fatalf("summary prompt should include compactable events only, got %q", plan.SummaryPrompt)
	}
	if plan.Truncated {
		t.Fatalf("did not expect summary prompt truncation: %#v", plan)
	}
}

func TestStoreCompactionShapesSensitivePermissionEvents(t *testing.T) {
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz"
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "compactsafe"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: EventPermission, Payload: map[string]any{
		"action":         "allow",
		"name":           "write_file",
		"permission":     "prompt",
		"reason":         "contains " + secret,
		"grant":          map[string]string{"reason": secret},
		"permissionMode": "unsafe",
		"risk":           map[string]any{"level": "high", "details": secret},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": "preserved"}}); err != nil {
		t.Fatal(err)
	}

	plan, err := store.PlanCompaction(session.SessionID, CompactionOptions{PreserveLast: 1, MaxPromptChars: 500})

	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.SummaryPrompt, secret) || strings.Contains(plan.SummaryPrompt, "contains") {
		t.Fatalf("compaction prompt leaked sensitive permission payload: %q", plan.SummaryPrompt)
	}
	if !strings.Contains(plan.SummaryPrompt, "write_file") || !strings.Contains(plan.SummaryPrompt, "allow") || !strings.Contains(plan.SummaryPrompt, "high") {
		t.Fatalf("compaction prompt lost safe permission fields: %q", plan.SummaryPrompt)
	}
}

func TestStoreCompactionPromptCanBeTruncated(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "compactshort"})
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"abcdefghijklmnopqrstuvwxyz", "0123456789", "preserved"} {
		if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": content}}); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := store.PlanCompaction(session.SessionID, CompactionOptions{PreserveLast: 1, MaxPromptChars: 80})

	if err != nil {
		t.Fatal(err)
	}
	if !plan.Truncated || len(plan.SummaryPrompt) > 80 {
		t.Fatalf("expected truncated summary prompt within limit, got %#v", plan)
	}
}

func TestStoreRecordsCompactionEventPayload(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir(), Now: sequenceClock([]time.Time{
		time.Date(2026, 6, 11, 10, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 11, 10, 0, 1, 0, time.UTC),
		time.Date(2026, 6, 11, 10, 0, 2, 0, time.UTC),
		time.Date(2026, 6, 11, 10, 0, 3, 0, time.UTC),
		time.Date(2026, 6, 11, 10, 0, 4, 0, time.UTC),
		time.Date(2026, 6, 11, 10, 0, 5, 0, time.UTC),
	})})
	session, err := store.Create(CreateInput{SessionID: "compactrecord"})
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"raw-alpha-context", "raw-beta-context", "gamma", "delta"} {
		if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": content}}); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := store.PlanCompaction(session.SessionID, CompactionOptions{PreserveLast: 2, MaxPromptChars: 500})
	if err != nil {
		t.Fatal(err)
	}

	event, err := store.RecordCompaction(session.SessionID, RecordCompactionInput{
		Plan:    plan,
		Summary: "Alpha and beta have been summarized for replay.",
	})

	if err != nil {
		t.Fatal(err)
	}
	if event.Type != EventCompaction || event.ID != "compactrecord:5" {
		t.Fatalf("compaction event = %#v", event)
	}
	var payload CompactionPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("decode compaction payload: %v", err)
	}
	if payload.Summary != "Alpha and beta have been summarized for replay." {
		t.Fatalf("summary = %q", payload.Summary)
	}
	if payload.CompactedThroughEventID != "compactrecord:2" || payload.CompactedThroughSequence != 2 {
		t.Fatalf("compacted-through fields = %#v", payload)
	}
	if payload.PreserveLast != 2 || payload.CompactableCount != 2 || payload.PreservedCount != 2 {
		t.Fatalf("compaction counts = %#v", payload)
	}
	if payload.PromptChars != plan.PromptChars || payload.Truncated != plan.Truncated {
		t.Fatalf("compact-plan parity fields = %#v, plan = %#v", payload, plan)
	}
	if len(payload.CompactableEvents) != 2 || payload.CompactableEvents[0].ID != "compactrecord:1" || payload.CompactableEvents[1].ID != "compactrecord:2" {
		t.Fatalf("compactable refs = %#v", payload.CompactableEvents)
	}
	if len(payload.PreservedEvents) != 2 || payload.PreservedEvents[0].ID != "compactrecord:3" || payload.PreservedEvents[1].ID != "compactrecord:4" {
		t.Fatalf("preserved refs = %#v", payload.PreservedEvents)
	}
	loaded, err := store.Get(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.EventCount != 5 || loaded.LastEventType != EventCompaction {
		t.Fatalf("metadata after compaction = %#v", loaded)
	}
}

func TestStoreReadRehydratedEventsReplacesCompactedPrefixWithSummary(t *testing.T) {
	store := NewStore(StoreOptions{RootDir: t.TempDir()})
	session, err := store.Create(CreateInput{SessionID: "rehydrate"})
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"raw-alpha-context", "raw-beta-context", "gamma", "delta"} {
		if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": content}}); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := store.PlanCompaction(session.SessionID, CompactionOptions{PreserveLast: 2, MaxPromptChars: 500})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordCompaction(session.SessionID, RecordCompactionInput{
		Plan:    plan,
		Summary: "Early context summary.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(session.SessionID, AppendEventInput{Type: EventMessage, Payload: map[string]string{"content": "epsilon"}}); err != nil {
		t.Fatal(err)
	}

	events, err := store.ReadRehydratedEvents(session.SessionID)

	if err != nil {
		t.Fatal(err)
	}
	gotIDs := eventIDs(events)
	wantIDs := []string{"rehydrate:5", "rehydrate:3", "rehydrate:4", "rehydrate:6"}
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("rehydrated ids = %#v, want %#v", gotIDs, wantIDs)
	}
	prepared, err := PrepareExec(PrepareExecOptions{Store: store, Resume: session.SessionID})
	if err != nil {
		t.Fatalf("PrepareExec returned error: %v", err)
	}
	if got := eventIDs(prepared.ContextEvents); strings.Join(got, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("prepared context ids = %#v, want %#v", got, wantIDs)
	}
	prompt := FormatExecPrompt("continue", prepared)
	for _, want := range []string{"Early context summary.", "gamma", "delta", "epsilon"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("rehydrated prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, dropped := range []string{"raw-alpha-context", "raw-beta-context"} {
		if strings.Contains(prompt, dropped) {
			t.Fatalf("rehydrated prompt should not include compacted event %q:\n%s", dropped, prompt)
		}
	}
}

func eventIDs(events []Event) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return ids
}
