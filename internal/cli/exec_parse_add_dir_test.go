package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestParseExecArgsCollectsAddDirs(t *testing.T) {
	options, _, err := parseExecArgs([]string{
		"--prompt", "hi",
		"--add-dir", "/one",
		"--add-dir=/two",
	})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if len(options.addDirs) != 2 || options.addDirs[0] != "/one" || options.addDirs[1] != "/two" {
		t.Fatalf("addDirs=%v want [/one /two]", options.addDirs)
	}
}

func TestParseExecArgsAddDirRequiresValue(t *testing.T) {
	if _, _, err := parseExecArgs([]string{"--add-dir"}); err == nil {
		t.Fatal("bare --add-dir must error")
	}
}

func TestSplitLeadingAddDirFlags(t *testing.T) {
	addDirs, rest, err := splitLeadingAddDirFlags([]string{"--add-dir", "/one", "--add-dir=/two", "exec", "--prompt", "x"})
	if err != nil {
		t.Fatalf("splitLeadingAddDirFlags: %v", err)
	}
	if len(addDirs) != 2 || addDirs[0] != "/one" || addDirs[1] != "/two" {
		t.Fatalf("addDirs=%v want [/one /two]", addDirs)
	}
	if len(rest) != 3 || rest[0] != "exec" {
		t.Fatalf("rest=%v want [exec --prompt x]", rest)
	}
	if _, _, err := splitLeadingAddDirFlags([]string{"--add-dir"}); err == nil {
		t.Fatal("trailing bare --add-dir must error")
	}
	// Value that looks like a flag must be rejected.
	if _, _, err := splitLeadingAddDirFlags([]string{"--add-dir", "--foo"}); err == nil {
		t.Fatal("--add-dir with flag-like value must error")
	}
}

// TestExecScopeReRegistrationSwapsCoreToolsByName pins the mechanism runExec
// relies on: a nil-scope core registry is built first (so --list-tools and
// tool-filter validation work before config resolve), then once the run scope
// is known the scoped core tools are re-registered and Registry.Register
// replaces the earlier instances BY NAME. The before/after write_file probes
// prove both the overwrite and the scoped enforcement it ships.
func TestExecScopeReRegistrationSwapsCoreToolsByName(t *testing.T) {
	root := t.TempDir()
	extra := t.TempDir()
	inside := filepath.Join(extra, "inside.txt")

	registry := newCoreRegistry(root)

	// Before re-registration the tools carry a nil scope, so an absolute path
	// inside the extra root (but outside the workspace) must be denied.
	denied := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path":    inside,
		"content": "too early",
	}, tools.RunOptions{PermissionGranted: true})
	if denied.Status == tools.StatusOK {
		t.Fatalf("nil-scope registry must deny extra-root write, got ok: %s", denied.Output)
	}
	if _, err := os.Stat(inside); !os.IsNotExist(err) {
		t.Fatalf("expected inside file to remain absent before re-registration, stat err=%v", err)
	}

	// Re-register exactly like exec.go does once the run scope is resolved.
	scope, err := sandbox.NewScope(root, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	for _, tool := range tools.CoreToolsScoped(root, scope) {
		registry.Register(tool)
	}

	allowed := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path":    inside,
		"content": "granted",
	}, tools.RunOptions{PermissionGranted: true})
	if allowed.Status != tools.StatusOK {
		t.Fatalf("scoped registry must allow extra-root write, got %s: %s", allowed.Status, allowed.Output)
	}
	content, err := os.ReadFile(inside)
	if err != nil {
		t.Fatalf("read %s: %v", inside, err)
	}
	if string(content) != "granted" {
		t.Fatalf("inside content = %q, want %q", string(content), "granted")
	}

	// A path outside every scope root must still be denied after re-registration.
	outside := filepath.Join(t.TempDir(), "outside.txt")
	deniedOutside := registry.RunWithOptions(context.Background(), "write_file", map[string]any{
		"path":    outside,
		"content": "never",
	}, tools.RunOptions{PermissionGranted: true})
	if deniedOutside.Status == tools.StatusOK {
		t.Fatalf("write outside all roots must fail, got ok: %s", deniedOutside.Output)
	}
	if !strings.Contains(deniedOutside.Output, "must stay inside the workspace") {
		t.Fatalf("expected outside-workspace denial, got %q", deniedOutside.Output)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("expected outside file to remain absent, stat err=%v", err)
	}
}
