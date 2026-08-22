package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
)

func mustPatchHeaderPaths(t *testing.T, patch string) []string {
	t.Helper()
	paths, err := sandbox.PatchHeaderPaths(patch)
	if err != nil {
		t.Fatalf("PatchHeaderPaths: %v", err)
	}
	return paths
}

func TestPatchHeaderPathsHandlesQuotedAndSpacedNames(t *testing.T) {
	// git C-quotes a path that contains a space; the old strings.Fields parse kept
	// only the first whitespace-delimited token ("a/dir/file" and the literal `name`
	// pieces would split), losing the real name. The whole post-prefix value, with a
	// trailing tab timestamp dropped and the quotes removed, must survive (L18).
	patch := "--- \"a/dir/file name.go\"\t2024-01-01 00:00:00\n" +
		"+++ \"b/dir/file name.go\"\n" +
		"@@ -1 +1 @@\n-old\n+new\n"
	got := mustPatchHeaderPaths(t, patch)
	if !slices.Contains(got, "dir/file name.go") {
		t.Fatalf("quoted spaced path not extracted: %v", got)
	}
}

func TestPatchHeaderPathsUnspacedStillWorks(t *testing.T) {
	patch := "--- a/x.go\n+++ b/x.go\n@@ -1 +1 @@\n-old\n+new\n"
	got := mustPatchHeaderPaths(t, patch)
	if !slices.Contains(got, "x.go") {
		t.Fatalf("plain path not extracted: %v", got)
	}
	// /dev/null (a deletion/creation sentinel) must not be reported as a target.
	if slices.Contains(got, "/dev/null") {
		t.Fatalf("/dev/null should not be a header path: %v", got)
	}
}

// git only C-quotes names containing characters it must escape, so a filename
// whose last byte is an ordinary space reaches the header raw. git apply reads
// it to the tab or newline and patches that exact file, so trimming the operand
// here would leave the token gate comparing against a neighbouring pathname.
func TestPatchHeaderPathsPreservesSurroundingSpacesInNames(t *testing.T) {
	patch := "--- a/bridge-token \t2024-01-01 00:00:00\n" +
		"+++ b/bridge-token \n" +
		"@@ -1 +1 @@\n-old\n+new\n"
	got := mustPatchHeaderPaths(t, patch)
	if !slices.Contains(got, "bridge-token ") {
		t.Fatalf("trailing-space path not preserved: %q", got)
	}
	if slices.Contains(got, "bridge-token") {
		t.Fatalf("trailing space trimmed off the header path: %q", got)
	}
}

func TestPatchHeaderPathsHandlesCQuotedDiffGitOperands(t *testing.T) {
	patch := "diff --git \"a/bridge\\040token\" \"b/exposed\\tcopy\"\n"
	got := mustPatchHeaderPaths(t, patch)
	want := []string{"bridge token", "exposed\tcopy"}
	if !slices.Equal(got, want) {
		t.Fatalf("C-quoted diff --git operands = %q, want %q", got, want)
	}
}

func TestPatchHeaderPathsPreservesTrailingSpaceInBinaryDiffOperands(t *testing.T) {
	patch := "diff --git a/bridge-token  b/bridge-token \n" +
		"GIT binary patch\n"
	got := mustPatchHeaderPaths(t, patch)
	want := []string{"bridge-token ", "bridge-token "}
	if !slices.Equal(got, want) {
		t.Fatalf("binary diff paths = %q, want %q", got, want)
	}
}

func TestPatchHeaderPathsRejectsAmbiguousDiffOperands(t *testing.T) {
	patch := "diff --git a/source b/part b/destination\nGIT binary patch\n"
	if paths, err := sandbox.PatchHeaderPaths(patch); err == nil {
		t.Fatalf("PatchHeaderPaths = %q, want ambiguous-path error", paths)
	}
}

func TestPatchHeaderPathsParsesGitDefaultAndNoPrefixOutput(t *testing.T) {
	for _, operation := range []string{"rename", "copy", "modify"} {
		for _, noPrefix := range []bool{false, true} {
			name := operation + "/default-prefix"
			if noPrefix {
				name = operation + "/no-prefix"
			}
			t.Run(name, func(t *testing.T) {
				patch, source, destination := gitGeneratedPatch(t, operation, noPrefix)
				got := mustPatchHeaderPaths(t, patch)
				for _, want := range []string{source, destination} {
					if !slices.Contains(got, want) {
						t.Fatalf("Git-generated %s paths = %q, want %q\npatch:\n%s", operation, got, want, patch)
					}
				}
			})
		}
	}
}

func TestPatchHeaderPathsRejectsMismatchedNoPrefixRenameMetadata(t *testing.T) {
	patch := "diff --git source.txt destination.txt\n" +
		"similarity index 100%\n" +
		"rename from other.txt\n" +
		"rename to destination.txt\n"
	if paths, err := sandbox.PatchHeaderPaths(patch); err == nil {
		t.Fatalf("PatchHeaderPaths = %q, want mismatched-header error", paths)
	}
}

func gitGeneratedPatch(t *testing.T, operation string, noPrefix bool) (string, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	dir := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		return string(output)
	}
	runGit("init", "-q")
	runGit("config", "user.name", "Zero Tests")
	runGit("config", "user.email", "zero-tests@example.invalid")

	source := "source name.txt"
	destination := "destination name.txt"
	if operation == "modify" {
		source = "quoted-\u00e9.txt"
		destination = source
	}
	if err := os.WriteFile(filepath.Join(dir, source), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit("add", "--", source)
	runGit("commit", "-qm", "base")

	switch operation {
	case "rename":
		if err := os.Rename(filepath.Join(dir, source), filepath.Join(dir, destination)); err != nil {
			t.Fatal(err)
		}
	case "copy":
		if err := os.WriteFile(filepath.Join(dir, destination), []byte("before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	case "modify":
		if err := os.WriteFile(filepath.Join(dir, source), []byte("after\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatalf("unknown operation %q", operation)
	}
	runGit("add", "-A")
	args := []string{"diff", "--cached"}
	if noPrefix {
		args = append(args, "--no-prefix")
	}
	switch operation {
	case "rename":
		args = append(args, "-M100%")
	case "copy":
		args = append(args, "--find-copies-harder", "-C100%")
	}
	patch := runGit(args...)
	if operation != "modify" && !strings.Contains(patch, operation+" from ") {
		t.Fatalf("git did not produce %s metadata:\n%s", operation, patch)
	}
	return patch, source, destination
}
