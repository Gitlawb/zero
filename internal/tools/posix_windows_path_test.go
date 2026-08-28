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
		{path: "/tmp/zero/file", want: true},
		{path: `\Windows\System32`, want: false},
		{path: `\tmp\zero\file`, want: false},
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
		{
			name:      "windows home repo basename case differs",
			goos:      "windows",
			workspace: filepath.Join("workspaces", "Zero"),
			requested: "/home/alice/zero/go.mod",
			want:      "go.mod",
		},
		{
			name:      "windows tmp repo basename case differs",
			goos:      "windows",
			workspace: filepath.Join("workspaces", "Zero"),
			requested: "/tmp/zero/foo.txt",
			want:      "foo.txt",
		},
		{
			name:      "windows posix tmp file still rewrites",
			goos:      "windows",
			workspace: workspace,
			requested: "/tmp/zero/file",
			want:      "file",
		},
		{
			name:      "windows tmp double slash rewrites",
			goos:      "windows",
			workspace: workspace,
			requested: "/tmp/zero//file",
			want:      "file",
		},
		{
			name:      "windows rooted backslash system32 stays",
			goos:      "windows",
			workspace: workspace,
			requested: `\Windows\System32`,
			want:      `\Windows\System32`,
		},
		{
			name:      "windows rooted tmp backslash stays",
			goos:      "windows",
			workspace: workspace,
			requested: `\tmp\zero\file`,
			want:      `\tmp\zero\file`,
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
	if !strings.Contains(msg, "POSIX") {
		t.Fatalf("hint missing POSIX path wording: %q", msg)
	}
	if strings.Contains(msg, root) {
		t.Fatalf("hint must not name the workspace root %q: %q", root, msg)
	}
	if strings.Contains(msg, "workspace root") {
		t.Fatalf("hint must not name the workspace root: %q", msg)
	}

	etcMiss := &os.PathError{Op: "stat", Path: "/etc/passwd", Err: os.ErrNotExist}
	got = annotatePosixWindowsPathError("windows", root, "/etc/passwd", etcMiss)
	if got == nil {
		t.Fatal("expected wrapped missing-path error for /etc/passwd")
	}
	msg = got.Error()
	if !strings.Contains(msg, "/etc/passwd") {
		t.Fatalf("hint should name the requested POSIX path: %q", msg)
	}
	if strings.Contains(msg, root) {
		t.Fatalf("hint must not name the workspace root %q: %q", root, msg)
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
	if strings.Contains(msg, "workspace root") {
		t.Fatalf("hint must not name the workspace root: %q", msg)
	}
	if strings.Contains(msg, "must stay inside the workspace") {
		t.Fatalf("POSIX miss used confinement instead of a missing-path hint: %q", msg)
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

func TestResolveWorkspacePathRejectsMissingLexicalEscapeOnWindows(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zero")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	_, _, err := resolveWorkspacePathForGOOS("windows", root, "/home/alice/zero/../../missing")
	if err == nil {
		t.Fatal("expected confinement error for a missing escaped target")
	}
	msg := err.Error()
	if !strings.Contains(msg, "must stay inside the workspace") {
		t.Fatalf("expected outsideWorkspaceError, got %q", msg)
	}
	if strings.Contains(msg, "host is Windows") {
		t.Fatalf("missing lexical escape got a POSIX-path hint instead of confinement: %q", msg)
	}
}

func TestResolveWorkspaceTargetPathRewritesMissingTmpFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zero")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	_, relative, err := resolveWorkspaceTargetPathForGOOS("windows", root, "/tmp/zero/new.txt")
	if err != nil {
		t.Fatalf("missing rewritten write target should resolve: %v", err)
	}
	if relative != "new.txt" {
		t.Fatalf("relative = %q, want new.txt", relative)
	}
}

func TestResolveWorkspacePathRewritesDoubleSlashTmpFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zero")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	writeTestFile(t, filepath.Join(root, "file"), "workspace file")

	_, relative, err := resolveWorkspacePathForGOOS("windows", root, "/tmp/zero//file")
	if err != nil {
		t.Fatalf("double-slash POSIX tmp path should resolve: %v", err)
	}
	if relative != "file" {
		t.Fatalf("relative = %q, want file", relative)
	}
}

func TestResolveWorkspaceTargetPathRejectsLexicalEscape(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zero")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	_, _, err := resolveWorkspaceTargetPathForGOOS("windows", root, "/home/alice/zero/../../new.txt")
	if err == nil {
		t.Fatal("expected confinement error for an escaped write target")
	}
	msg := err.Error()
	if !strings.Contains(msg, "must stay inside the workspace") {
		t.Fatalf("expected outsideWorkspaceError, got %q", msg)
	}
}

func TestIsAbsForGOOS(t *testing.T) {
	tests := []struct {
		goos string
		path string
		want bool
	}{
		{goos: "windows", path: "/tmp/x", want: false},
		{goos: "linux", path: "/tmp/x", want: true},
		{goos: "windows", path: "C:/foo", want: true},
		{goos: "windows", path: "C:\\foo", want: true},
		{goos: "windows", path: "//unc/share", want: true},
		{goos: "windows", path: "relative/path", want: false},
		{goos: "windows", path: "\\Windows\\System32", want: true},
	}
	for _, tt := range tests {
		if got := isAbsForGOOS(tt.goos, tt.path); got != tt.want {
			t.Fatalf("isAbsForGOOS(%q, %q) = %v, want %v", tt.goos, tt.path, got, tt.want)
		}
	}
}

func TestResolveWorkspacePathRejectsRootedWindowsBackslash(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zero")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	writeTestFile(t, filepath.Join(root, "file"), "workspace file")

	for _, requested := range []string{`\Windows\System32`, `\tmp\zero\file`} {
		if got := rewritePosixWorkspacePath("windows", root, requested); got != requested {
			t.Fatalf("rooted Windows path %q rewrote to %q", requested, got)
		}

		_, relative, err := resolveWorkspacePathForGOOS("windows", root, requested)
		if relative == "file" {
			t.Fatalf("rooted Windows path %q resolved as workspace-relative file (err=%v)", requested, err)
		}
		if err == nil {
			t.Fatalf("rooted Windows path %q should not resolve inside the workspace (relative=%q)", requested, relative)
		}
		if !strings.Contains(err.Error(), "must stay inside the workspace") {
			t.Fatalf("rooted Windows path %q: expected confinement, got %q", requested, err)
		}

		_, relative, err = resolveWorkspaceTargetPathForGOOS("windows", root, requested)
		if relative == "file" {
			t.Fatalf("write resolver treated %q as workspace-relative file (err=%v)", requested, err)
		}
		if err == nil {
			t.Fatalf("write resolver accepted rooted Windows path %q (relative=%q)", requested, relative)
		}
		if !strings.Contains(err.Error(), "must stay inside the workspace") {
			t.Fatalf("write resolver for %q: expected confinement, got %q", requested, err)
		}
	}

	if got := rewritePosixWorkspacePath("windows", root, "/tmp/zero/file"); got != "file" {
		t.Fatalf("POSIX /tmp/zero/file should still rewrite to file, got %q", got)
	}
}

func TestJoinAgainstRootWindowsPosix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "zero")
	got := joinAgainstRoot("windows", root, "/tmp/does-not-exist-xyz")
	want := filepath.Join(root, "tmp", "does-not-exist-xyz")
	if got != want {
		t.Fatalf("joinAgainstRoot(windows, root, /tmp/does-not-exist-xyz) = %q, want %q", got, want)
	}
	got = joinAgainstRoot("linux", root, "/tmp/does-not-exist-xyz")
	if got != filepath.Clean("/tmp/does-not-exist-xyz") && got != "/tmp/does-not-exist-xyz" {
		t.Fatalf("joinAgainstRoot(linux, ...) should keep POSIX abs, got %q", got)
	}
}
