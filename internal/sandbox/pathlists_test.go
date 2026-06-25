package sandbox

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolvedTempDir returns a symlink-resolved temp dir so paths built under it
// compare equal to the EvalSymlinks-normalized forms the path lists produce
// (macOS /var -> /private/var would otherwise diverge).
func resolvedTempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(tempdir): %v", err)
	}
	return resolved
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", path, err)
	}
	return path
}

func TestReadDenyAllowPrecedence(t *testing.T) {
	ws := resolvedTempDir(t)
	mkdir(t, filepath.Join(ws, "src"))
	secret := mkdir(t, filepath.Join(ws, "secret"))
	public := mkdir(t, filepath.Join(ws, "secret", "public"))

	cases := []struct {
		name       string
		policy     Policy
		path       string
		wantDenied bool
	}{
		{
			name:       "no lists: nothing denied",
			policy:     Policy{},
			path:       filepath.Join(secret, "data"),
			wantDenied: false,
		},
		{
			name:       "denyread blocks subtree",
			policy:     Policy{DenyRead: []string{secret}},
			path:       filepath.Join(secret, "data"),
			wantDenied: true,
		},
		{
			name:       "denyread does not touch siblings",
			policy:     Policy{DenyRead: []string{secret}},
			path:       filepath.Join(ws, "src", "main.go"),
			wantDenied: false,
		},
		{
			name:       "more-specific allowread re-includes",
			policy:     Policy{DenyRead: []string{secret}, AllowRead: []string{public}},
			path:       filepath.Join(public, "ok.txt"),
			wantDenied: false,
		},
		{
			name:       "allowread only re-includes its own subtree",
			policy:     Policy{DenyRead: []string{secret}, AllowRead: []string{public}},
			path:       filepath.Join(secret, "other.txt"),
			wantDenied: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := readDenied(tc.policy, ws, tc.path); got != tc.wantDenied {
				t.Fatalf("readDenied(%q) = %v, want %v", tc.path, got, tc.wantDenied)
			}
		})
	}
}

func TestWritePrecedenceMatrix(t *testing.T) {
	ws := resolvedTempDir(t)
	mkdir(t, filepath.Join(ws, "src"))
	wsSecret := mkdir(t, filepath.Join(ws, "secret"))
	ext := tempDirOutsideDefaultTemp(t)
	mkdir(t, filepath.Join(ext, "build"))
	extProtected := mkdir(t, filepath.Join(ext, "build", "protected"))
	outside := outsideDefaultTempPath(ws, "outside")

	scope, err := NewScope(ws, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	cases := []struct {
		name      string
		policy    Policy
		path      string
		wantAllow bool
	}{
		{
			name:      "workspace path writable by default",
			policy:    Policy{},
			path:      filepath.Join(ws, "src", "main.go"),
			wantAllow: true,
		},
		{
			name:      "outside path denied by default",
			policy:    Policy{},
			path:      filepath.Join(outside, "x"),
			wantAllow: false,
		},
		{
			name:      "allowwrite extends to external root",
			policy:    Policy{AllowWrite: []string{filepath.Join(ext, "build")}},
			path:      filepath.Join(ext, "build", "out.o"),
			wantAllow: true,
		},
		{
			name:      "denywrite wins over workspace",
			policy:    Policy{DenyWrite: []string{wsSecret}},
			path:      filepath.Join(wsSecret, "creds"),
			wantAllow: false,
		},
		{
			name:      "denywrite wins over allowwrite",
			policy:    Policy{AllowWrite: []string{filepath.Join(ext, "build")}, DenyWrite: []string{extProtected}},
			path:      filepath.Join(extProtected, "x"),
			wantAllow: false,
		},
		{
			name:      "allowwrite does not allow unlisted external path",
			policy:    Policy{AllowWrite: []string{filepath.Join(ext, "build")}},
			path:      filepath.Join(outside, "x"),
			wantAllow: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// This matrix exercises the workspace-enforced write precedence.
			block := validateWritePath(scope, tc.policy, true, ws, tc.path)
			gotAllow := block == nil
			if gotAllow != tc.wantAllow {
				t.Fatalf("validateWritePath(%q) allow=%v (block=%v), want allow=%v", tc.path, gotAllow, block, tc.wantAllow)
			}
		})
	}
}

func TestReadExclusionGlobs(t *testing.T) {
	ws := resolvedTempDir(t)
	secret := mkdir(t, filepath.Join(ws, "secret"))
	scope, err := NewScope(ws, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	// Empty DenyRead → no globs (default behavior unchanged).
	if globs := ReadExclusionGlobs(Policy{}, scope); globs != nil {
		t.Fatalf("empty DenyRead must yield no globs, got %v", globs)
	}

	// DenyRead inside the workspace → ripgrep exclusion globs for the rel path.
	got := strings.Join(ReadExclusionGlobs(Policy{DenyRead: []string{secret}}, scope), " ")
	want := "--glob !secret --glob !secret/**"
	if got != want {
		t.Fatalf("ReadExclusionGlobs = %q, want %q", got, want)
	}

	// DenyRead OUTSIDE the workspace → skipped (a rooted search never reaches it).
	outside := resolvedTempDir(t)
	if globs := ReadExclusionGlobs(Policy{DenyRead: []string{outside}}, scope); globs != nil {
		t.Fatalf("out-of-workspace DenyRead must yield no globs, got %v", globs)
	}
}

// TestReadDirExcludedDescendsForNestedAllow verifies a read-denied dir is NOT
// skipped wholesale when it contains a nested AllowRead root, so the walk can
// still reach the re-included subtree.
func TestReadDirExcludedDescendsForNestedAllow(t *testing.T) {
	ws := resolvedTempDir(t)
	secret := mkdir(t, filepath.Join(ws, "secret"))
	public := mkdir(t, filepath.Join(ws, "secret", "public"))

	engine := NewEngine(EngineOptions{
		WorkspaceRoot: ws,
		Policy:        Policy{Mode: ModeEnforce, EnforceWorkspace: true, DenyRead: []string{secret}, AllowRead: []string{public}},
	})
	// The denied dir must be descended (nested allow), but the denied dir's own
	// files are still excluded per-file.
	rx := engine.ReadExclusions()
	if rx.DirExcluded(secret) {
		t.Fatalf("a denied dir with a nested AllowRead must not be skipped wholesale")
	}
	if !rx.PathExcluded(filepath.Join(secret, "creds")) {
		t.Fatalf("a denied file outside the re-included subtree must be excluded")
	}
	if rx.PathExcluded(filepath.Join(public, "ok")) {
		t.Fatalf("a re-included AllowRead file must not be excluded")
	}
}

// TestDirExcludedResolvesSymlinkPrefix verifies DirExcluded resolves a symlink
// in the path prefix before checking for a nested AllowRead root, so a denied dir
// reached through a symlink is NOT skipped wholesale when it contains a
// re-included subtree (the resolved allowRoots would otherwise never match).
func TestDirExcludedResolvesSymlinkPrefix(t *testing.T) {
	ws := resolvedTempDir(t)
	real := mkdir(t, filepath.Join(ws, "real"))
	secret := mkdir(t, filepath.Join(real, "secret"))
	public := mkdir(t, filepath.Join(secret, "public"))
	link := filepath.Join(ws, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	engine := NewEngine(EngineOptions{
		WorkspaceRoot: ws,
		Policy:        Policy{Mode: ModeEnforce, EnforceWorkspace: true, DenyRead: []string{secret}, AllowRead: []string{public}},
	})
	rx := engine.ReadExclusions()
	// secret reached THROUGH the symlink still contains the re-included AllowRead
	// subtree once the prefix is resolved, so it must not be skipped wholesale.
	if rx.DirExcluded(filepath.Join(link, "secret")) {
		t.Fatalf("DirExcluded must resolve the symlink prefix and detect the nested AllowRead (must not skip)")
	}
}

// TestPathListsApplyWithoutWorkspaceEnforcement verifies the explicit path lists
// (DenyWrite/DenyRead) are honored even when EnforceWorkspace is off, while the
// workspace boundary itself is not enforced in that mode.
func TestPathListsApplyWithoutWorkspaceEnforcement(t *testing.T) {
	ws := resolvedTempDir(t)
	secret := mkdir(t, filepath.Join(ws, "secret"))
	outside := resolvedTempDir(t)
	scope, err := NewScope(ws, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	// DenyWrite still blocks even with workspace enforcement off.
	denyPolicy := Policy{DenyWrite: []string{secret}}
	if v := validateWritePath(scope, denyPolicy, false, ws, filepath.Join(secret, "creds")); v == nil {
		t.Fatal("DenyWrite must block even when EnforceWorkspace is off")
	}
	// With enforcement off and no DenyWrite match, an out-of-workspace write is
	// NOT blocked (the workspace boundary is intentionally not applied).
	if v := validateWritePath(scope, denyPolicy, false, ws, filepath.Join(outside, "x")); v != nil {
		t.Fatalf("with EnforceWorkspace off, a non-denied path must be allowed, got %v", v)
	}
	// DenyRead still blocks reads even with workspace enforcement off.
	if v := validatePathWithPolicy(scope, Policy{DenyRead: []string{secret}}, SideEffectRead, false, ws, filepath.Join(secret, "x")); v == nil {
		t.Fatal("DenyRead must block reads even when EnforceWorkspace is off")
	}
}

// TestEvaluatePathListsWithoutWorkspaceRoot verifies the path lists are enforced
// even on an engine built WITHOUT a workspace root: absolute paths are matched
// against DenyWrite, and an unanchorable relative path fails closed.
func TestEvaluatePathListsWithoutWorkspaceRoot(t *testing.T) {
	secret := resolvedTempDir(t)
	other := resolvedTempDir(t)
	engine := NewEngine(EngineOptions{
		Policy: Policy{Mode: ModeEnforce, DenyWrite: []string{secret}},
	})

	denied := engine.Evaluate(context.Background(), Request{
		ToolName: "write_file", SideEffect: SideEffectWrite,
		Args: map[string]any{"path": filepath.Join(secret, "x")},
	})
	if denied.Action != ActionDeny {
		t.Fatalf("absolute DenyWrite path must be denied without a workspace root, got %q", denied.Action)
	}

	ok := engine.Evaluate(context.Background(), Request{
		ToolName: "write_file", SideEffect: SideEffectWrite,
		Args: map[string]any{"path": filepath.Join(other, "x")},
	})
	if ok.Action == ActionDeny {
		t.Fatalf("a non-denied absolute path must not be denied without a workspace root, got deny: %s", ok.ErrorString())
	}

	rel := engine.Evaluate(context.Background(), Request{
		ToolName: "write_file", SideEffect: SideEffectWrite,
		Args: map[string]any{"path": "x"},
	})
	if rel.Action != ActionDeny {
		t.Fatalf("an unanchorable relative path must fail closed when path lists exist, got %q", rel.Action)
	}
}

// TestReadExclusionsInactiveWhenDisabled verifies a disabled policy filters
// nothing — ReadExclusions/ReadExclusionGlobs are inert under ModeDisabled even
// when DenyRead is configured.
func TestReadExclusionsInactiveWhenDisabled(t *testing.T) {
	ws := resolvedTempDir(t)
	secret := mkdir(t, filepath.Join(ws, "secret"))
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: ws,
		Policy:        Policy{Mode: ModeDisabled, DenyRead: []string{secret}},
	})
	if rx := engine.ReadExclusions(); rx.Active() {
		t.Fatal("ReadExclusions must be inactive under a disabled policy")
	}
	if globs := engine.ReadExclusionGlobs(); globs != nil {
		t.Fatalf("ReadExclusionGlobs must be empty under a disabled policy, got %v", globs)
	}
}

// TestEvaluateAppliesReadDeny verifies the lists are wired into Evaluate: a read
// under DenyRead is denied, while a sibling read is not.
func TestEvaluateAppliesReadDeny(t *testing.T) {
	ws := resolvedTempDir(t)
	mkdir(t, filepath.Join(ws, "src"))
	secret := mkdir(t, filepath.Join(ws, "secret"))

	engine := NewEngine(EngineOptions{
		WorkspaceRoot: ws,
		Policy: Policy{
			Mode:             ModeEnforce,
			EnforceWorkspace: true,
			DenyRead:         []string{secret},
		},
	})

	denied := engine.Evaluate(context.Background(), Request{
		ToolName:   "read_file",
		SideEffect: SideEffectRead,
		Args:       map[string]any{"path": filepath.Join(secret, "creds.txt")},
	})
	if denied.Action != ActionDeny {
		t.Fatalf("read under DenyRead = %q, want deny", denied.Action)
	}

	ok := engine.Evaluate(context.Background(), Request{
		ToolName:   "read_file",
		SideEffect: SideEffectRead,
		Args:       map[string]any{"path": filepath.Join(ws, "src", "main.go")},
	})
	if ok.Action == ActionDeny {
		t.Fatalf("read of a non-denied sibling must not be denied, got %q (%s)", ok.Action, ok.ErrorString())
	}
}

// TestEvaluateAppliesWriteAllow verifies an external write is denied by default
// but permitted once the path is on AllowWrite.
func TestEvaluateAppliesWriteAllow(t *testing.T) {
	ws := resolvedTempDir(t)
	ext := tempDirOutsideDefaultTemp(t)
	build := mkdir(t, filepath.Join(ext, "build"))

	base := Policy{Mode: ModeEnforce, EnforceWorkspace: true}
	target := filepath.Join(build, "out.o")

	denyEngine := NewEngine(EngineOptions{WorkspaceRoot: ws, Policy: base})
	denied := denyEngine.Evaluate(context.Background(), Request{
		ToolName:   "write_file",
		SideEffect: SideEffectWrite,
		Args:       map[string]any{"path": target},
	})
	if denied.Action != ActionPrompt {
		t.Fatalf("external write without AllowWrite = %q, want prompt", denied.Action)
	}

	allowPolicy := base
	allowPolicy.AllowWrite = []string{build}
	allowEngine := NewEngine(EngineOptions{WorkspaceRoot: ws, Policy: allowPolicy})
	allowed := allowEngine.Evaluate(context.Background(), Request{
		ToolName:   "write_file",
		SideEffect: SideEffectWrite,
		Args:       map[string]any{"path": target},
	})
	if allowed.Action == ActionDeny {
		t.Fatalf("external write under AllowWrite must not be denied, got deny: %s", allowed.ErrorString())
	}
}

// TestWriteRootsReflectAllowWrite verifies AllowWrite roots flow into the OS
// backend write binds (so a sandboxed shell can write there).
func TestWriteRootsReflectAllowWrite(t *testing.T) {
	ws := resolvedTempDir(t)
	ext := tempDirOutsideDefaultTemp(t)
	build := mkdir(t, filepath.Join(ext, "build"))

	engine := NewEngine(EngineOptions{
		WorkspaceRoot: ws,
		Policy:        Policy{Mode: ModeEnforce, EnforceWorkspace: true, AllowWrite: []string{build}},
	})
	roots := engine.writeRoots(ws)
	found := false
	for _, r := range roots {
		if r == build {
			found = true
		}
	}
	if !found {
		t.Fatalf("writeRoots must include AllowWrite root %q, got %v", build, roots)
	}
}

// TestSandboxExecProfileEmitsDenyWriteRule verifies a DenyWrite directory becomes
// an explicit seatbelt deny clause AFTER the write-allow (last-match-wins).
func TestSandboxExecProfileEmitsDenyWriteRule(t *testing.T) {
	ws := resolvedTempDir(t)
	secret := mkdir(t, filepath.Join(ws, "secret"))

	policy := Policy{Mode: ModeEnforce, EnforceWorkspace: true, DenyWrite: []string{secret}}
	profile := sandboxExecProfile([]string{ws}, policy, "")

	// The path is escaped the same way the profile escapes it (e.g. Windows
	// backslashes are doubled), so the assertion holds on every platform.
	wantDeny := `(deny file-write* (subpath "` + sandboxProfileString(secret) + `"))`
	if !strings.Contains(profile, wantDeny) {
		t.Fatalf("profile missing DenyWrite rule %q:\n%s", wantDeny, profile)
	}
	// The deny clause must appear AFTER the write-allow so seatbelt's
	// last-match-wins lets it override the bind.
	allowIdx := strings.Index(profile, "(allow file-write*")
	denyIdx := strings.Index(profile, wantDeny)
	if allowIdx < 0 || denyIdx < 0 || denyIdx < allowIdx {
		t.Fatalf("DenyWrite rule must follow the write-allow (allow@%d deny@%d):\n%s", allowIdx, denyIdx, profile)
	}
}
