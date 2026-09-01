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
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
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
	baseline := scratchFileSnapshot(root)

	scratchPath := filepath.Join(root, "_debug file.py")
	if err := os.WriteFile(scratchPath, []byte("print('debug')"), 0o644); err != nil {
		t.Fatal(err)
	}

	warning := scratchFileWarning(root, baseline)
	if warning == "" {
		t.Fatal("expected a warning about the untracked scratch file")
	}
	if !strings.Contains(warning, "_debug file.py") {
		t.Fatalf("expected warning to mention _debug file.py, got %q", warning)
	}
}

func TestScratchFileWarningDetectsShellCreatedScratchFile(t *testing.T) {
	root := t.TempDir()
	runGitForScratchTest(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForScratchTest(t, root, "add", "README.md")
	runGitForScratchTest(t, root, "commit", "-m", "init")
	baseline := scratchFileSnapshot(root)

	// Simulate a shell heredoc/redirect creating a scratch file. The warning is
	// based on the git before/after state, not only FileTracker-created paths.
	if err := os.WriteFile(filepath.Join(root, "_fix_test.py"), []byte("print('debug')"), 0o644); err != nil {
		t.Fatal(err)
	}

	warning := scratchFileWarning(root, baseline)
	if !strings.Contains(warning, "_fix_test.py") {
		t.Fatalf("expected warning to mention shell-created _fix_test.py, got %q", warning)
	}
}

func TestScratchFileWarningSilentForNormalDeliverableFile(t *testing.T) {
	root := t.TempDir()
	runGitForScratchTest(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForScratchTest(t, root, "add", "README.md")
	runGitForScratchTest(t, root, "commit", "-m", "init")
	baseline := scratchFileSnapshot(root)

	if err := os.WriteFile(filepath.Join(root, "app.py"), []byte("print('app')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if warning := scratchFileWarning(root, baseline); warning != "" {
		t.Fatalf("expected no warning for a normal new deliverable file, got %q", warning)
	}
}

func TestScratchFileWarningSilentForUnderscoreDeliverableFile(t *testing.T) {
	root := t.TempDir()
	runGitForScratchTest(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForScratchTest(t, root, "add", "README.md")
	runGitForScratchTest(t, root, "commit", "-m", "init")
	baseline := scratchFileSnapshot(root)

	if err := os.WriteFile(filepath.Join(root, "_generated.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if warning := scratchFileWarning(root, baseline); warning != "" {
		t.Fatalf("expected no warning for an underscore-prefixed deliverable file, got %q", warning)
	}
}

func TestScratchFileWarningSilentWhenFileIsTracked(t *testing.T) {
	root := t.TempDir()
	runGitForScratchTest(t, root, "init")
	baseline := scratchFileSnapshot(root)
	trackedPath := filepath.Join(root, "_debug.py")
	if err := os.WriteFile(trackedPath, []byte("print('app')"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForScratchTest(t, root, "add", "_debug.py")
	runGitForScratchTest(t, root, "commit", "-m", "init")

	// A scratch-like file subsequently staged/committed by the
	// model itself is no longer a loose scratch file, so no warning.
	if warning := scratchFileWarning(root, baseline); warning != "" {
		t.Fatalf("expected no warning for a tracked file, got %q", warning)
	}
}

func TestScratchFileWarningSilentWhenNoFilesCreated(t *testing.T) {
	root := t.TempDir()
	runGitForScratchTest(t, root, "init")
	baseline := scratchFileSnapshot(root)
	if warning := scratchFileWarning(root, baseline); warning != "" {
		t.Fatalf("expected no warning when nothing was created, got %q", warning)
	}
}

func TestScratchFileWarningSilentWhenFileWasRemovedAgain(t *testing.T) {
	root := t.TempDir()
	runGitForScratchTest(t, root, "init")
	baseline := scratchFileSnapshot(root)
	scratchPath := filepath.Join(root, "_debug.py")
	if err := os.WriteFile(scratchPath, []byte("print('debug')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(scratchPath); err != nil {
		t.Fatal(err)
	}
	// The model created and then cleaned up its own scratch file — nothing to warn about.
	if warning := scratchFileWarning(root, baseline); warning != "" {
		t.Fatalf("expected no warning for a removed file, got %q", warning)
	}
}

func TestScratchFileWarningSilentForPreexistingScratchFile(t *testing.T) {
	root := t.TempDir()
	runGitForScratchTest(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "_debug.py"), []byte("print('debug')"), 0o644); err != nil {
		t.Fatal(err)
	}
	baseline := scratchFileSnapshot(root)

	if warning := scratchFileWarning(root, baseline); warning != "" {
		t.Fatalf("expected no warning for a pre-existing scratch file, got %q", warning)
	}
}

func TestScratchFileWarningSilentWhenNotAGitRepo(t *testing.T) {
	root := t.TempDir()
	scratchPath := filepath.Join(root, "_debug.py")
	if err := os.WriteFile(scratchPath, []byte("print('debug')"), 0o644); err != nil {
		t.Fatal(err)
	}
	if warning := scratchFileWarning(root, scratchFileSnapshot(root)); warning != "" {
		t.Fatalf("expected no warning outside a git repo, got %q", warning)
	}
}
