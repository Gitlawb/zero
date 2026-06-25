package sandbox

import (
	"path/filepath"
	"testing"
)

func newScopeTestStore(t *testing.T) *GrantStore {
	t.Helper()
	store, err := NewGrantStore(StoreOptions{
		FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json"),
		Now:      fixedSandboxTime("2026-06-05T14:30:00Z"),
	})
	if err != nil {
		t.Fatalf("NewGrantStore: %v", err)
	}
	return store
}

func TestLookupMatchesFileScopeExactlyNotSiblings(t *testing.T) {
	store := newScopeTestStore(t)
	file := filepath.Join(string(filepath.Separator)+"proj", "src", "main.go")
	sibling := filepath.Join(string(filepath.Separator)+"proj", "src", "other.go")
	if _, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, Scope: file, ScopeKind: ScopeFile}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if m, err := store.Lookup("write_file", file); err != nil || !m.Matched {
		t.Fatalf("exact file should match: m=%#v err=%v", m, err)
	}
	if m, _ := store.Lookup("write_file", sibling); m.Matched {
		t.Fatalf("sibling should not match a file grant: %#v", m)
	}
	if m, _ := store.Lookup("write_file", ""); m.Matched {
		t.Fatalf("tool-wide request should not match a file grant: %#v", m)
	}
}

func TestLookupDirScopeCoversSubtree(t *testing.T) {
	store := newScopeTestStore(t)
	dir := filepath.Join(string(filepath.Separator)+"proj", "src")
	descendant := filepath.Join(dir, "api", "z.go")
	outside := filepath.Join(string(filepath.Separator)+"proj", "lib", "x.go")
	if _, err := store.Grant(GrantInput{ToolName: "list_directory", Decision: GrantAllow, Scope: dir, ScopeKind: ScopeDir}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if m, _ := store.Lookup("list_directory", descendant); !m.Matched {
		t.Fatalf("descendant should match dir grant")
	}
	if m, _ := store.Lookup("list_directory", outside); m.Matched {
		t.Fatalf("outside path should not match dir grant")
	}
}

func TestLookupDenyWinsOverAllow(t *testing.T) {
	store := newScopeTestStore(t)
	dir := filepath.Join(string(filepath.Separator)+"proj", "secrets")
	under := filepath.Join(dir, "creds.txt")
	outside := filepath.Join(string(filepath.Separator)+"proj", "readme.md")
	if _, err := store.Grant(GrantInput{ToolName: "read_file", Decision: GrantAllow}); err != nil {
		t.Fatalf("grant allow: %v", err)
	}
	if _, err := store.Grant(GrantInput{ToolName: "read_file", Decision: GrantDeny, Scope: dir, ScopeKind: ScopeDir}); err != nil {
		t.Fatalf("grant deny: %v", err)
	}
	if m, _ := store.Lookup("read_file", under); !m.Matched || m.Grant.Decision != GrantDeny {
		t.Fatalf("deny should win under the deny subtree: %#v", m)
	}
	if m, _ := store.Lookup("read_file", outside); !m.Matched || m.Grant.Decision != GrantAllow {
		t.Fatalf("path outside deny subtree should get tool-wide allow: %#v", m)
	}
}

func TestLookupMostSpecificAllowWins(t *testing.T) {
	store := newScopeTestStore(t)
	dir := filepath.Join(string(filepath.Separator)+"proj", "src")
	file := filepath.Join(dir, "main.go")
	if _, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, Scope: dir, ScopeKind: ScopeDir}); err != nil {
		t.Fatalf("grant dir: %v", err)
	}
	if _, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, Scope: file, ScopeKind: ScopeFile}); err != nil {
		t.Fatalf("grant file: %v", err)
	}
	if m, _ := store.Lookup("write_file", file); !m.Matched || m.Grant.ScopeKind != ScopeFile {
		t.Fatalf("most specific (file) allow should win: %#v", m)
	}
}

func TestGrantReplacesSameScope(t *testing.T) {
	store := newScopeTestStore(t)
	file := filepath.Join(string(filepath.Separator)+"proj", "src", "main.go")
	if _, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, Scope: file, ScopeKind: ScopeFile}); err != nil {
		t.Fatalf("first grant: %v", err)
	}
	if _, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantDeny, Scope: file, ScopeKind: ScopeFile}); err != nil {
		t.Fatalf("second grant: %v", err)
	}
	grants, _ := store.List()
	if len(grants) != 1 || grants[0].Decision != GrantDeny {
		t.Fatalf("re-granting the same scope should replace, not duplicate: %#v", grants)
	}
}

func TestLookupTrimsWhitespacePaddedKeys(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sandbox-grants.json")
	v2 := `{"schemaVersion":2,"grants":{" write_file ":[{"toolName":"write_file","decision":"allow","approvedAt":"2026-06-05T14:30:00Z"}]}}`
	if err := writeText(path, v2); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	store, err := NewGrantStore(StoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if m, err := store.Lookup("write_file", ""); err != nil || !m.Matched {
		t.Fatalf("whitespace-padded key should still match: m=%#v err=%v", m, err)
	}
}

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
		{name: "dot-slash root is tool-wide", tool: "bash", args: map[string]any{"command": "ls", "cwd": "./"}, wantRaw: "", wantKind: ScopeToolWide},
		{name: "dot-slash-dot root is tool-wide", tool: "list_directory", args: map[string]any{"directory": "./."}, wantRaw: "", wantKind: ScopeToolWide},
		{name: "dot-dot-collapse root is tool-wide", tool: "write_file", args: map[string]any{"path": "a/.."}, wantRaw: "", wantKind: ScopeToolWide},
		{name: "no path-like args", tool: "bash", args: map[string]any{"command": "ls"}, wantRaw: "", wantKind: ScopeToolWide},
		{name: "path wins over cwd", tool: "x", args: map[string]any{"cwd": "a", "path": "b"}, wantRaw: "b", wantKind: ScopeFile},
		{name: "non-string path ignored", tool: "write_file", args: map[string]any{"path": 42}, wantRaw: "", wantKind: ScopeToolWide},
		{name: "whitespace path is tool-wide", tool: "write_file", args: map[string]any{"path": "  "}, wantRaw: "", wantKind: ScopeToolWide},
		{name: "web fetch url host", tool: "web_fetch", args: map[string]any{"url": "https://Example.COM:443/path?q=1"}, wantRaw: "example.com", wantKind: ScopeHost},
		{name: "web fetch invalid url is tool-wide", tool: "web_fetch", args: map[string]any{"url": "://bad"}, wantRaw: "", wantKind: ScopeToolWide},
		{name: "browser open url host", tool: "browser_open", args: map[string]any{"url": "https://Example.COM/path?q=1"}, wantRaw: "example.com", wantKind: ScopeHost},
		{name: "browser open bare host", tool: "browser_open", args: map[string]any{"url": "Example.COM/path"}, wantRaw: "example.com", wantKind: ScopeHost},
		{name: "browser open loopback bare host with port", tool: "browser_open", args: map[string]any{"url": "localhost:8080/path"}, wantRaw: "localhost", wantKind: ScopeHost},
		{name: "browser open about blank is tool-wide", tool: "browser_open", args: map[string]any{"url": "about:blank"}, wantRaw: "", wantKind: ScopeToolWide},
		{name: "browser open javascript url is tool-wide", tool: "browser_open", args: map[string]any{"url": "javascript:alert(1)"}, wantRaw: "", wantKind: ScopeToolWide},
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
	// Build a genuinely-absolute root. A leading separator (\proj\a) is "rooted"
	// but NOT absolute on Windows (filepath.IsAbs wants a volume like C:), so the
	// absolute-passthrough case below would be treated as relative and doubled.
	root, err := filepath.Abs(filepath.Join("proj", "a"))
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
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
	otherRoot, err := filepath.Abs(filepath.Join("proj", "b"))
	if err != nil {
		t.Fatalf("abs other root: %v", err)
	}
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
	hostGrant := Grant{Scope: "example.com", ScopeKind: ScopeHost}

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
		{"host covers exact host case-insensitively", hostGrant, "EXAMPLE.com", true},
		{"host does not cover subdomain", hostGrant, "api.example.com", false},
		{"host does not cover empty request", hostGrant, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := grantCovers(tc.grant, tc.req); got != tc.want {
				t.Fatalf("grantCovers(%+v, %q) = %v, want %v", tc.grant, tc.req, got, tc.want)
			}
		})
	}
}
