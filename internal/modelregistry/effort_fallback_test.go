package modelregistry

import "testing"

func TestReasoningEffortsFallbackForGPT5AndOSeries(t *testing.T) {
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		want []ReasoningEffort
	}{
		{"gpt-5", []ReasoningEffort{ReasoningEffortMinimal, ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}},
		{"gpt-5.5", []ReasoningEffort{ReasoningEffortMinimal, ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}},
		{"gpt-5.4-mini", []ReasoningEffort{ReasoningEffortMinimal, ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}},
		{"o3-mini", []ReasoningEffort{ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}},
		{"gpt-5.3-codex-spark", []ReasoningEffort{ReasoningEffortMinimal, ReasoningEffortLow, ReasoningEffortMedium, ReasoningEffortHigh}},
		{"gpt-4.1", nil}, // non-reasoning, registered: stays empty
		{"gpt-4o-mini", nil},
		{"ollama/llama3.1", nil},
	}
	for _, c := range cases {
		got := reg.ReasoningEfforts(c.name)
		if len(got) != len(c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: got %v, want %v", c.name, got, c.want)
			}
		}
	}
}
