package streamjson

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFormatEventRedactsSecretsAndSerializesOneLine(t *testing.T) {
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"

	line, err := FormatEvent(Event{
		SchemaVersion: SchemaVersion,
		Type:          EventError,
		RunID:         "run_test",
		Code:          "provider_error",
		Message:       "provider leaked " + secret,
		Recoverable:   boolPtr(false),
	})

	if err != nil {
		t.Fatalf("FormatEvent returned error: %v", err)
	}
	if strings.Contains(line, "\n") {
		t.Fatalf("expected one JSON line, got %q", line)
	}
	if strings.Contains(line, secret) {
		t.Fatalf("expected secret to be redacted, got %q", line)
	}
	if !strings.Contains(line, "[REDACTED]") {
		t.Fatalf("expected redaction marker, got %q", line)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("expected valid JSON, got %q: %v", line, err)
	}
	if decoded["schemaVersion"] != float64(SchemaVersion) || decoded["type"] != string(EventError) {
		t.Fatalf("unexpected event payload: %#v", decoded)
	}
}

func TestParseInputPromptCombinesPromptAndUserMessages(t *testing.T) {
	input := strings.Join([]string{
		`{"schemaVersion":1,"type":"message","role":"user","content":"Inspect this repo."}`,
		`{"schemaVersion":1,"type":"prompt","content":"Focus on failing tests."}`,
		"",
	}, "\n")

	prompt, err := ParsePrompt(input)

	if err != nil {
		t.Fatalf("ParsePrompt returned error: %v", err)
	}
	if prompt != "Inspect this repo.\n\nFocus on failing tests." {
		t.Fatalf("prompt = %q", prompt)
	}
}

func TestParseInputRejectsMalformedLinesWithLineNumbers(t *testing.T) {
	_, err := ParseInput(`{"type":"prompt"`)

	if err == nil || !strings.Contains(err.Error(), "Invalid stream-json input at line 1") {
		t.Fatalf("expected line-numbered parse error, got %v", err)
	}
}

func TestParseInputRejectsUnknownFields(t *testing.T) {
	_, err := ParseInput(`{"schemaVersion":1,"type":"prompt","content":"hello","extra":true}`)

	if err == nil || !strings.Contains(err.Error(), "Invalid stream-json input at line 1") {
		t.Fatalf("expected strict input error, got %v", err)
	}
}

func TestCreateRunIDUsesStablePrefix(t *testing.T) {
	runID := CreateRunID(time.Date(2026, 6, 4, 12, 34, 56, 0, time.UTC))

	if !strings.HasPrefix(runID, "run_20260604123456_") {
		t.Fatalf("run id = %q", runID)
	}
}

func boolPtr(value bool) *bool {
	return &value
}
