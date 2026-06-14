package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestManagerCheckRealGopls drives the exact path the self-correct LSP wiring
// uses (NewManager -> Check) against a REAL gopls and asserts a real diagnostic
// comes back. Skips when gopls is not installed. Temporary manual-verification
// test (the rest of the suite uses a fake server).
func TestManagerCheckRealGopls(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not on PATH; skipping real-server check")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n\ngo 1.24\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := "package main\n\nfunc main() {\n\tvar count int = \"three\"\n\t_ = count\n}\n"
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewManager(root)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.Shutdown(ctx)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	diags, err := m.Check(ctx, path, src)
	if err != nil {
		t.Fatalf("Manager.Check returned error: %v", err)
	}
	errs := FilterBySeverity(diags, SeverityError)
	if len(errs) == 0 {
		t.Fatalf("expected gopls to report the type error, got %d diagnostics: %+v", len(diags), diags)
	}
	t.Logf("real gopls returned %d error diagnostic(s):", len(errs))
	for _, d := range errs {
		t.Logf("  [source=%s] %s", d.Source, d.Message)
	}
}
