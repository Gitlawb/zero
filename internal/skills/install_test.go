package skills

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initGitSkillRepo creates a real local git repo holding a skill and returns a
// file:// URL for it, so the DEFAULT git runner (system git) is exercised end to
// end rather than only the injected runner. The test is skipped when git is
// unavailable.
func initGitSkillRepo(t *testing.T, content string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	writeSourceSkill(t, repo, content)
	run("add", "-A")
	run("commit", "-qm", "init")
	return "file://" + repo
}

func TestInstallFromRealLocalGitRepo(t *testing.T) {
	destDir := t.TempDir()
	url := initGitSkillRepo(t, "---\nname: from-git\ndescription: cloned\n---\nbody from git\n")

	result, err := Install(context.Background(), InstallOptions{Source: url, Dir: destDir})
	if err != nil {
		t.Fatalf("Install from git: %v", err)
	}
	if result.Name != "from-git" {
		t.Fatalf("Name = %q, want from-git", result.Name)
	}
	got, ok := Get(destDir, "from-git")
	if !ok || !strings.Contains(got.Content, "body from git") {
		t.Fatalf("installed git skill not discoverable: ok=%v skill=%+v", ok, got)
	}
	// The .git clone metadata must not leak into the skills dir.
	if _, err := os.Stat(filepath.Join(destDir, "from-git", ".git")); err == nil {
		t.Fatalf(".git metadata must not be installed into the skills dir")
	}
}

// writeSourceSkill lays out a candidate skill directory containing a SKILL.md so
// it can be used as a local install source.
func writeSourceSkill(t *testing.T, dir string, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, skillFileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return dir
}

func TestInstallCopiesLocalSkillAndRecordsHash(t *testing.T) {
	destDir := t.TempDir()
	source := writeSourceSkill(t, filepath.Join(t.TempDir(), "src"),
		"---\nname: confirmation-policy\ndescription: Ask first.\n---\nAsk before risky actions.\n")

	result, err := Install(context.Background(), InstallOptions{Source: source, Dir: destDir})
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if result.Name != "confirmation-policy" {
		t.Fatalf("Name = %q, want confirmation-policy", result.Name)
	}
	if result.Hash == "" {
		t.Fatalf("expected a recorded content hash")
	}
	if result.Updated {
		t.Fatalf("first install should not be flagged as an update")
	}

	// The skill is discoverable through the normal loader.
	got, ok := Get(destDir, "confirmation-policy")
	if !ok {
		t.Fatalf("installed skill not discoverable via Get")
	}
	if !strings.Contains(got.Content, "Ask before risky actions.") {
		t.Fatalf("installed content unexpected: %q", got.Content)
	}

	// The lockfile records name -> source + hash.
	entries, err := ReadLock(destDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	entry, ok := entries["confirmation-policy"]
	if !ok {
		t.Fatalf("lockfile missing entry for confirmation-policy: %#v", entries)
	}
	if entry.Hash != result.Hash {
		t.Fatalf("lockfile hash %q != install hash %q", entry.Hash, result.Hash)
	}
	// The recorded source is the canonical (absolute, symlink-resolved) local path.
	if entry.Source != canonicalSource(source) {
		t.Fatalf("lockfile source = %q, want %q", entry.Source, canonicalSource(source))
	}
}

func TestInstallRejectsInvalidSkill(t *testing.T) {
	destDir := t.TempDir()
	// A source directory with no SKILL.md is not a valid skill.
	src := filepath.Join(t.TempDir(), "empty")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err == nil {
		t.Fatalf("expected an error for a source without SKILL.md")
	}
	// Nothing should have been written into the destination dir.
	if entries, _ := os.ReadDir(destDir); len(entries) != 0 {
		t.Fatalf("invalid source must not write into dest, found: %#v", entries)
	}
}

func TestInstallRejectsSkillWithBlankFrontmatterName(t *testing.T) {
	destDir := t.TempDir()
	// Frontmatter name is blank, so the name falls back to the source dir's base —
	// which is itself not a usable skill name (validSkillName rejects "..") — so
	// there is nothing valid to install under. (A whitespace-only dir name would be
	// the other no-usable-name case, but that is not creatable on Windows.)
	src := writeSourceSkill(t, filepath.Join(t.TempDir(), "no..usable..name"),
		"---\nname:    \n---\nbody\n")

	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir}); err == nil {
		t.Fatalf("expected rejection of a skill with no usable name")
	}
}

func TestInstallReinstallShowsHashChange(t *testing.T) {
	destDir := t.TempDir()
	srcRoot := t.TempDir()
	src := writeSourceSkill(t, filepath.Join(srcRoot, "src"),
		"---\nname: demo\ndescription: v1\n---\nfirst body\n")

	first, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err != nil {
		t.Fatalf("first Install: %v", err)
	}

	// Change the source content and reinstall.
	writeSourceSkill(t, src, "---\nname: demo\ndescription: v2\n---\nsecond body\n")
	second, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if !second.Updated {
		t.Fatalf("reinstall with changed content should be flagged as an update")
	}
	if second.PreviousHash != first.Hash {
		t.Fatalf("PreviousHash = %q, want %q", second.PreviousHash, first.Hash)
	}
	if second.Hash == first.Hash {
		t.Fatalf("hash should change when content changes")
	}

	// The lockfile reflects the new hash.
	entries, err := ReadLock(destDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if entries["demo"].Hash != second.Hash {
		t.Fatalf("lockfile not updated: %q != %q", entries["demo"].Hash, second.Hash)
	}

	// The installed content is the new version.
	got, _ := Get(destDir, "demo")
	if !strings.Contains(got.Content, "second body") {
		t.Fatalf("reinstall did not overwrite content: %q", got.Content)
	}
}

func TestConcurrentInstallsPreserveEveryLockEntry(t *testing.T) {
	destDir := t.TempDir()
	const count = 12
	sources := make([]string, count)
	for index := range count {
		content := fmt.Sprintf("---\nname: concurrent-%02d\ndescription: test\n---\nbody\n", index)
		sources[index] = writeSourceSkill(t, filepath.Join(t.TempDir(), "src"), content)
	}

	errs := make(chan error, count)
	for _, source := range sources {
		go func() {
			_, err := Install(context.Background(), InstallOptions{Source: source, Dir: destDir})
			errs <- err
		}()
	}
	for range count {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Install: %v", err)
		}
	}

	entries, err := ReadLock(destDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if len(entries) != count {
		t.Fatalf("lockfile has %d entries, want %d: %#v", len(entries), count, entries)
	}
}

// TestInstallSameLocalSourceDifferentSpellingIsNotAClash verifies that a local
// source installed via one spelling (e.g. a relative path) and re-installed via
// an equivalent spelling (the absolute path) is treated as the same source, not
// a clash, because the recorded source is canonicalized.
func TestInstallSameLocalSourceDifferentSpellingIsNotAClash(t *testing.T) {
	destDir := t.TempDir()
	srcRoot := t.TempDir()
	abs := writeSourceSkill(t, filepath.Join(srcRoot, "src"),
		"---\nname: demo\ndescription: v1\n---\nbody\n")

	if _, err := Install(context.Background(), InstallOptions{Source: abs, Dir: destDir}); err != nil {
		t.Fatalf("first install: %v", err)
	}

	// Reinstall using a different textual spelling of the same directory (a
	// redundant "/./" segment). canonicalSource normalizes both spellings to the
	// same absolute path, so this must not be treated as a clash. (A cwd-relative
	// spelling can't be expressed across drives on Windows, where the temp dir and
	// the repo live on different volumes, so use a same-directory alternate.)
	messy := filepath.Dir(abs) + string(filepath.Separator) + "." + string(filepath.Separator) + filepath.Base(abs)
	if _, err := Install(context.Background(), InstallOptions{Source: messy, Dir: destDir}); err != nil {
		t.Fatalf("reinstall with an equivalent spelling should not clash: %v", err)
	}

	want := canonicalSource(abs)
	entries, err := ReadLock(destDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if entries["demo"].Source != want {
		t.Fatalf("lockfile should record the canonical source %q, got %q", want, entries["demo"].Source)
	}
}

func TestInstallNameClashWarnsAndDoesNotOverwriteWithoutForce(t *testing.T) {
	destDir := t.TempDir()
	// Pre-existing skill installed from source A.
	srcA := writeSourceSkill(t, filepath.Join(t.TempDir(), "a"),
		"---\nname: shared\ndescription: original\n---\noriginal body\n")
	if _, err := Install(context.Background(), InstallOptions{Source: srcA, Dir: destDir}); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	// A different source declaring the same name must not silently overwrite.
	srcB := writeSourceSkill(t, filepath.Join(t.TempDir(), "b"),
		"---\nname: shared\ndescription: replacement\n---\nreplacement body\n")
	_, err := Install(context.Background(), InstallOptions{Source: srcB, Dir: destDir})
	if !errors.Is(err, ErrNameClash) {
		t.Fatalf("expected ErrNameClash without Force, got %v", err)
	}

	// The original content survives.
	got, _ := Get(destDir, "shared")
	if !strings.Contains(got.Content, "original body") {
		t.Fatalf("clash should not overwrite: %q", got.Content)
	}

	// With Force the install proceeds and overwrites.
	result, err := Install(context.Background(), InstallOptions{Source: srcB, Dir: destDir, Force: true})
	if err != nil {
		t.Fatalf("forced reinstall: %v", err)
	}
	if !result.Updated {
		t.Fatalf("forced overwrite should be flagged as an update")
	}
	got, _ = Get(destDir, "shared")
	if !strings.Contains(got.Content, "replacement body") {
		t.Fatalf("forced overwrite did not replace content: %q", got.Content)
	}
}

// TestInstallReinstallSameSourceUnchangedIsNotAClash verifies that re-running an
// install from the SAME recorded source is treated as an idempotent update, not a
// name clash (the clash guard only protects against a DIFFERENT source).
func TestInstallReinstallSameSourceUnchangedIsNotAClash(t *testing.T) {
	destDir := t.TempDir()
	src := writeSourceSkill(t, filepath.Join(t.TempDir(), "src"),
		"---\nname: demo\ndescription: v1\n---\nbody\n")
	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir}); err != nil {
		t.Fatalf("first install: %v", err)
	}
	result, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err != nil {
		t.Fatalf("idempotent reinstall from same source should succeed, got %v", err)
	}
	if result.Updated {
		t.Fatalf("reinstall of identical content from same source is not an update")
	}
}

func TestInstallGitSourceUsesRunnerAndDoesNotExecuteContent(t *testing.T) {
	destDir := t.TempDir()
	cloneRoot := t.TempDir()

	executed := false
	runner := func(ctx context.Context, destination string, source string) error {
		// Emulate a clone by laying down a skill plus a hostile executable that
		// must NEVER be run during install.
		writeSourceSkill(t, destination, "---\nname: remote\ndescription: fetched\n---\nremote body\n")
		script := filepath.Join(destination, "install.sh")
		if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+filepath.Join(cloneRoot, "PWNED")+"\n"), 0o755); err != nil {
			return err
		}
		executed = true
		return nil
	}

	result, err := Install(context.Background(), InstallOptions{
		Source:    "https://example.com/remote-skill.git",
		Dir:       destDir,
		GitRunner: runner,
	})
	if err != nil {
		t.Fatalf("Install via git runner: %v", err)
	}
	if !executed {
		t.Fatalf("git runner was not invoked for a URL source")
	}
	if result.Name != "remote" {
		t.Fatalf("Name = %q, want remote", result.Name)
	}
	// The hostile install script must not have run.
	if _, err := os.Stat(filepath.Join(cloneRoot, "PWNED")); err == nil {
		t.Fatalf("install must never execute fetched content")
	}
	// The fetched executable must not be copied into the skills dir either (skills
	// are markdown — only SKILL.md is installed, not arbitrary fetched files).
	if _, err := os.Stat(filepath.Join(destDir, "remote", "install.sh")); err == nil {
		t.Fatalf("install must not copy fetched executable into the skills dir")
	}
}

func TestRemoveDeletesSkillAndLockEntry(t *testing.T) {
	destDir := t.TempDir()
	src := writeSourceSkill(t, filepath.Join(t.TempDir(), "src"),
		"---\nname: demo\ndescription: d\n---\nbody\n")
	if _, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir}); err != nil {
		t.Fatalf("install: %v", err)
	}

	if err := Remove(destDir, "demo"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, ok := Get(destDir, "demo"); ok {
		t.Fatalf("skill still present after Remove")
	}
	entries, err := ReadLock(destDir)
	if err != nil {
		t.Fatalf("ReadLock: %v", err)
	}
	if _, ok := entries["demo"]; ok {
		t.Fatalf("lockfile entry survived Remove: %#v", entries)
	}
}

func TestRemoveUnknownSkillErrors(t *testing.T) {
	if err := Remove(t.TempDir(), "nope"); err == nil {
		t.Fatalf("expected an error removing an unknown skill")
	}
}

func TestInfoReturnsFrontmatterSourceAndHash(t *testing.T) {
	destDir := t.TempDir()
	src := writeSourceSkill(t, filepath.Join(t.TempDir(), "src"),
		"---\nname: demo\ndescription: described\n---\nbody text\n")
	installed, err := Install(context.Background(), InstallOptions{Source: src, Dir: destDir})
	if err != nil {
		t.Fatalf("install: %v", err)
	}

	info, ok := Info(destDir, "demo")
	if !ok {
		t.Fatalf("Info(demo) not found")
	}
	if info.Skill.Description != "described" {
		t.Fatalf("Info description = %q", info.Skill.Description)
	}
	if info.Source != installed.Source {
		t.Fatalf("Info source = %q, want %q", info.Source, installed.Source)
	}
	if info.Hash != installed.Hash {
		t.Fatalf("Info hash = %q, want %q", info.Hash, installed.Hash)
	}
}
