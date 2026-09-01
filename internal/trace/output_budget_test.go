package trace

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestOutputBudgetTraceRoundTripContainsNoOutput(t *testing.T) {
	recorder := NewRecorder("session", "run", "")
	recorder.Start()
	event := OutputBudgetEvent{
		Tool:                    "grep",
		Category:                "search",
		OriginalBytes:           10000,
		RetainedBytes:           1000,
		EstimatedOriginalTokens: 2500,
		EstimatedRetainedTokens: 250,
		Truncated:               true,
		Reason:                  "semantic_search_budget",
		SpillCreated:            true,
	}
	recorder.EmitOutputBudget(event)

	var encoded bytes.Buffer
	if err := WriteNDJSON(&encoded, recorder.Finish()); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	const secretOutput = "secret output body"
	if strings.Contains(encoded.String(), secretOutput) {
		t.Fatal("output_budget trace contains raw secret output")
	}
	allowedKeys := map[string]bool{
		"type": true, "tool": true, "category": true, "original_bytes": true, "retained_bytes": true,
		"estimated_original_tokens": true, "estimated_retained_tokens": true, "truncated": true,
		"reason": true, "spill_created": true,
	}
	findUndocumentedKey := func(record map[string]any) string {
		for key := range record {
			if !allowedKeys[key] {
				return key
			}
		}
		return ""
	}
	decoder := json.NewDecoder(strings.NewReader(encoded.String()))
	var outputBudgetRecord map[string]any
	for {
		var record map[string]any
		if err := decoder.Decode(&record); err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("decode trace record: %v", err)
		}
		if record["type"] == "output_budget" {
			outputBudgetRecord = record
			break
		}
	}
	if outputBudgetRecord == nil {
		t.Fatal("missing output_budget trace record")
	}
	if key := findUndocumentedKey(outputBudgetRecord); key != "" {
		t.Fatalf("output_budget trace contains undocumented key %q", key)
	}
	for key := range allowedKeys {
		if _, exists := outputBudgetRecord[key]; !exists {
			t.Fatalf("output_budget trace missing documented key %q", key)
		}
	}
	recordJSON, err := json.Marshal(outputBudgetRecord)
	if err != nil {
		t.Fatalf("encode output_budget trace record: %v", err)
	}
	var serializedEvent OutputBudgetEvent
	if err := json.Unmarshal(recordJSON, &serializedEvent); err != nil {
		t.Fatalf("decode output_budget trace event: %v", err)
	}
	if serializedEvent != event {
		t.Fatalf("serialized output_budget event = %#v, want %#v", serializedEvent, event)
	}
	contaminated := make(map[string]any, len(outputBudgetRecord)+1)
	for key, value := range outputBudgetRecord {
		contaminated[key] = value
	}
	contaminated["output"] = secretOutput
	if key := findUndocumentedKey(contaminated); key == "" {
		t.Fatal("trace key validation would allow raw secret output")
	}
	parsed, err := ReadNDJSON(strings.NewReader(encoded.String()))
	if err != nil {
		t.Fatalf("ReadNDJSON: %v", err)
	}
	if len(parsed.OutputBudgets) != 1 || parsed.OutputBudgets[0] != event {
		t.Fatalf("unexpected round trip: %#v", parsed.OutputBudgets)
	}
}
