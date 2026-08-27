package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLooksLikePosixAbsolute(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{path: "/home/x", want: true},
		{path: "C:\\foo", want: false},
		{path: "C:/foo", want: false},
		{path: "//unc/share", want: false},
		{path: "relative/path", want: false},
		{path: "", want: false},
		{path: " /tmp/x ", want: true},
	}
	for _, tt := range tests {
		if got := looksLikePosixAbsolute(tt.path); got != tt.want {
			t.Fatalf("looksLikePosixAbsolute(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestRewritePosixWorkspacePath(t *testing.T) {
	workspace := filepath.Join("workspaces", "zero")
	tests := []struct {
		name      string
		goos      string
		workspace string
		requested string
		want      string
	}{
		{
			name:      "windows home repo file",
			goos:      "windows",
			workspace: workspace,
			requested: "/home/alice/zero/internal/tools/read_file.go",
			want:      "internal/tools/read_file.go",
		},
		{
			name:      "windows home repo root",
			goos:      "windows",
			workspace: workspace,
			requested: "/home/alice/zero",
			want:      ".",
		},
		{
			name:      "windows mac-style users prefix",
			goos:      "windows",
			workspace: workspace,
			requested: "/Users/alice/zero/pkg/x.go",
			want:      "pkg/x.go",
		},
		{
			name:      "windows tmp repo file",
			goos:      "windows",
			workspace: workspace,
			requested: "/tmp/zero/foo.txt",
			want:      "foo.txt",
		},
		{
			name:      "windows tmp without repo stays",
			goos:      "windows",
			workspace: workspace,
			requested: "/tmp/foo.txt",
			want:      "/tmp/foo.txt",
		},
		{
			name:      "windows foreign repo stays",
			goos:      "windows",
			workspace: workspace,
			requested: "/home/alice/otherrepo/file.go",
			want:      "/home/alice/otherrepo/file.go",
		},
		{
			name:      "linux does not rewrite",
			goos:      "linux",
			workspace: workspace,
			requested: "/home/alice/zero/file.go",
			want:      "/home/alice/zero/file.go",
		},
		{
			name:      "windows var tmp repo file",
			goos:      "windows",
			workspace: workspace,
			requested: "/var/tmp/zero/foo.txt",
			want:      "foo.txt",
		},
		{
			name:      "workspace named home does not naive-strip",
			goos:      "windows",
			workspace: filepath.Join("workspaces", "home"),
			requested: "/home/user/file",
			want:      "/home/user/file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rewritePosixWorkspacePath(tt.goos, tt.workspace, tt.requested)
			if got != tt.want {
				t.Fatalf("rewritePosixWorkspacePath(%q, %q, %q) = %q, want %q", tt.goos, tt.workspace, tt.requested, got, tt.want)
			}
		})
	}
}

func TestAnnotatePosixWindowsPathError(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zero")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	missing := &os.PathError{Op: "stat", Path: "/tmp/does-not-exist-xyz", Err: os.ErrNotExist}

	got := annotatePosixWindowsPathError("windows", root, "/tmp/does-not-exist-xyz", missing)
	if got == nil {
		t.Fatal("expected wrapped missing-path error")
	}
	if !errors.Is(got, missing) {
		t.Fatalf("wrapped error should unwrap to original, got %v", got)
	}
	msg := got.Error()
	if !strings.Contains(msg, "Windows") {
		t.Fatalf("hint missing Windows host: %q", msg)
	}
	if !strings.Contains(msg, root) {
		t.Fatalf("hint missing workspace root %q: %q", root, msg)
	}
	if !strings.Contains(msg, "POSIX") {
		t.Fatalf("hint missing POSIX path wording: %q", msg)
	}

	if got := annotatePosixWindowsPathError("linux", root, "/tmp/does-not-exist-xyz", missing); got != missing {
		t.Fatalf("linux should not wrap missing-path errors, got %v", got)
	}

	confine := outsideWorkspaceError("/home/alice/zero/../secret")
	if got := annotatePosixWindowsPathError("windows", root, "/home/alice/zero/../secret", confine); got != confine {
		t.Fatalf("confinement errors must not be wrapped, got %v", got)
	}

	if got := annotatePosixWindowsPathError("windows", root, "/tmp/x", nil); got != nil {
		t.Fatalf("nil error should stay nil, got %v", got)
	}

	relativeMiss := &os.PathError{Op: "stat", Path: "notes.txt", Err: os.ErrNotExist}
	if got := annotatePosixWindowsPathError("windows", root, "notes.txt", relativeMiss); got != relativeMiss {
		t.Fatalf("relative paths should not be annotated, got %v", got)
	}
}

func TestReadFileToolRewritesSyntheticPosixPrefixOnWindows(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zero")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	writeTestFile(t, filepath.Join(root, "notes.txt"), "hello from notes\n")

	absolute, relative, err := resolveWorkspacePathForGOOS("windows", root, "/home/alice/zero/notes.txt")
	if err != nil {
		t.Fatalf("resolve rewritten posix path: %v", err)
	}
	if relative != "notes.txt" {
		t.Fatalf("relative = %q, want notes.txt", relative)
	}
	got, err := os.ReadFile(absolute)
	if err != nil {
		t.Fatalf("read resolved path: %v", err)
	}
	if string(got) != "hello from notes\n" {
		t.Fatalf("content = %q, want %q", got, "hello from notes\n")
	}
}

func TestResolveWorkspacePathAnnotatesPosixMissOnWindows(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zero")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	_, _, err := resolveWorkspacePathForGOOS("windows", root, "/tmp/does-not-exist-xyz")
	if err == nil {
		t.Fatal("expected missing-path error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Windows") {
		t.Fatalf("error missing Windows host hint: %q", msg)
	}
	if !strings.Contains(msg, root) {
		t.Fatalf("error missing workspace root %q: %q", root, msg)
	}
}

func TestResolveWorkspacePathDoesNotRewriteForeignRepo(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zero")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	writeTestFile(t, filepath.Join(root, "x"), "workspace x")

	requested := "/home/alice/otherrepo/x"
	if got := rewritePosixWorkspacePath("windows", root, requested); got != requested {
		t.Fatalf("rewrote foreign repo path %q to %q", requested, got)
	}

	_, relative, err := resolveWorkspacePathForGOOS("windows", root, requested)
	if err == nil {
		t.Fatal("foreign repo path should not resolve to a workspace file")
	}
	if relative == "x" {
		t.Fatalf("foreign repo path was treated as workspace file x")
	}
}
