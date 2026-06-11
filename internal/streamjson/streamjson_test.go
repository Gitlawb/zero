package streamjson

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRedactStringDoesNotOverMatchProse(t *testing.T) {
	// Ordinary prose must survive: the old unanchored patterns redacted "sk-"
	// inside "task-list" and the word after a bare "bearer".
	for _, clean := range []string{
		"updated the task-list and the task-runner",
		"the bearer of bad news arrived",
		"set the bearer token in the header",
	} {
		if got := redactString(clean); got != clean {
			t.Fatalf("redactString(%q) = %q, want unchanged", clean, got)
		}
	}

	// Real secrets must still be redacted.
	for _, secret := range []string{
		"sk-proj-abcdefghijklmnopqrstuvwxyz0123456789",
		"Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9abcdef",
	} {
		if got := redactString(secret); strings.Contains(got, "abcdef") && !strings.Contains(got, "[REDACTED]") {
			t.Fatalf("redactString(%q) = %q, expected redaction", secret, got)
		}
		if !strings.Contains(redactString(secret), "[REDACTED]") {
			t.Fatalf("redactString(%q) did not redact", secret)
		}
	}
}

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

func TestResolveImagesDecodesNormalizesAndCaps(t *testing.T) {
	// happy path: two images across two events, jpg media type normalized to image/jpeg
	rawPNG := []byte("\x89PNG fake bytes")
	rawJPG := []byte("\xff\xd8\xff fake jpeg")
	events := []InputEvent{
		{SchemaVersion: 1, Type: InputMessage, Role: "user", Content: "a", Images: []InputImage{
			{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(rawPNG)},
		}},
		{SchemaVersion: 1, Type: InputMessage, Role: "user", Content: "b", Images: []InputImage{
			{MediaType: "jpg", Data: base64.StdEncoding.EncodeToString(rawJPG)},
		}},
	}
	images, err := ResolveImages(events)
	if err != nil {
		t.Fatalf("ResolveImages returned error: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(images))
	}
	if images[0].MediaType != "image/png" || string(images[0].Data) != string(rawPNG) {
		t.Fatalf("png image not resolved: %+v", images[0])
	}
	if images[1].MediaType != "image/jpeg" || string(images[1].Data) != string(rawJPG) {
		t.Fatalf("jpg image not normalized/decoded: %+v", images[1])
	}

	// no images -> nil, no error
	none, err := ResolveImages([]InputEvent{{SchemaVersion: 1, Type: InputPrompt, Content: "x"}})
	if err != nil {
		t.Fatalf("ResolveImages with no images errored: %v", err)
	}
	if none != nil {
		t.Fatalf("expected nil for no images, got %+v", none)
	}
}

func TestResolveImagesRejectsBadInputs(t *testing.T) {
	// invalid base64
	badB64 := []InputEvent{{SchemaVersion: 1, Type: InputMessage, Role: "user", Content: "a",
		Images: []InputImage{{MediaType: "image/png", Data: "not base64!!"}}}}
	if _, err := ResolveImages(badB64); err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("expected base64 decode error, got %v", err)
	}

	// unsupported media type
	badType := []InputEvent{{SchemaVersion: 1, Type: InputMessage, Role: "user", Content: "a",
		Images: []InputImage{{MediaType: "image/svg+xml", Data: base64.StdEncoding.EncodeToString([]byte("x"))}}}}
	if _, err := ResolveImages(badType); err == nil || !strings.Contains(err.Error(), "unsupported image media type") {
		t.Fatalf("expected unsupported media type error, got %v", err)
	}

	// oversized image (> 10 MiB decoded)
	oversize := make([]byte, (10<<20)+1)
	bigEvent := []InputEvent{{SchemaVersion: 1, Type: InputMessage, Role: "user", Content: "a",
		Images: []InputImage{{MediaType: "image/png", Data: base64.StdEncoding.EncodeToString(oversize)}}}}
	if _, err := ResolveImages(bigEvent); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversize rejection, got %v", err)
	}
}

// TestResolveImagesRejectsOversizedEncodedLengthBeforeDecode asserts that an
// over-encoded-length payload is rejected from its ENCODED length BEFORE the
// expensive base64 decode runs — so a huge blob never allocates a decode buffer
// just to be capped after the fact. The Data here is NOT valid base64 ('@' is
// outside the alphabet); reaching DecodeString would surface a "base64" error,
// so an "exceeds" error proves the size gate fired first, before any decode.
func TestResolveImagesRejectsOversizedEncodedLengthBeforeDecode(t *testing.T) {
	// Encoded length whose DecodedLen exceeds the cap, made of non-base64 bytes
	// so a decode attempt would fail loudly rather than silently allocate.
	encodedLen := base64.StdEncoding.EncodedLen(maxStreamImageBytes) + 8
	huge := strings.Repeat("@", encodedLen)
	event := []InputEvent{{SchemaVersion: 1, Type: InputMessage, Role: "user", Content: "a",
		Images: []InputImage{{MediaType: "image/png", Data: huge}}}}

	_, err := ResolveImages(event)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected pre-decode oversize rejection, got %v", err)
	}
	if strings.Contains(err.Error(), "base64") {
		t.Fatalf("decode ran before the size gate: %v", err)
	}
}

func TestValidateInputEventAllowsImageOnlyMessage(t *testing.T) {
	// image-only message (empty content) is valid
	imageOnly := `{"schemaVersion":1,"type":"message","role":"user","content":"","images":[{"mediaType":"image/png","data":"aGVsbG8="}]}`
	events, err := ParseInput(imageOnly)
	if err != nil {
		t.Fatalf("image-only message should be valid, got %v", err)
	}
	if len(events) != 1 || len(events[0].Images) != 1 {
		t.Fatalf("image-only event not parsed: %+v", events)
	}

	// empty content AND no images is still rejected
	empty := `{"schemaVersion":1,"type":"message","role":"user","content":""}`
	if _, err := ParseInput(empty); err == nil || !strings.Contains(err.Error(), "content is required") {
		t.Fatalf("expected empty-content rejection, got %v", err)
	}

	// prompt event with empty content is still rejected (no images allowed there)
	emptyPrompt := `{"schemaVersion":1,"type":"prompt","content":""}`
	if _, err := ParseInput(emptyPrompt); err == nil || !strings.Contains(err.Error(), "content is required") {
		t.Fatalf("expected empty prompt rejection, got %v", err)
	}
}

func TestParseInputImagesAcceptedOnlyOnMessageEvents(t *testing.T) {
	// images allowed on a message event
	msg := `{"schemaVersion":1,"type":"message","role":"user","content":"look","images":[{"mediaType":"image/png","data":"aGVsbG8="}]}`
	events, err := ParseInput(msg)
	if err != nil {
		t.Fatalf("message+images should parse, got %v", err)
	}
	if len(events) != 1 || len(events[0].Images) != 1 || events[0].Images[0].Data != "aGVsbG8=" {
		t.Fatalf("images not parsed: %+v", events)
	}

	// images rejected on a prompt event (not whitelisted there)
	prompt := `{"schemaVersion":1,"type":"prompt","content":"look","images":[{"mediaType":"image/png","data":"aGVsbG8="}]}`
	if _, err := ParseInput(prompt); err == nil || !strings.Contains(err.Error(), "unknown field images") {
		t.Fatalf("expected images rejected on prompt event, got %v", err)
	}

	// truly unknown field still rejected on a message event
	bad := `{"schemaVersion":1,"type":"message","role":"user","content":"look","extra":true}`
	if _, err := ParseInput(bad); err == nil || !strings.Contains(err.Error(), "unknown field extra") {
		t.Fatalf("expected unknown field still rejected, got %v", err)
	}
}

func TestInputEventImagesRoundTripAndOmitempty(t *testing.T) {
	ev := InputEvent{
		SchemaVersion: 1,
		Type:          InputMessage,
		Role:          "user",
		Content:       "look at this",
		Images: []InputImage{
			{MediaType: "image/png", Data: "aGVsbG8="},
		},
	}
	data, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(data), `"mediaType":"image/png"`) {
		t.Fatalf("expected mediaType key, got %s", data)
	}
	if !strings.Contains(string(data), `"data":"aGVsbG8="`) {
		t.Fatalf("expected data key, got %s", data)
	}

	var back InputEvent
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back.Images) != 1 || back.Images[0].MediaType != "image/png" || back.Images[0].Data != "aGVsbG8=" {
		t.Fatalf("images lost in round-trip: %+v", back.Images)
	}

	// omitempty: a text-only event must not emit the new key.
	bare, _ := json.Marshal(InputEvent{SchemaVersion: 1, Type: InputPrompt, Content: "hi"})
	if strings.Contains(string(bare), "images") {
		t.Fatalf("expected images omitted on text-only event, got %s", bare)
	}
}

func TestParseInputThenResolveImagesRoundTrip(t *testing.T) {
	raw := []byte("\x89PNG round trip bytes")
	line := `{"schemaVersion":1,"type":"message","role":"user","content":"describe","images":[{"mediaType":"png","data":"` +
		base64.StdEncoding.EncodeToString(raw) + `"}]}`

	events, err := ParseInput(line)
	if err != nil {
		t.Fatalf("ParseInput: %v", err)
	}

	// prompt resolution is unaffected by images
	prompt, err := ResolvePrompt(events)
	if err != nil {
		t.Fatalf("ResolvePrompt: %v", err)
	}
	if prompt != "describe" {
		t.Fatalf("prompt = %q", prompt)
	}

	images, err := ResolveImages(events)
	if err != nil {
		t.Fatalf("ResolveImages: %v", err)
	}
	if len(images) != 1 || images[0].MediaType != "image/png" || string(images[0].Data) != string(raw) {
		t.Fatalf("round-trip image mismatch: %+v", images)
	}

	// text-only input still resolves to nil images, non-empty prompt
	textOnly, err := ParseInput(`{"schemaVersion":1,"type":"prompt","content":"hello"}`)
	if err != nil {
		t.Fatalf("ParseInput text-only: %v", err)
	}
	imgs, err := ResolveImages(textOnly)
	if err != nil {
		t.Fatalf("ResolveImages text-only: %v", err)
	}
	if imgs != nil {
		t.Fatalf("expected nil images for text-only input, got %+v", imgs)
	}
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
