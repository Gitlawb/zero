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
	if _, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, MaxAutonomy: AutonomyHigh, Scope: file, ScopeKind: ScopeFile}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if m, err := store.Lookup("write_file", file, AutonomyHigh); err != nil || !m.Matched {
		t.Fatalf("exact file should match: m=%#v err=%v", m, err)
	}
	if m, _ := store.Lookup("write_file", sibling, AutonomyHigh); m.Matched {
		t.Fatalf("sibling should not match a file grant: %#v", m)
	}
	if m, _ := store.Lookup("write_file", "", AutonomyHigh); m.Matched {
		t.Fatalf("tool-wide request should not match a file grant: %#v", m)
	}
}

func TestLookupDirScopeCoversSubtree(t *testing.T) {
	store := newScopeTestStore(t)
	dir := filepath.Join(string(filepath.Separator)+"proj", "src")
	descendant := filepath.Join(dir, "api", "z.go")
	outside := filepath.Join(string(filepath.Separator)+"proj", "lib", "x.go")
	if _, err := store.Grant(GrantInput{ToolName: "list_directory", Decision: GrantAllow, MaxAutonomy: AutonomyHigh, Scope: dir, ScopeKind: ScopeDir}); err != nil {
		t.Fatalf("grant: %v", err)
	}
	if m, _ := store.Lookup("list_directory", descendant, AutonomyHigh); !m.Matched {
		t.Fatalf("descendant should match dir grant")
	}
	if m, _ := store.Lookup("list_directory", outside, AutonomyHigh); m.Matched {
		t.Fatalf("outside path should not match dir grant")
	}
}

func TestLookupDenyWinsOverAllow(t *testing.T) {
	store := newScopeTestStore(t)
	dir := filepath.Join(string(filepath.Separator)+"proj", "secrets")
	under := filepath.Join(dir, "creds.txt")
	outside := filepath.Join(string(filepath.Separator)+"proj", "readme.md")
	if _, err := store.Grant(GrantInput{ToolName: "read_file", Decision: GrantAllow, MaxAutonomy: AutonomyHigh}); err != nil {
		t.Fatalf("grant allow: %v", err)
	}
	if _, err := store.Grant(GrantInput{ToolName: "read_file", Decision: GrantDeny, MaxAutonomy: AutonomyHigh, Scope: dir, ScopeKind: ScopeDir}); err != nil {
		t.Fatalf("grant deny: %v", err)
	}
	if m, _ := store.Lookup("read_file", under, AutonomyHigh); !m.Matched || m.Grant.Decision != GrantDeny {
		t.Fatalf("deny should win under the deny subtree: %#v", m)
	}
	if m, _ := store.Lookup("read_file", outside, AutonomyHigh); !m.Matched || m.Grant.Decision != GrantAllow {
		t.Fatalf("path outside deny subtree should get tool-wide allow: %#v", m)
	}
}

func TestLookupMostSpecificAllowWins(t *testing.T) {
	store := newScopeTestStore(t)
	dir := filepath.Join(string(filepath.Separator)+"proj", "src")
	file := filepath.Join(dir, "main.go")
	if _, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, MaxAutonomy: AutonomyHigh, Scope: dir, ScopeKind: ScopeDir}); err != nil {
		t.Fatalf("grant dir: %v", err)
	}
	if _, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, MaxAutonomy: AutonomyHigh, Scope: file, ScopeKind: ScopeFile}); err != nil {
		t.Fatalf("grant file: %v", err)
	}
	if m, _ := store.Lookup("write_file", file, AutonomyHigh); !m.Matched || m.Grant.ScopeKind != ScopeFile {
		t.Fatalf("most specific (file) allow should win: %#v", m)
	}
}

func TestGrantReplacesSameScope(t *testing.T) {
	store := newScopeTestStore(t)
	file := filepath.Join(string(filepath.Separator)+"proj", "src", "main.go")
	store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, MaxAutonomy: AutonomyMedium, Scope: file, ScopeKind: ScopeFile})
	store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, MaxAutonomy: AutonomyHigh, Scope: file, ScopeKind: ScopeFile})
	grants, _ := store.List()
	if len(grants) != 1 || grants[0].MaxAutonomy != AutonomyHigh {
		t.Fatalf("re-granting the same scope should replace, not duplicate: %#v", grants)
	}
}

func TestMigrateV1FileToToolWideGrant(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sandbox-grants.json")
	v1 := `{"schemaVersion":1,"grants":{"write_file":{"toolName":"write_file","decision":"allow","maxAutonomy":"high","approvedAt":"2026-06-05T14:30:00Z"}}}`
	if err := writeText(path, v1); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	store, err := NewGrantStore(StoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	scoped := filepath.Join(root, "src", "main.go")
	m, err := store.Lookup("write_file", scoped, AutonomyHigh)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !m.Matched || m.Grant.ScopeKind != ScopeToolWide {
		t.Fatalf("v1 grant should migrate to tool-wide: %#v", m)
	}
	if _, err := store.Grant(GrantInput{ToolName: "bash", Decision: GrantAllow, MaxAutonomy: AutonomyHigh}); err != nil {
		t.Fatalf("grant after migrate: %v", err)
	}
	if grants, _ := store.List(); len(grants) != 2 {
		t.Fatalf("expected 2 grants after migrate+add, got %#v", grants)
	}
}

func TestLookupTrimsWhitespacePaddedKeys(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sandbox-grants.json")
	v2 := `{"schemaVersion":2,"grants":{" write_file ":[{"toolName":"write_file","decision":"allow","maxAutonomy":"high","approvedAt":"2026-06-05T14:30:00Z"}]}}`
	if err := writeText(path, v2); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	store, err := NewGrantStore(StoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	if m, err := store.Lookup("write_file", "", AutonomyHigh); err != nil || !m.Matched {
		t.Fatalf("whitespace-padded key should still match: m=%#v err=%v", m, err)
	}
}
