package workspaceindex

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScanBuildsDeterministicWorkspaceSummary(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.test/repo\n")
	writeFile(t, root, "README.md", "# Example\n")
	writeFile(t, root, "package.json", `{"name":"example"}`)
	writeFile(t, root, "cmd/zero/main.go", "package main\n")
	writeFile(t, root, "internal/app/app.go", "package app\n")
	writeFile(t, root, "web/app.ts", "export const app = true\n")
	writeFile(t, root, "zero.exe", "ignored binary")
	writeFile(t, root, ".git/config", "[core]\n")
	writeFile(t, root, ".zero/state.json", "{}")
	writeFile(t, root, "node_modules/pkg/index.js", "ignored")
	writeFile(t, root, "vendor/lib/lib.go", "ignored")
	writeFile(t, root, "dist/bundle.js", "ignored")
	writeFile(t, root, "coverage/out.txt", "ignored")
	writeFile(t, root, ".next/cache.js", "ignored")
	writeFile(t, root, ".cache/blob", "ignored")

	got, err := Scan(root, Options{MaxDepth: DefaultMaxDepth})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if got.Root != filepath.Clean(root) {
		t.Fatalf("Root=%q want %q", got.Root, filepath.Clean(root))
	}
	wantFiles := []string{
		"README.md",
		"cmd/zero/main.go",
		"go.mod",
		"internal/app/app.go",
		"package.json",
		"web/app.ts",
	}
	if !reflect.DeepEqual(pathsOf(got.Files), wantFiles) {
		t.Fatalf("Files=%v want %v", pathsOf(got.Files), wantFiles)
	}
	if got.TotalFiles != len(wantFiles) {
		t.Fatalf("TotalFiles=%d want %d", got.TotalFiles, len(wantFiles))
	}
	wantLanguages := map[string]int{
		"Go":         2,
		"JSON":       1,
		"Markdown":   1,
		"TypeScript": 1,
	}
	if !reflect.DeepEqual(got.LanguageCounts, wantLanguages) {
		t.Fatalf("LanguageCounts=%v want %v", got.LanguageCounts, wantLanguages)
	}
	if !got.Files[0].Important || got.Files[0].Path != "README.md" {
		t.Fatalf("README.md should be marked important, got %#v", got.Files[0])
	}
}

func TestScanHonorsTraversalCaps(t *testing.T) {
	t.Run("max files keeps first deterministic files", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "a.go", "package a\n")
		writeFile(t, root, "b.go", "package b\n")
		writeFile(t, root, "c.go", "package c\n")

		got, err := Scan(root, Options{MaxFiles: 2})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if want := []string{"a.go", "b.go"}; !reflect.DeepEqual(pathsOf(got.Files), want) {
			t.Fatalf("Files=%v want %v", pathsOf(got.Files), want)
		}
		if !got.Truncated {
			t.Fatal("Truncated=false want true")
		}
	})

	t.Run("max depth zero includes root files only", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "top.go", "package top\n")
		writeFile(t, root, "pkg/one.go", "package pkg\n")

		got, err := Scan(root, Options{MaxDepth: 0})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if want := []string{"top.go"}; !reflect.DeepEqual(pathsOf(got.Files), want) {
			t.Fatalf("Files=%v want %v", pathsOf(got.Files), want)
		}
		if !got.Truncated {
			t.Fatal("Truncated=false want true")
		}
	})

	t.Run("max depth includes retained directories without files", func(t *testing.T) {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "a", "b", "c"), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}

		got, err := Scan(root, Options{MaxDepth: 3})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if got.DirectoryCount != 3 {
			t.Fatalf("DirectoryCount=%d want 3", got.DirectoryCount)
		}
		if got.MaxDepth != 3 {
			t.Fatalf("MaxDepth=%d want deepest retained directory depth 3", got.MaxDepth)
		}
	})

	t.Run("max bytes per file name skips overlong entries", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, root, "short.go", "package short\n")
		writeFile(t, root, "longname.go", "package longname\n")

		got, err := Scan(root, Options{MaxBytesPerFileName: len("short.go")})
		if err != nil {
			t.Fatalf("Scan: %v", err)
		}
		if want := []string{"short.go"}; !reflect.DeepEqual(pathsOf(got.Files), want) {
			t.Fatalf("Files=%v want %v", pathsOf(got.Files), want)
		}
		if !got.Truncated {
			t.Fatal("Truncated=false want true")
		}
	})
}

func TestHelpersClassifySharedWorkspaceRules(t *testing.T) {
	for _, name := range []string{".git", ".zero", ".cache", ".next", "node_modules", "vendor", "dist", "build", "coverage", ".worktrees"} {
		if !ShouldSkipDir(name) {
			t.Fatalf("ShouldSkipDir(%q)=false want true", name)
		}
	}
	for _, file := range []string{".git", ".DS_Store", "bin/app.exe", "archive.tar", "archive.tgz", "lib.so", "image.png"} {
		if !ShouldSkipFile(file) {
			t.Fatalf("ShouldSkipFile(%q)=false want true", file)
		}
		if file != ".git" && file != ".DS_Store" && !LooksBinaryPath(file) {
			t.Fatalf("LooksBinaryPath(%q)=false want true", file)
		}
	}
	for path, want := range map[string]string{
		"main.go":          "Go",
		"src/app.tsx":      "TypeScript",
		"src/app.jsx":      "JavaScript",
		"README.md":        "Markdown",
		"config.yaml":      "YAML",
		"Cargo.toml":       "TOML",
		"unknown.zerolang": "",
	} {
		if got := LanguageForPath(path); got != want {
			t.Fatalf("LanguageForPath(%q)=%q want %q", path, got, want)
		}
	}
	if !IsImportantPath("docs/AGENTS.md") || !IsImportantPath("go.mod") {
		t.Fatal("expected AGENTS.md and go.mod to be important")
	}
}

func TestFileDepthHandlesNativeSeparators(t *testing.T) {
	for rel, want := range map[string]int{
		"main.go":               0,
		"pkg/one.go":            1,
		`pkg\one.go`:            1,
		`internal\app\app.go`:   2,
		"internal/app/app.go":   2,
		`internal/app\mixed.go`: 2,
	} {
		if got := FileDepth(rel); got != want {
			t.Fatalf("FileDepth(%q)=%d want %d", rel, got, want)
		}
	}
	for rel, want := range map[string]int{
		"pkg":                  1,
		"pkg/nested":           2,
		`pkg\nested`:           2,
		`internal\app\service`: 3,
	} {
		if got := PathDepth(rel); got != want {
			t.Fatalf("PathDepth(%q)=%d want %d", rel, got, want)
		}
	}
}

func TestHandleWalkErrorKeepsScanningAfterUnreadableSubdir(t *testing.T) {
	truncated := false
	handled, err := HandleWalkError("root", "root/blocked", fakeDirEntry{dir: true}, os.ErrPermission, &truncated)
	if !handled {
		t.Fatal("handled=false want true")
	}
	if err != filepath.SkipDir {
		t.Fatalf("err=%v want filepath.SkipDir", err)
	}
	if !truncated {
		t.Fatal("truncated=false want true")
	}
}

func writeFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func pathsOf(files []File) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

type fakeDirEntry struct {
	dir bool
}

func (entry fakeDirEntry) Name() string {
	return "fake"
}

func (entry fakeDirEntry) IsDir() bool {
	return entry.dir
}

func (entry fakeDirEntry) Type() fs.FileMode {
	if entry.dir {
		return fs.ModeDir
	}
	return 0
}

func (entry fakeDirEntry) Info() (fs.FileInfo, error) {
	return nil, os.ErrInvalid
}
