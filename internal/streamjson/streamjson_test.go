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

func TestFormatEventRedactsSensitiveObjectKeys(t *testing.T) {
	apiKey := "plain-api-key-value"
	accessToken := "plain-access-token-value"

	line, err := FormatEvent(Event{
		SchemaVersion: SchemaVersion,
		Type:          EventToolCall,
		RunID:         "run_test",
		ID:            "call_1",
		Name:          "bash",
		Args: map[string]any{
			"api_key": apiKey,
			"nested": map[string]any{
				"accessToken":  accessToken,
				"promptTokens": 12,
			},
		},
	})

	if err != nil {
		t.Fatalf("FormatEvent returned error: %v", err)
	}
	if strings.Contains(line, apiKey) || strings.Contains(line, accessToken) {
		t.Fatalf("expected sensitive object values to be redacted, got %q", line)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("expected valid JSON, got %q: %v", line, err)
	}
	args := decoded["args"].(map[string]any)
	if args["api_key"] != "[REDACTED]" {
		t.Fatalf("expected api_key to be redacted, got %#v", args["api_key"])
	}
	nested := args["nested"].(map[string]any)
	if nested["accessToken"] != "[REDACTED]" {
		t.Fatalf("expected accessToken to be redacted, got %#v", nested["accessToken"])
	}
	if nested["promptTokens"] != float64(12) {
		t.Fatalf("expected non-sensitive token counter to remain numeric, got %#v", nested["promptTokens"])
	}
}

func TestFormatEventIncludesPermissionDecisionReason(t *testing.T) {
	line, err := FormatEvent(Event{
		SchemaVersion:  SchemaVersion,
		Type:           EventPermissionDecision,
		RunID:          "run_test",
		ID:             "call_1",
		Name:           "write_file",
		Action:         "allow",
		DecisionReason: "approved by operator",
	})

	if err != nil {
		t.Fatalf("FormatEvent returned error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("expected valid JSON, got %q: %v", line, err)
	}
	if decoded["type"] != "permission_decision" || decoded["decisionReason"] != "approved by operator" {
		t.Fatalf("expected permission decision reason to be serialized, got %#v", decoded)
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
	runID, err := CreateRunID(time.Date(2026, 6, 4, 12, 34, 56, 0, time.UTC))

	if err != nil {
		t.Fatalf("CreateRunID returned error: %v", err)
	}
	if !strings.HasPrefix(runID, "run_20260604123456_") {
		t.Fatalf("run id = %q", runID)
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func TestEventRoundTripsStructuredToolResultFields(t *testing.T) {
	redacted := true
	truncated := false
	ev := Event{
		SchemaVersion: 1,
		Type:          EventToolResult,
		Output:        "Edited f.go",
		Truncated:     &truncated,
		Redacted:      &redacted,
		ChangedFiles:  []string{"f.go"},
		Display:       &Display{Summary: "Edited f.go", Kind: "diff"},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var back Event
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Redacted == nil || !*back.Redacted {
		t.Error("redacted lost in round-trip")
	}
	if len(back.ChangedFiles) != 1 || back.ChangedFiles[0] != "f.go" {
		t.Errorf("changedFiles lost: %v", back.ChangedFiles)
	}
	if back.Display == nil || back.Display.Kind != "diff" {
		t.Errorf("display lost: %+v", back.Display)
	}
	// omitempty: a bare event must not emit the new keys
	bare, _ := json.Marshal(Event{SchemaVersion: 1, Type: EventText})
	for _, k := range []string{"redacted", "changedFiles", "display"} {
		if strings.Contains(string(bare), k) {
			t.Errorf("expected %q omitted on bare event, got %s", k, bare)
		}
	}
}

func TestEventRoundTripsCheckpointInfo(t *testing.T) {
	ev := Event{
		SchemaVersion: 1,
		Type:          EventCheckpoint,
		Checkpoint:    &CheckpointInfo{Sequence: 5, Tool: "edit_file", Files: []string{"a.go"}},
	}
	data, _ := json.Marshal(ev)
	var back Event
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Checkpoint == nil || back.Checkpoint.Tool != "edit_file" || back.Checkpoint.Sequence != 5 {
		t.Errorf("checkpoint lost: %+v", back.Checkpoint)
	}
	bare, _ := json.Marshal(Event{SchemaVersion: 1, Type: EventText})
	if strings.Contains(string(bare), "checkpoint") {
		t.Errorf("checkpoint should be omitted on bare event: %s", bare)
	}
}
