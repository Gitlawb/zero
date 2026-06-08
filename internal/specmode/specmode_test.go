package specmode

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/tools"
)

func TestSaveDraftWritesPlainMarkdownWithCollisionSuffix(t *testing.T) {
	root := t.TempDir()
	now := fixedSpecTime("2026-06-08T10:00:00Z")

	first, err := SaveDraft(SaveOptions{
		WorkspaceRoot: root,
		Title:         "OAuth Redirect",
		Plan:          "# Goal\n\nImplement redirect handling.",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("SaveDraft first returned error: %v", err)
	}
	second, err := SaveDraft(SaveOptions{
		WorkspaceRoot: root,
		Title:         "OAuth Redirect",
		Plan:          "# Goal\n\nImplement redirect handling again.",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("SaveDraft second returned error: %v", err)
	}

	if first.ID != "2026-06-08-oauth-redirect" || second.ID != "2026-06-08-oauth-redirect-2" {
		t.Fatalf("unexpected ids: first=%q second=%q", first.ID, second.ID)
	}
	if first.RelativePath != ".zero/specs/2026-06-08-oauth-redirect.md" {
		t.Fatalf("relative path = %q", first.RelativePath)
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(first.RelativePath)))
	if err != nil {
		t.Fatalf("read saved spec: %v", err)
	}
	if got := string(content); got != "# Goal\n\nImplement redirect handling.\n" {
		t.Fatalf("saved content = %q", got)
	}
}

func TestExitToolSavesSpecAndReturnsReviewControl(t *testing.T) {
	root := t.TempDir()
	tool := NewExitTool(root, fixedSpecTime("2026-06-08T11:00:00Z"))

	result := tool.Run(context.Background(), map[string]any{
		"title": "Spec Mode",
		"plan":  "# Goal\n\nAdd spec mode.",
	})

	if result.Status != tools.StatusOK {
		t.Fatalf("ExitSpecMode status = %s output=%s", result.Status, result.Output)
	}
	if result.Meta["control"] != ControlSpecReviewRequired {
		t.Fatalf("control meta = %#v", result.Meta)
	}
	if result.Meta["specId"] != "2026-06-08-spec-mode" {
		t.Fatalf("specId meta = %#v", result.Meta)
	}
	if len(result.ChangedFiles) != 1 || result.ChangedFiles[0] != ".zero/specs/2026-06-08-spec-mode.md" {
		t.Fatalf("changed files = %#v", result.ChangedFiles)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(result.ChangedFiles[0]))); err != nil {
		t.Fatalf("expected spec file to exist: %v", err)
	}
	if !strings.Contains(result.Output, result.ChangedFiles[0]) {
		t.Fatalf("output should mention relative path, got %q", result.Output)
	}
}

func TestExitToolRejectsInvalidArgs(t *testing.T) {
	result := NewExitTool(t.TempDir(), nil).Run(context.Background(), map[string]any{
		"title": "Missing plan",
	})
	if result.Status != tools.StatusError || !strings.Contains(result.Output, "plan is required") {
		t.Fatalf("unexpected invalid arg result: %#v", result)
	}
}

func fixedSpecTime(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
}
