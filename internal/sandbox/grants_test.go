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

	if _, err := store.Grant(GrantInput{ToolName: "bash", Decision: GrantDeny, MaxAutonomy: AutonomyHigh, Reason: "network blocked"}); err != nil {
		t.Fatalf("Grant deny returned error: %v", err)
	}
	allowed, err := store.Grant(GrantInput{ToolName: "write_file", Decision: GrantAllow, MaxAutonomy: AutonomyMedium, Reason: "workspace edits"})
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

	match, err := reopened.Lookup("write_file", "", AutonomyLow)
	if err != nil {
		t.Fatalf("Lookup returned error: %v", err)
	}
	if !match.Matched || match.Grant.Decision != GrantAllow {
		t.Fatalf("lookup allow = %#v, want matched allow", match)
	}
	match, err = reopened.Lookup("write_file", "", AutonomyHigh)
	if err != nil {
		t.Fatalf("Lookup high returned error: %v", err)
	}
	if match.Matched {
		t.Fatalf("high-autonomy lookup should not match medium grant: %#v", match)
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

func TestGrantStoreRejectsUnsafeInputsAndMalformedFiles(t *testing.T) {
	root := t.TempDir()
	store, err := NewGrantStore(StoreOptions{FilePath: filepath.Join(root, "sandbox-grants.json")})
	if err != nil {
		t.Fatalf("NewGrantStore returned error: %v", err)
	}
	for _, input := range []GrantInput{
		{ToolName: "", Decision: GrantAllow, MaxAutonomy: AutonomyLow},
		{ToolName: "../escape", Decision: GrantAllow, MaxAutonomy: AutonomyLow},
		{ToolName: "write_file", Decision: GrantDecision("maybe"), MaxAutonomy: AutonomyLow},
		{ToolName: "write_file", Decision: GrantAllow, MaxAutonomy: Autonomy("root")},
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
		ToolName:    "write_file",
		Decision:    GrantAllow,
		MaxAutonomy: AutonomyMedium,
		ApprovedAt:  "2026-06-05T14:30:00Z",
		Reason:      "workspace edits",
	}})
	for _, want := range []string{"Sandbox Grants:", "write_file", "allow", "medium", "workspace edits"} {
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
