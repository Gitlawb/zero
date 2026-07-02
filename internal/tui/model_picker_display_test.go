package tui

import "testing"

// The model picker must title each row with the model NAME, not the models.dev
// marketing description. Regression: Ollama Cloud models resolved full-sentence
// descriptions from the remote catalog, so rows showed sentences and several
// models sharing one description looked like exact duplicates (issue: /model
// list rendered descriptions as titles).
func TestModelPickerDisplayNamePrefersModelName(t *testing.T) {
	sentence := "DeepSeek chat model for instruction following, coding, and analysis"
	cases := []struct {
		id          string
		description string
		want        string
	}{
		{"deepseek-v3.2", sentence, "Deepseek V3.2"},
		{"deepseek-v3.2-thinking", sentence, "Deepseek V3.2 Thinking"},
		{"glm-5.2", "GLM flagship model", "GLM 5.2"},
		{"qwen3-coder:480b", "Qwen coding agent model for repository tasks", "Qwen3 Coder 480b"},
		{"anthropic/claude-sonnet-4.6", "some blurb", "Claude Sonnet 4.6"},
	}
	for _, tc := range cases {
		if got := modelPickerDisplayName(tc.id, tc.description); got != tc.want {
			t.Errorf("modelPickerDisplayName(%q, %q) = %q, want %q", tc.id, tc.description, got, tc.want)
		}
	}
}

// Two distinct model ids that share the same catalog description must render as
// distinct rows (the "duplicate-looking rows" symptom), never as the shared
// sentence.
func TestModelPickerDisplayNameDistinctForSharedDescription(t *testing.T) {
	shared := "Mistral coding agent model for repository tasks and software engineering"
	a := modelPickerDisplayName("codestral-2", shared)
	b := modelPickerDisplayName("devstral-2", shared)
	if a == b {
		t.Fatalf("shared description collapsed rows to the same title: %q", a)
	}
	if a == shared || b == shared {
		t.Fatalf("row title used the description sentence: a=%q b=%q", a, b)
	}
}

// With no id to name the row, a non-generic description is still an acceptable
// fallback; a generic placeholder (or nothing) falls back to "model".
func TestModelPickerDisplayNameFallsBackToDescriptionOnlyWithoutID(t *testing.T) {
	if got := modelPickerDisplayName("", "Friendly Name"); got != "Friendly Name" {
		t.Fatalf("empty id should fall back to the description, got %q", got)
	}
	if got := modelPickerDisplayName("", "catalog default"); got != "model" {
		t.Fatalf("generic description with no id should yield %q, got %q", "model", got)
	}
	if got := modelPickerDisplayName("", ""); got != "model" {
		t.Fatalf("empty id and description should yield %q, got %q", "model", got)
	}
}
