package sandbox

import (
	"path/filepath"
	"testing"
)

func TestDeriveScope(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		args     map[string]any
		wantRaw  string
		wantKind ScopeKind
	}{
		{name: "write file path", tool: "write_file", args: map[string]any{"path": "src/main.go", "content": "x"}, wantRaw: "src/main.go", wantKind: ScopeFile},
		{name: "edit file path", tool: "edit_file", args: map[string]any{"path": "a/b.txt"}, wantRaw: "a/b.txt", wantKind: ScopeFile},
		{name: "file key", tool: "read_file", args: map[string]any{"file": "go.mod"}, wantRaw: "go.mod", wantKind: ScopeFile},
		{name: "directory key", tool: "list_directory", args: map[string]any{"directory": "pkg"}, wantRaw: "pkg", wantKind: ScopeDir},
		{name: "dir key", tool: "glob", args: map[string]any{"dir": "internal"}, wantRaw: "internal", wantKind: ScopeDir},
		{name: "bash explicit cwd", tool: "bash", args: map[string]any{"command": "ls", "cwd": "services/api"}, wantRaw: "services/api", wantKind: ScopeDir},
		{name: "bash workspace-root cwd is tool-wide", tool: "bash", args: map[string]any{"command": "ls", "cwd": "."}, wantRaw: "", wantKind: ScopeToolWide},
		{name: "no path-like args", tool: "bash", args: map[string]any{"command": "ls"}, wantRaw: "", wantKind: ScopeToolWide},
		{name: "path wins over cwd", tool: "x", args: map[string]any{"cwd": "a", "path": "b"}, wantRaw: "b", wantKind: ScopeFile},
		{name: "non-string path ignored", tool: "write_file", args: map[string]any{"path": 42}, wantRaw: "", wantKind: ScopeToolWide},
		{name: "whitespace path is tool-wide", tool: "write_file", args: map[string]any{"path": "  "}, wantRaw: "", wantKind: ScopeToolWide},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, kind := DeriveScope(tt.tool, tt.args)
			if raw != tt.wantRaw || kind != tt.wantKind {
				t.Fatalf("DeriveScope(%q,%v) = (%q,%q), want (%q,%q)", tt.tool, tt.args, raw, kind, tt.wantRaw, tt.wantKind)
			}
		})
	}
}

func TestResolveScopeAbs(t *testing.T) {
	root := filepath.Join(string(filepath.Separator)+"proj", "a")
	abs := filepath.Join(root, "src", "main.go")

	if got := resolveScopeAbs("src/main.go", root); got != abs {
		t.Fatalf("relative anchored = %q, want %q", got, abs)
	}
	if got := resolveScopeAbs("./src/main.go", root); got != abs {
		t.Fatalf("./relative cleaned = %q, want %q", got, abs)
	}
	if got := resolveScopeAbs(abs, root); got != abs {
		t.Fatalf("absolute passthrough = %q, want %q", got, abs)
	}
	if got := resolveScopeAbs("", root); got != "" {
		t.Fatalf("empty scope = %q, want empty", got)
	}
	// A grant made in one workspace must never resolve into another.
	otherRoot := filepath.Join(string(filepath.Separator)+"proj", "b")
	if resolveScopeAbs("src/main.go", root) == resolveScopeAbs("src/main.go", otherRoot) {
		t.Fatalf("same relative scope resolved equal across workspaces")
	}
	// Empty root falls back to filepath.Abs (process cwd anchored, deterministic).
	wantAbs, _ := filepath.Abs("src/main.go")
	if got := resolveScopeAbs("src/main.go", ""); got != wantAbs {
		t.Fatalf("empty-root resolve = %q, want %q", got, wantAbs)
	}
}

func TestGrantCovers(t *testing.T) {
	dir := filepath.Join(string(filepath.Separator)+"proj", "src")
	file := filepath.Join(dir, "main.go")
	sibling := filepath.Join(dir, "other.go")
	descendant := filepath.Join(dir, "api", "z.go")
	parent := filepath.Join(string(filepath.Separator) + "proj")
	siblingDir := filepath.Join(string(filepath.Separator)+"proj", "srcfoo", "x.go")

	toolWide := Grant{ScopeKind: ScopeToolWide}
	fileGrant := Grant{Scope: file, ScopeKind: ScopeFile}
	dirGrant := Grant{Scope: dir, ScopeKind: ScopeDir}

	cases := []struct {
		name  string
		grant Grant
		req   string
		want  bool
	}{
		{"tool-wide covers a scoped request", toolWide, file, true},
		{"tool-wide covers an empty request", toolWide, "", true},
		{"file covers its exact path", fileGrant, file, true},
		{"file does not cover a sibling", fileGrant, sibling, false},
		{"file does not cover an empty request", fileGrant, "", false},
		{"dir covers itself", dirGrant, dir, true},
		{"dir covers a descendant", dirGrant, descendant, true},
		{"dir does not cover a sibling dir prefix", dirGrant, siblingDir, false},
		{"dir does not cover its parent", dirGrant, parent, false},
		{"dir does not cover an empty request", dirGrant, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := grantCovers(tc.grant, tc.req); got != tc.want {
				t.Fatalf("grantCovers(%+v, %q) = %v, want %v", tc.grant, tc.req, got, tc.want)
			}
		})
	}
}
