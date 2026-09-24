package search

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/redaction"
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

// EVERY TEXT FIELD, NOT A HAND-KEPT SUBSET.
//
// `zero search --json` prints Hit.Session straight to stdout, so a field
// redactMetadata forgets is a field that reaches the terminal with whatever was
// in it. The list in redactMetadata is written out by hand; this walks the
// struct instead, so a string field added to sessions.Metadata (or to the Goal
// hanging off it) fails here on the day it lands rather than the day someone
// notices it in output. It named twenty uncovered fields when it was written,
// including the review text the user types and the model's own draft reasoning.
func TestRedactMetadataCoversEveryTextField(t *testing.T) {
	const secret = "sk-ant-api03-aaaaaaaaaaaaaaaaaaaaaaaa0123456789ABCD"

	var session sessions.Metadata
	fillMetadataStrings(reflect.ValueOf(&session).Elem(), secret)
	session.Goal = &sessions.Goal{}
	fillMetadataStrings(reflect.ValueOf(session.Goal).Elem(), secret)

	// The premise: the secret is one RedactString already recognizes, so a field
	// that still carries it was not passed through the redactor at all.
	if redaction.RedactString(secret, redaction.Options{}) == secret {
		t.Fatalf("SETUP INVALID: %q is not a shape RedactString redacts, so every leg below passes vacuously", secret)
	}

	var leaked []string
	var checked int
	walkMetadataStrings(reflect.ValueOf(redactMetadata(session, redaction.Options{})), "", func(name, value string) {
		checked++
		if strings.Contains(value, secret) {
			leaked = append(leaked, name)
		}
	})
	if checked == 0 {
		t.Fatal("SETUP INVALID: the walk visited no fields, so it proves nothing")
	}
	if len(leaked) > 0 {
		t.Errorf("redactMetadata left the secret in %d of %d text fields: %s",
			len(leaked), checked, strings.Join(leaked, ", "))
	}
}

// Metadata is passed by value, but Goal is a pointer inside it. Redacting
// through that pointer would edit the session the caller still holds, so a
// search would quietly rewrite the live record it was asked to report on.
func TestRedactMetadataDoesNotRewriteTheCallersGoal(t *testing.T) {
	const secret = "sk-ant-api03-aaaaaaaaaaaaaaaaaaaaaaaa0123456789ABCD"
	goal := &sessions.Goal{Objective: "ship " + secret, StatusReason: "blocked on " + secret}
	session := sessions.Metadata{SessionID: "s", Goal: goal}

	redacted := redactMetadata(session, redaction.Options{})

	if goal.Objective != "ship "+secret || goal.StatusReason != "blocked on "+secret {
		t.Errorf("redactMetadata rewrote the caller's goal: %+v", *goal)
	}
	if redacted.Goal == goal {
		t.Error("redacted metadata still points at the caller's goal")
	}
	if strings.Contains(redacted.Goal.Objective, secret) || strings.Contains(redacted.Goal.StatusReason, secret) {
		t.Errorf("goal text was not redacted: %+v", *redacted.Goal)
	}
}

// fillMetadataStrings sets every string-kinded field of one struct value.
func fillMetadataStrings(value reflect.Value, text string) {
	for index := 0; index < value.NumField(); index++ {
		field := value.Field(index)
		if field.Kind() == reflect.String && field.CanSet() {
			field.SetString(text)
		}
	}
}

// walkMetadataStrings visits every string-kinded field, descending into nested
// structs and non-nil pointers so the Goal is not skipped.
func walkMetadataStrings(value reflect.Value, prefix string, visit func(name, value string)) {
	switch value.Kind() {
	case reflect.Pointer:
		if !value.IsNil() {
			walkMetadataStrings(value.Elem(), prefix, visit)
		}
	case reflect.Struct:
		for index := 0; index < value.NumField(); index++ {
			name := value.Type().Field(index).Name
			if prefix != "" {
				name = prefix + "." + name
			}
			field := value.Field(index)
			switch field.Kind() {
			case reflect.String:
				visit(name, field.String())
			case reflect.Struct, reflect.Pointer:
				walkMetadataStrings(field, name, visit)
			}
		}
	}
}
