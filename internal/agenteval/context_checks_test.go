package agenteval

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestEvaluateContextChecksReportsMissingAndForbiddenFiles(t *testing.T) {
	workspace := t.TempDir()
	writeContextFile(t, workspace, "docs/present.md")
	writeContextFile(t, workspace, "logs/leak.txt")

	got, err := (ContextChecks{
		RequiredFiles:  []string{"docs/present.md", "docs/missing.md"},
		ForbiddenFiles: []string{"logs/leak.txt", "logs/not-there.txt"},
	}).CheckWorkspace(workspace)

	if err != nil {
		t.Fatalf("CheckWorkspace returned error: %v", err)
	}
	if !reflect.DeepEqual(got.MissingRequiredFiles, []string{"docs/missing.md"}) {
		t.Fatalf("missing required = %#v", got.MissingRequiredFiles)
	}
	if !reflect.DeepEqual(got.PresentForbiddenFiles, []string{"logs/leak.txt"}) {
		t.Fatalf("present forbidden = %#v", got.PresentForbiddenFiles)
	}
}

func TestEvaluateContextChecksRejectsMissingWorkspace(t *testing.T) {
	_, err := (ContextChecks{
		RequiredFiles: []string{"docs/present.md"},
	}).CheckWorkspace(filepath.Join(t.TempDir(), "missing"))

	if err == nil {
		t.Fatal("expected workspace error")
	}
}

func writeContextFile(t *testing.T, workspace string, file string) {
	t.Helper()
	path := filepath.Join(workspace, filepath.FromSlash(file))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
}
