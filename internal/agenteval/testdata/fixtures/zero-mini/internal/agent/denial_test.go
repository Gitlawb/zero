package agent

import (
	"os"
	"strings"
	"testing"
)

func TestPromptMentionsDeniedOperations(t *testing.T) {
	data, err := os.ReadFile("system_prompt.md")
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if !strings.Contains(string(data), "denied") && !strings.Contains(string(data), "blocked") {
		t.Fatalf("prompt should describe denied operations:\n%s", data)
	}
}
