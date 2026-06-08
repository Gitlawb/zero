package tui

import "testing"

func TestModelSupportsVisionTUI(t *testing.T) {
	cases := []struct {
		modelName string
		want      bool
	}{
		{modelName: "gpt-4.1", want: true},                  // vision model in the catalog
		{modelName: "claude-sonnet-4.5", want: true},        // vision model in the catalog
		{modelName: "claude-haiku-3.5", want: true},         // has vision capability
		{modelName: "totally-unknown-custom", want: false},  // not in catalog -> can't confirm
		{modelName: "", want: false},
	}
	for _, tc := range cases {
		got := modelSupportsVisionTUI(tc.modelName)
		if got != tc.want {
			t.Fatalf("modelSupportsVisionTUI(%q) = %v, want %v", tc.modelName, got, tc.want)
		}
	}
}
