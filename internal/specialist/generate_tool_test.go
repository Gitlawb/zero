package specialist

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

func TestGenerateToolCreatesSpecialist(t *testing.T) {
	userDir := filepath.Join(t.TempDir(), "user")
	tool := NewGenerateTool(NewStorage(Paths{UserDir: userDir}))

	result := tool.Run(context.Background(), map[string]any{
		"description":   "API review helper",
		"name":          "api-review",
		"system_prompt": "Review API diffs.",
		"tools":         []any{"read-only", "plan"},
	})

	if result.Status != tools.StatusOK {
		t.Fatalf("GenerateSpecialist status = %s output=%s", result.Status, result.Output)
	}
	path := filepath.Join(userDir, "api-review.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("expected generated specialist file: %v", err)
	}
	if !strings.Contains(string(data), `name: "api-review"`) || !strings.Contains(string(data), "Review API diffs.") {
		t.Fatalf("unexpected generated file:\n%s", string(data))
	}
	if result.Meta["path"] != path || result.Meta["name"] != "api-review" {
		t.Fatalf("unexpected result meta: %#v", result.Meta)
	}
}

func TestGenerateToolDerivesNameAndDefaultPrompt(t *testing.T) {
	userDir := filepath.Join(t.TempDir(), "user")
	tool := NewGenerateTool(NewStorage(Paths{UserDir: userDir}))

	result := tool.Run(context.Background(), map[string]any{"description": "Security Audit Helper"})

	if result.Status != tools.StatusOK {
		t.Fatalf("GenerateSpecialist status = %s output=%s", result.Status, result.Output)
	}
	data, err := os.ReadFile(filepath.Join(userDir, "security-audit-helper.md"))
	if err != nil {
		t.Fatalf("expected generated specialist file: %v", err)
	}
	if !strings.Contains(string(data), "Purpose: Security Audit Helper") {
		t.Fatalf("default prompt missing purpose:\n%s", string(data))
	}
}

func TestGenerateToolRejectsInvalidLocation(t *testing.T) {
	tool := NewGenerateTool(NewStorage(Paths{UserDir: t.TempDir()}))

	result := tool.Run(context.Background(), map[string]any{
		"description": "Bad location",
		"location":    "remote",
	})

	if result.Status != tools.StatusError || !strings.Contains(result.Output, "location must be user or project") {
		t.Fatalf("invalid location result = %#v", result)
	}
}
