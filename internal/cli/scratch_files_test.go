package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitForScratchTest(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

func TestScratchFileWarningFlagsUntrackedCreatedFile(t *testing.T) {
	root := t.TempDir()
	runGitForScratchTest(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForScratchTest(t, root, "add", "README.md")
	runGitForScratchTest(t, root, "commit", "-m", "init")

	scratchPath := filepath.Join(root, "_debug.py")
	if err := os.WriteFile(scratchPath, []byte("print('debug')"), 0o644); err != nil {
		t.Fatal(err)
	}

	warning := scratchFileWarning(root, []string{scratchPath})
	if warning == "" {
		t.Fatal("expected a warning about the untracked scratch file")
	}
	if !strings.Contains(warning, "_debug.py") {
		t.Fatalf("expected warning to mention _debug.py, got %q", warning)
	}
}

func TestScratchFileWarningSilentWhenFileIsTracked(t *testing.T) {
	root := t.TempDir()
	runGitForScratchTest(t, root, "init")
	trackedPath := filepath.Join(root, "app.py")
	if err := os.WriteFile(trackedPath, []byte("print('app')"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForScratchTest(t, root, "add", "app.py")
	runGitForScratchTest(t, root, "commit", "-m", "init")

	// A file created by write_file but subsequently staged/committed by the
	// model itself is no longer a loose scratch file, so no warning.
	if warning := scratchFileWarning(root, []string{trackedPath}); warning != "" {
		t.Fatalf("expected no warning for a tracked file, got %q", warning)
	}
}

func TestScratchFileWarningSilentWhenNoFilesCreated(t *testing.T) {
	root := t.TempDir()
	runGitForScratchTest(t, root, "init")
	if warning := scratchFileWarning(root, nil); warning != "" {
		t.Fatalf("expected no warning when nothing was created, got %q", warning)
	}
}

func TestScratchFileWarningSilentWhenFileWasRemovedAgain(t *testing.T) {
	root := t.TempDir()
	runGitForScratchTest(t, root, "init")
	scratchPath := filepath.Join(root, "_debug.py")
	if err := os.WriteFile(scratchPath, []byte("print('debug')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(scratchPath); err != nil {
		t.Fatal(err)
	}
	// The model created and then cleaned up its own scratch file — nothing to warn about.
	if warning := scratchFileWarning(root, []string{scratchPath}); warning != "" {
		t.Fatalf("expected no warning for a removed file, got %q", warning)
	}
}

func TestScratchFileWarningSilentWhenNotAGitRepo(t *testing.T) {
	root := t.TempDir()
	scratchPath := filepath.Join(root, "_debug.py")
	if err := os.WriteFile(scratchPath, []byte("print('debug')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if warning := scratchFileWarning(root, []string{scratchPath}); warning != "" {
		t.Fatalf("expected no warning outside a git repo, got %q", warning)
	}
}
