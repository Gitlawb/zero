package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrantStorePersistsListsRevokesAndClears(t *testing.T) {
	store, err := NewGrantStore(StoreOptions{
		FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json"),
		Now:      fixedSandboxTime("2026-06-05T14:30:00Z"),
	})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}

	if _, err := store.Grant(GrantInput{ToolName: "bash", Decision: GrantDeny, Reason: "network blocked"}); err != nil {
		t.Fatalf("Grant deny returned error: %v", err)
	}
	allowed, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, Reason: "workspace edits"})
	if err != nil {
		t.Fatalf("Grant allow returned error: %v", err)
	}
	if allowed.ApprovedAt != "2026-06-05T14:30:00Z" {
		t.Fatalf("approvedAt = %q, want fixed timestamp", allowed.ApprovedAt)
	}

	reopened, err := NewGrantStore(StoreOptions{FilePath: store.FilePath()})
	if err != nil {
		t.Fatalf("reopen grant store: %v", err)
	}
	grants, err := reopened.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(grants) != 2 || grants[0].ToolName != "bash" || grants[1].ToolName != "write_file" {
		t.Fatalf("unexpected sorted grants: %#v", grants)
	}

	match, err := reopened.Lookup("write_file", "")
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if !match.Matched || match.Grant.Decision != GrantAllow {
		t.Fatalf("lookup allow = %#v, want matched allow", match)
	}

	revoked, err := reopened.Revoke("bash")
	if err != nil {
		t.Fatalf("Revoke returned error: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("revoked = %d, want 1", revoked)
	}
	cleared, err := reopened.Clear()
	if err != nil {
		t.Fatalf("Clear returned error: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("cleared = %d, want 1", cleared)
	}
	grants, err = reopened.List()
	if err != nil {
		t.Fatalf("List after clear returned error: %v", err)
	}
	if len(grants) != 0 {
		t.Fatalf("expected no grants after clear, got %#v", grants)
	}
}

func TestGrantStoreMigratesExactV1GrantAndReportsOnce(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sandbox-grants.json")
	original := `{"schemaVersion":1,"grants":{"write_file":{"toolName":"write_file","decision":"allow","approvedAt":"2026-06-05T14:30:00Z","reason":"legacy"}}}`
	if err := writeText(path, original); err != nil {
		t.Fatalf("write v1 grants: %v", err)
	}
	store, err := NewGrantStore(StoreOptions{FilePath: path, Now: fixedSandboxTime("2026-06-05T15:00:00Z")})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}

	grants, err := store.List()
	if err != nil {
		t.Fatalf("List v1 grants returned error: %v", err)
	}
	if len(grants) != 1 || grants[0].ToolName != "write_file" || grants[0].Decision != GrantAllow || grants[0].Reason != "legacy" {
		t.Fatalf("unexpected v1 grants: %#v", grants)
	}
	notice, err := store.ConsumeMigrationNotice()
	if err != nil || !strings.Contains(notice, "migrated 1, invalidated 0") {
		t.Fatalf("migration notice = %q err=%v", notice, err)
	}
	if again, err := store.ConsumeMigrationNotice(); err != nil || again != "" {
		t.Fatalf("second migration notice = %q err=%v, want empty", again, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rewritten grant file: %v", err)
	}
	if !strings.Contains(string(raw), `"schemaVersion": 3`) || !strings.Contains(string(raw), `"policyVersion": 1`) || !strings.Contains(string(raw), `"write_file": [`) {
		t.Fatalf("grant file was not rewritten as a versioned grant store:\n%s", raw)
	}
	backup, err := os.ReadFile(path + ".v1.backup")
	if err != nil || string(backup) != original {
		t.Fatalf("migration backup = %q err=%v, want original", backup, err)
	}
}

func TestGrantStoreInvalidatesLegacyShellAllowButKeepsSafePrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sandbox-grants.json")
	original := `{"schemaVersion":2,"grants":{"exec_command":[{"toolName":"exec_command","decision":"allow","approvedAt":"2026-06-05T14:30:00Z"}]},"commandPrefixes":{"exec_command":[{"toolName":"exec_command","prefix":["git","status"],"approvedAt":"2026-06-05T14:30:00Z"}]}}`
	if err := writeText(path, original); err != nil {
		t.Fatal(err)
	}
	store, err := NewGrantStore(StoreOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	grants, err := store.List()
	if err != nil || len(grants) != 0 {
		t.Fatalf("legacy shell allows = %#v err=%v, want invalidated", grants, err)
	}
	prefixes, err := store.ListCommandPrefixes()
	if err != nil || len(prefixes) != 1 || !sameStringSlice(prefixes[0].Prefix, []string{"git", "status"}) {
		t.Fatalf("migrated prefixes = %#v err=%v", prefixes, err)
	}
	notice, err := store.ConsumeMigrationNotice()
	if err != nil || !strings.Contains(notice, "migrated 1, invalidated 1") {
		t.Fatalf("notice = %q err=%v", notice, err)
	}
	backup, err := os.ReadFile(path + ".v2.backup")
	if err != nil || string(backup) != original {
		t.Fatalf("backup = %q err=%v", backup, err)
	}
}

func TestGrantStorePolicyChangePreservesDeniesAndInvalidatesApprovals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sandbox-grants.json")
	original := `{"schemaVersion":3,"policyVersion":0,"grants":{"write_file":[{"toolName":"write_file","decision":"allow","approvedAt":"2026-06-05T14:30:00Z"}],"bash":[{"toolName":"bash","decision":"deny","approvedAt":"2026-06-05T14:30:00Z"}]},"commandPrefixes":{"exec_command":[{"toolName":"exec_command","prefix":["git","status"],"approvedAt":"2026-06-05T14:30:00Z"}]}}`
	if err := writeText(path, original); err != nil {
		t.Fatal(err)
	}
	store, err := NewGrantStore(StoreOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	grants, err := store.List()
	if err != nil || len(grants) != 1 || grants[0].Decision != GrantDeny {
		t.Fatalf("migrated policy grants = %#v err=%v, want deny only", grants, err)
	}
	if prefixes, err := store.ListCommandPrefixes(); err != nil || len(prefixes) != 0 {
		t.Fatalf("changed-policy prefixes = %#v err=%v, want invalidated", prefixes, err)
	}
	if notice, err := store.ConsumeMigrationNotice(); err != nil || !strings.Contains(notice, "migrated 1, invalidated 2") {
		t.Fatalf("notice = %q err=%v", notice, err)
	}
	backup, err := os.ReadFile(path + ".policy-v0.backup")
	if err != nil || string(backup) != original {
		t.Fatalf("policy backup = %q err=%v", backup, err)
	}
}

func TestGrantStorePersistsCommandPrefixes(t *testing.T) {
	store, err := NewGrantStore(StoreOptions{
		FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json"),
		Now:      fixedSandboxTime("2026-06-05T14:30:00Z"),
	})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	grant, err := store.GrantCommandPrefix(CommandPrefixInput{
		ToolName: "bash",
		Prefix:   []string{"git", "status"},
		Reason:   "status checks",
	})
	if err != nil {
		t.Fatalf("GrantCommandPrefix returned error: %v", err)
	}
	if grant.ApprovedAt != "2026-06-05T14:30:00Z" {
		t.Fatalf("approvedAt = %q, want fixed timestamp", grant.ApprovedAt)
	}

	// Re-granting the same prefix updates instead of duplicating.
	if _, err := store.GrantCommandPrefix(CommandPrefixInput{ToolName: "bash", Prefix: []string{"git", "status"}, Reason: "updated"}); err != nil {
		t.Fatalf("GrantCommandPrefix update returned error: %v", err)
	}
	reopened, err := NewGrantStore(StoreOptions{FilePath: store.FilePath()})
	if err != nil {
		t.Fatalf("reopen grant store: %v", err)
	}
	prefixes, err := reopened.ListCommandPrefixes()
	if err != nil {
		t.Fatalf("ListCommandPrefixes returned error: %v", err)
	}
	if len(prefixes) != 1 || prefixes[0].ToolName != "bash" || !sameStringSlice(prefixes[0].Prefix, []string{"git", "status"}) || prefixes[0].Reason != "updated" {
		t.Fatalf("unexpected command prefixes: %#v", prefixes)
	}
	match, matched, err := reopened.LookupCommandPrefix("bash", []string{"git", "status", "--short"})
	if err != nil {
		t.Fatalf("LookupCommandPrefix returned error: %v", err)
	}
	if !matched || !sameStringSlice(match.Prefix, []string{"git", "status"}) {
		t.Fatalf("lookup = (%#v,%t), want git status match", match, matched)
	}
	if _, matched, err := reopened.LookupCommandPrefix("bash", []string{"git", "diff"}); err != nil || matched {
		t.Fatalf("git diff lookup = matched %t err %v, want no match", matched, err)
	}
	text := FormatGrantListWithCommandPrefixes(nil, prefixes)
	for _, want := range []string{"Sandbox Grants:", "bash", "`git status`", "command-prefix", "updated"} {
		if !strings.Contains(text, want) {
			t.Fatalf("formatted grants = %q, missing %q", text, want)
		}
	}

	revoked, err := reopened.Revoke("bash")
	if err != nil {
		t.Fatalf("Revoke returned error: %v", err)
	}
	if revoked != 1 {
		t.Fatalf("revoked = %d, want 1", revoked)
	}
	if prefixes, err := reopened.ListCommandPrefixes(); err != nil || len(prefixes) != 0 {
		t.Fatalf("prefixes after revoke = %#v err %v, want none", prefixes, err)
	}
}

func TestGrantStoreRejectsUnsafeCommandPrefixes(t *testing.T) {
	store, err := NewGrantStore(StoreOptions{FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json")})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	for _, prefix := range [][]string{
		{"find"},
		{"xargs"},
		{"python", "script.py"},
		{"./script.sh"},
		{"git"},
	} {
		if _, err := store.GrantCommandPrefix(CommandPrefixInput{ToolName: "bash", Prefix: prefix}); err == nil {
			t.Fatalf("GrantCommandPrefix(%#v) succeeded, want validation error", prefix)
		}
	}
}

func TestGrantStoreRejectsUnsafeInputsAndMalformedFiles(t *testing.T) {
	root := t.TempDir()
	store, err := NewGrantStore(StoreOptions{FilePath: filepath.Join(root, "sandbox-grants.json")})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	for _, input := range []GrantInput{
		{ToolName: "", Decision: GrantAllow},
		{ToolName: "../escape", Decision: GrantAllow},
		{ToolName: "write_file", Decision: GrantDecision("maybe")},
	} {
		if _, err := store.Grant(input); err == nil {
			t.Fatalf("Grant(%#v) succeeded, want validation error", input)
		}
	}

	if err := writeText(filepath.Join(root, "sandbox-grants.json"), `{"schemaVersion":99}`); err != nil {
		t.Fatalf("write malformed grants: %v", err)
	}
	if _, err := store.List(); err == nil || !strings.Contains(err.Error(), "unsupported schemaVersion") {
		t.Fatalf("expected unsupported schema error, got %v", err)
	}
}

func TestGrantStoreInvalidatesUnsafeLegacyCommandPrefix(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sandbox-grants.json")
	if err := writeText(path, `{"schemaVersion":2,"grants":{},"commandPrefixes":{"bash":[{"toolName":"bash","prefix":["find"],"approvedAt":"2026-06-05T14:30:00Z"}]}}`); err != nil {
		t.Fatalf("write malformed command prefix: %v", err)
	}
	store, err := NewGrantStore(StoreOptions{FilePath: path})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	if prefixes, err := store.ListCommandPrefixes(); err != nil || len(prefixes) != 0 {
		t.Fatalf("unsafe legacy prefixes = %#v err=%v, want invalidated", prefixes, err)
	}
	if notice, err := store.ConsumeMigrationNotice(); err != nil || !strings.Contains(notice, "invalidated 1") {
		t.Fatalf("migration notice = %q err=%v", notice, err)
	}
}

func TestGrantStoreInvalidatesMalformedLegacyToolKeys(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sandbox-grants.json")
	original := `{"schemaVersion":2,"grants":{"../escape":[{"toolName":"../escape","decision":"deny","approvedAt":"2026-06-05T14:30:00Z"}]},"commandPrefixes":{"bad name":[{"toolName":"bad name","prefix":["git","status"],"approvedAt":"2026-06-05T14:30:00Z"}]}}`
	if err := writeText(path, original); err != nil {
		t.Fatal(err)
	}
	store, err := NewGrantStore(StoreOptions{FilePath: path})
	if err != nil {
		t.Fatal(err)
	}
	if grants, err := store.List(); err != nil || len(grants) != 0 {
		t.Fatalf("malformed legacy grants = %#v err=%v, want invalidated", grants, err)
	}
	if prefixes, err := store.ListCommandPrefixes(); err != nil || len(prefixes) != 0 {
		t.Fatalf("malformed legacy prefixes = %#v err=%v, want invalidated", prefixes, err)
	}
	if notice, err := store.ConsumeMigrationNotice(); err != nil || !strings.Contains(notice, "invalidated 2") {
		t.Fatalf("migration notice = %q err=%v", notice, err)
	}
	backup, err := os.ReadFile(path + ".v2.backup")
	if err != nil || string(backup) != original {
		t.Fatalf("backup = %q err=%v, want original", backup, err)
	}
}

func TestResolveGrantPathUsesOverrideAndConfigHome(t *testing.T) {
	override := filepath.Join(t.TempDir(), "custom.json")
	path, err := ResolveGrantPath(map[string]string{"ZERO_SANDBOX_GRANTS_PATH": override})
	if err != nil {
		t.Fatalf("ResolveGrantPath override returned error: %v", err)
	}
	if path != filepath.Clean(override) {
		t.Fatalf("override path = %q, want %q", path, filepath.Clean(override))
	}

	configHome := t.TempDir()
	path, err = ResolveGrantPath(map[string]string{"XDG_CONFIG_HOME": configHome})
	if err != nil {
		t.Fatalf("ResolveGrantPath config home returned error: %v", err)
	}
	want := filepath.Join(configHome, "zero", "sandbox-grants.json")
	if path != want {
		t.Fatalf("config path = %q, want %q", path, want)
	}
}

func TestFormatGrantList(t *testing.T) {
	empty := FormatGrantList(nil)
	if !strings.Contains(empty, "No persistent sandbox grants") {
		t.Fatalf("unexpected empty list text: %q", empty)
	}
	text := FormatGrantList([]Grant{{
		ToolName:   "write_file",
		Decision:   GrantAllow,
		ApprovedAt: "2026-06-05T14:30:00Z",
		Reason:     "workspace edits",
	}})
	for _, want := range []string{"Sandbox Grants:", "write_file", "allow", "workspace edits"} {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %q in formatted grants: %q", want, text)
		}
	}
}

func writeText(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func TestGrantStoreRevokePathRemovesOnlyMatchingScope(t *testing.T) {
	store, err := NewGrantStore(StoreOptions{
		FilePath: filepath.Join(t.TempDir(), "sandbox-grants.json"),
		Now:      fixedSandboxTime("2026-06-05T14:30:00Z"),
	})
	if err != nil {
		t.Fatalf("NewGrantStore: %v", err)
	}
	dir := t.TempDir()
	fileA := filepath.Join(dir, "a.txt")
	fileB := filepath.Join(dir, "b.txt")
	for _, scope := range []string{fileA, fileB} {
		if _, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, Scope: scope, ScopeKind: ScopeFile}); err != nil {
			t.Fatalf("Grant %s: %v", scope, err)
		}
	}
	// A tool-wide grant for the same tool must survive a path-scoped revoke.
	if _, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow}); err != nil {
		t.Fatalf("Grant tool-wide: %v", err)
	}

	removed, err := store.RevokePath("write_file", fileA)
	if err != nil || removed != 1 {
		t.Fatalf("RevokePath(fileA) = (%d,%v), want (1,nil)", removed, err)
	}
	grants, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(grants) != 2 {
		t.Fatalf("expected 2 grants left (fileB + tool-wide), got %d: %#v", len(grants), grants)
	}
	for _, grant := range grants {
		if grant.Scope == fileA {
			t.Fatalf("fileA grant should have been revoked: %#v", grants)
		}
	}
	// A path with no matching grant removes nothing (and does not error).
	if removed, err := store.RevokePath("write_file", filepath.Join(dir, "nope.txt")); err != nil || removed != 0 {
		t.Fatalf("RevokePath(nonexistent) = (%d,%v), want (0,nil)", removed, err)
	}
}
