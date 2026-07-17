package trace

import (
	"bytes"
	"strings"
	"testing"
)

func TestOutputBudgetTraceRoundTripContainsNoOutput(t *testing.T) {
	recorder := NewRecorder("session", "run", "")
	recorder.Start()
	recorder.EmitOutputBudget(OutputBudgetEvent{
		Tool:                    "grep",
		Category:                "search",
		OriginalBytes:           10000,
		RetainedBytes:           1000,
		EstimatedOriginalTokens: 2500,
		EstimatedRetainedTokens: 250,
		Truncated:               true,
		Reason:                  "semantic_search_budget",
		SpillCreated:            true,
	})

	var encoded bytes.Buffer
	if err := WriteNDJSON(&encoded, recorder.Finish()); err != nil {
		t.Fatalf("WriteNDJSON: %v", err)
	}
	if strings.Contains(encoded.String(), "secret output body") {
		t.Fatal("trace unexpectedly contains output text")
	}
	parsed, err := ReadNDJSON(strings.NewReader(encoded.String()))
	if err != nil {
		t.Fatalf("ReadNDJSON: %v", err)
	}
	if len(parsed.OutputBudgets) != 1 || parsed.OutputBudgets[0].Tool != "grep" || !parsed.OutputBudgets[0].Truncated {
		t.Fatalf("unexpected round trip: %#v", parsed.OutputBudgets)
	}
}
