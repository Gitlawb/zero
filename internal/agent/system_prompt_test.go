package agent

import (
	"strings"
	"testing"
)

func TestCoreSystemPromptIncludesCodingQualityRules(t *testing.T) {
	prompt := strings.ToLower(buildSystemPrompt(Options{}))

	for _, want := range []string{
		"read-before-edit",
		"inspect the target file",
		"plan then act",
		"choose the narrowest tool",
		"prefer edit_file or apply_patch",
		"verify after edits",
		"honor the active permission mode",
		"avoid broad refactors",
		"final response",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("expected core system prompt to include %q, got:\n%s", want, buildSystemPrompt(Options{}))
		}
	}
}
