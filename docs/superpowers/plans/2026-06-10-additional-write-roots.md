# Additional Write Roots Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users grant zero write access to directories outside the workspace via a repeatable `--add-dir` flag, a `sandbox.additionalWriteRoots` config key, and a session-only `/add-dir` TUI command.

**Architecture:** A new concurrency-safe `sandbox.Scope` (workspace root + extra write roots) is the single source of truth, shared by all four enforcement layers: the policy engine's path validation, the macOS seatbelt profile, the Linux bubblewrap binds, and the Go file tools' path resolution. A mid-session `Add` is immediately visible everywhere because every layer reads from the same pointer.

**Tech Stack:** Go stdlib only (`sync`, `path/filepath`, `os`). No new dependencies. Spec: `docs/superpowers/specs/2026-06-10-additional-write-roots-design.md`.

**Conventions:** Run all commands from the repo root. Tests use stdlib `testing` only, matching the existing suite. Every commit must leave `go build ./...` green.

---

## Task 1: `sandbox.Scope` core type

**Files:**
- Create: `internal/sandbox/scope.go`
- Create: `internal/sandbox/scope_test.go`

The Scope owns root normalization and multi-root validation. `validateWorkspacePath` (already in `internal/sandbox/paths.go`) stays as the per-root primitive.

- [ ] **Step 1: Write the failing tests**

Create `internal/sandbox/scope_test.go`:

```go
package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewScopeNormalizesAndValidatesExtraRoots(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()

	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	roots := scope.Roots()
	if len(roots) != 2 {
		t.Fatalf("Roots()=%v want workspace + 1 extra", roots)
	}
	if roots[0] != scope.WorkspaceRoot() {
		t.Fatalf("Roots()[0]=%q want workspace root %q", roots[0], scope.WorkspaceRoot())
	}
}

func TestNewScopeRejectsBadExtraRoots(t *testing.T) {
	workspace := t.TempDir()
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	for name, root := range map[string]string{
		"missing directory": missing,
		"regular file":      file,
		"filesystem root":   string(filepath.Separator),
		"empty":             "   ",
	} {
		if _, err := NewScope(workspace, []string{root}); err == nil {
			t.Fatalf("NewScope(%s root %q) = nil error, want rejection", name, root)
		}
	}
}

func TestScopeAddIsIdempotentAndRejectsContainedPaths(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	added, err := scope.Add(extra)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := scope.Add(extra); err != nil {
		t.Fatalf("Add (repeat): %v", err)
	}
	nested := filepath.Join(extra, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if _, err := scope.Add(nested); err != nil {
		t.Fatalf("Add (nested in existing root): %v", err)
	}
	if _, err := scope.Add(workspace); err != nil {
		t.Fatalf("Add (workspace itself): %v", err)
	}
	if got := scope.Roots(); len(got) != 2 {
		t.Fatalf("Roots()=%v want exactly workspace + %q (idempotent adds)", got, added)
	}
}

func TestScopeValidateAllowsAnyRootButRelativeOnlyWorkspace(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}

	if violation := scope.validate(filepath.Join(extra, "out.txt")); violation != nil {
		t.Fatalf("validate(extra-root path) = %v, want nil", violation)
	}
	if violation := scope.validate(filepath.Join(workspace, "in.txt")); violation != nil {
		t.Fatalf("validate(workspace path) = %v, want nil", violation)
	}
	if violation := scope.validate("nested/in.txt"); violation != nil {
		t.Fatalf("validate(relative path) = %v, want nil (resolves against workspace)", violation)
	}

	outside := filepath.Join(t.TempDir(), "elsewhere.txt")
	violation := scope.validate(outside)
	if violation == nil {
		t.Fatal("validate(outside all roots) = nil, want violation")
	}
	if violation.Code != ViolationOutsideWorkspace {
		t.Fatalf("violation.Code=%q want %q", violation.Code, ViolationOutsideWorkspace)
	}
	if !strings.Contains(violation.Reason, "--add-dir") {
		t.Fatalf("violation.Reason=%q want actionable --add-dir hint", violation.Reason)
	}
}

func TestScopeValidateKeepsSymlinkTraversalProtection(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(workspace, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	scope, err := NewScope(workspace, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	violation := scope.validate(filepath.Join(link, "escape.txt"))
	if violation == nil {
		t.Fatal("validate(symlink escape) = nil, want violation")
	}
	if violation.Code != ViolationSymlinkTraversal {
		t.Fatalf("violation.Code=%q want %q", violation.Code, ViolationSymlinkTraversal)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run 'TestNewScope|TestScope' -v`
Expected: FAIL — `undefined: NewScope` (compile error).

- [ ] **Step 3: Implement `internal/sandbox/scope.go`**

```go
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Scope is the shared set of directories the sandbox allows writes in: the
// workspace root plus zero or more user-granted extra roots. One instance is
// created per run and shared by the policy engine, the OS command runners, and
// the file tools, so a mid-session Add is immediately visible to every layer.
type Scope struct {
	mu            sync.RWMutex
	workspaceRoot string
	extraRoots    []string
}

// NewScope builds a scope for workspaceRoot plus the given extra roots. The
// workspace root is normalized best-effort (it may not exist in tests); every
// extra root must normalize strictly via Add and an invalid one fails the
// whole construction so a bad --add-dir/config entry surfaces at startup.
func NewScope(workspaceRoot string, extras []string) (*Scope, error) {
	scope := &Scope{workspaceRoot: normalizeWorkspaceRootBestEffort(workspaceRoot)}
	for _, extra := range extras {
		if _, err := scope.Add(extra); err != nil {
			return nil, fmt.Errorf("write root %q: %w", extra, err)
		}
	}
	return scope, nil
}

func (s *Scope) WorkspaceRoot() string {
	return s.workspaceRoot
}

// Roots returns the workspace root first, then the extra roots, as a copy.
func (s *Scope) Roots() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	roots := make([]string, 0, 1+len(s.extraRoots))
	roots = append(roots, s.workspaceRoot)
	roots = append(roots, s.extraRoots...)
	return roots
}

// Add grants write access under path. The path must be an existing directory;
// it is home-expanded, made absolute, and symlink-resolved before being
// trusted, and the filesystem root is rejected outright. Adding a path already
// covered by an existing root is an idempotent success.
func (s *Scope) Add(path string) (string, error) {
	root, err := normalizeScopeRoot(path)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range append([]string{s.workspaceRoot}, s.extraRoots...) {
		if pathWithinRoot(existing, root) {
			return root, nil
		}
	}
	s.extraRoots = append(s.extraRoots, root)
	return root, nil
}

// validate reports whether requestedPath is allowed by any scope root.
// Relative paths resolve against the workspace root only; absolute paths are
// accepted if they validate (including per-segment symlink checks) under ANY
// root. The returned violation is the workspace root's, with an actionable
// hint appended for plain outside-workspace denials.
func (s *Scope) validate(requestedPath string) *pathViolation {
	roots := s.Roots()
	if !filepath.IsAbs(requestedPath) {
		return validateWorkspacePath(roots[0], requestedPath)
	}
	var first *pathViolation
	for _, root := range roots {
		violation := validateWorkspacePath(root, requestedPath)
		if violation == nil {
			return nil
		}
		if first == nil {
			first = violation
		}
	}
	if first != nil && first.Code == ViolationOutsideWorkspace {
		first.Reason += " (use /add-dir or --add-dir to allow writes there)"
	}
	return first
}

func normalizeWorkspaceRootBestEffort(workspaceRoot string) string {
	trimmed := strings.TrimSpace(workspaceRoot)
	if trimmed == "" {
		return ""
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return resolved
	}
	return filepath.Clean(absolute)
}

func normalizeScopeRoot(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", errors.New("write root path is empty")
	}
	if trimmed == "~" || strings.HasPrefix(trimmed, "~/") || strings.HasPrefix(trimmed, "~"+string(filepath.Separator)) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("expand ~: %w", err)
		}
		trimmed = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(trimmed[1:], "/"), string(filepath.Separator)))
	}
	absolute, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("write root must exist: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("write root %s is not a directory", resolved)
	}
	if filepath.Dir(resolved) == resolved {
		return "", fmt.Errorf("refusing filesystem root %s as a write root", resolved)
	}
	return resolved, nil
}

func pathWithinRoot(root string, candidate string) bool {
	if root == "" {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/sandbox/ -run 'TestNewScope|TestScope' -v`
Expected: PASS (symlink test may SKIP on platforms without symlink support).

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/scope.go internal/sandbox/scope_test.go
git commit -m "sandbox: add Scope type for multi-root write access"
```

---

## Task 2: Engine integration (policy layer + risk)

**Files:**
- Modify: `internal/sandbox/engine.go` (EngineOptions, Engine struct, Evaluate at line 88)
- Modify: `internal/sandbox/risk.go` (Classify path loop at lines 107-124)
- Test: `internal/sandbox/engine_test.go` (append)

- [ ] **Step 1: Write the failing test**

Append to `internal/sandbox/engine_test.go`:

```go
func TestEvaluateAllowsWritesInsideExtraScopeRoot(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        DefaultPolicy(),
		Scope:         scope,
	})

	inside := engine.Evaluate(context.Background(), Request{
		ToolName:   "write_file",
		SideEffect: SideEffectWrite,
		Permission: PermissionAllow,
		Args:       map[string]any{"path": filepath.Join(extra, "report.txt")},
	})
	if inside.Action != ActionAllow {
		t.Fatalf("extra-root write Action=%q (%s), want allow", inside.Action, inside.Reason)
	}
	if HasRiskCategory(inside.Risk, "out_of_workspace") {
		t.Fatalf("extra-root write risk=%v, must not be out_of_workspace", inside.Risk)
	}

	outside := engine.Evaluate(context.Background(), Request{
		ToolName:   "write_file",
		SideEffect: SideEffectWrite,
		Permission: PermissionAllow,
		Args:       map[string]any{"path": filepath.Join(t.TempDir(), "escape.txt")},
	})
	if outside.Action != ActionDeny || outside.Violation == nil {
		t.Fatalf("outside write Action=%q, want deny with violation", outside.Action)
	}
	if !strings.Contains(outside.Violation.Reason, "--add-dir") {
		t.Fatalf("outside violation reason=%q, want --add-dir hint", outside.Violation.Reason)
	}
}
```

Add `"path/filepath"` and `"strings"` to the test file imports if not present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/ -run TestEvaluateAllowsWritesInsideExtraScopeRoot -v`
Expected: FAIL — `unknown field Scope in struct literal` (compile error).

- [ ] **Step 3: Implement engine + risk changes**

In `internal/sandbox/engine.go`:

(a) Add `Scope *Scope` to `EngineOptions` and `scope *Scope` to `Engine`. In `NewEngine`, after the existing fields, default the scope:

```go
func NewEngine(options EngineOptions) *Engine {
	policy := options.Policy
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	scope := options.Scope
	workspaceRoot := strings.TrimSpace(options.WorkspaceRoot)
	if scope == nil && workspaceRoot != "" {
		scope = &Scope{workspaceRoot: normalizeWorkspaceRootBestEffort(workspaceRoot)}
	}
	return &Engine{
		workspaceRoot: workspaceRoot,
		policy:        policy,
		store:         options.Store,
		backend:       options.Backend,
		scope:         scope,
	}
}

// Scope returns the engine's shared write scope (nil when the engine was
// built without a workspace root). The TUI uses it for /add-dir.
func (engine *Engine) Scope() *Scope {
	if engine == nil {
		return nil
	}
	return engine.scope
}

// scopeFor returns the scope to validate request paths against. The engine's
// shared scope applies only when the request targets the engine's own
// workspace root; a per-request override root gets an ad-hoc single-root scope
// so subagent-style overrides keep today's exact semantics.
func (engine *Engine) scopeFor(requestRoot string) *Scope {
	if engine.scope != nil && requestRoot == engine.workspaceRoot {
		return engine.scope
	}
	return &Scope{workspaceRoot: requestRoot}
}
```

(b) In `Evaluate`, replace the workspace gate (currently `if policy.EnforceWorkspace && request.WorkspaceRoot != "" { if violation := validateWorkspacePaths(...)`) with:

```go
	if policy.EnforceWorkspace && request.WorkspaceRoot != "" {
		scope := engine.scopeFor(request.WorkspaceRoot)
		for _, requested := range requestPaths(request) {
			if requested == "" {
				continue
			}
			if violation := scope.validate(requested); violation != nil {
				return deny(request, risk, violation.Code, violation.Path, violation.Reason, false)
			}
		}
	}
```

(c) Still in `Evaluate`, change `risk := Classify(request)` to `risk := classifyWithScope(request, engine.scopeFor(request.WorkspaceRoot))` — but only after the `request.WorkspaceRoot = firstNonEmpty(...)` line, where it already sits. Guard for the nil-engine path at the top of Evaluate: that early return already calls `Classify(request)` directly and stays unchanged.

In `internal/sandbox/risk.go`:

(d) Add a scope-aware variant and keep `Classify` as the public single-root wrapper:

```go
func Classify(request Request) Risk {
	return classifyWithScope(request, nil)
}

func classifyWithScope(request Request, scope *Scope) Risk {
```

(e) Inside the per-path loop (lines 107-124), replace the `validateWorkspacePath` call with the scope when present:

```go
		if request.WorkspaceRoot != "" {
			var violation *pathViolation
			if scope != nil {
				violation = scope.validate(path)
			} else {
				violation = validateWorkspacePath(request.WorkspaceRoot, path)
			}
			if violation != nil {
				switch violation.Code {
				case ViolationSymlinkTraversal:
					add("symlink_traversal", RiskCritical)
				default:
					add("out_of_workspace", RiskCritical)
				}
			}
		}
```

- [ ] **Step 4: Run the sandbox suite**

Run: `go test ./internal/sandbox/ -v 2>&1 | tail -20`
Expected: PASS, including all pre-existing engine/risk tests (the nil-scope and ad-hoc paths must behave exactly as before).

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/engine.go internal/sandbox/risk.go internal/sandbox/engine_test.go
git commit -m "sandbox: make engine path validation and risk scope-aware"
```

---

## Task 3: OS runners (seatbelt profile, bubblewrap binds, command cwd)

**Files:**
- Modify: `internal/sandbox/runner.go` (`BuildCommandPlan`, `resolveCommandDir`, `bubblewrapCommandPlan`, `sandboxExecCommandPlan`, `sandboxExecProfile`)
- Test: `internal/sandbox/runner_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `internal/sandbox/runner_test.go`:

```go
func TestSandboxExecProfileIncludesExtraWriteRoots(t *testing.T) {
	profile := sandboxExecProfile([]string{"/ws", "/extra root"}, Policy{Mode: ModeEnforce, EnforceWorkspace: true})
	if !strings.Contains(profile, `(allow file-write* (subpath "/ws") (subpath "/extra root"))`) {
		t.Fatalf("profile missing multi-root write rule:\n%s", profile)
	}
}

func TestBubblewrapPlanBindsExtraWriteRoots(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	engine := NewEngine(EngineOptions{
		WorkspaceRoot: workspace,
		Policy:        DefaultPolicy(),
		Scope:         scope,
		Backend:       Backend{Name: BackendBubblewrap, Available: true, Executable: "/usr/bin/bwrap"},
	})
	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "true"})
	if err != nil {
		t.Fatalf("BuildCommandPlan: %v", err)
	}
	joined := strings.Join(plan.Args, " ")
	resolvedExtra := scope.Roots()[1]
	if !strings.Contains(joined, "--bind "+resolvedExtra+" "+resolvedExtra) {
		t.Fatalf("bubblewrap args missing rw bind for extra root %q:\n%s", resolvedExtra, joined)
	}
}

func TestResolveCommandDirAllowsExtraRootCwd(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	engine := NewEngine(EngineOptions{WorkspaceRoot: workspace, Policy: DefaultPolicy(), Scope: scope})
	if _, _, _, err := engine.resolveCommandDir(extra, engine.policy); err != nil {
		t.Fatalf("resolveCommandDir(extra root) = %v, want nil", err)
	}
	if _, _, _, err := engine.resolveCommandDir(t.TempDir(), engine.policy); err == nil {
		t.Fatal("resolveCommandDir(outside all roots) = nil error, want violation")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/sandbox/ -run 'TestSandboxExecProfileIncludesExtra|TestBubblewrapPlanBindsExtra|TestResolveCommandDirAllowsExtraRoot' -v`
Expected: FAIL — `sandboxExecProfile` signature mismatch (compile error).

- [ ] **Step 3: Implement runner changes**

In `internal/sandbox/runner.go`:

(a) Add a helper on Engine:

```go
// writeRoots returns the full ordered write-root list for command plans:
// the workspace root plus any granted extra roots.
func (engine *Engine) writeRoots(workspaceRoot string) []string {
	if engine.scope != nil {
		return engine.scope.Roots()
	}
	return []string{workspaceRoot}
}
```

(b) In `BuildCommandPlan`, thread the roots into both backends. Replace the two backend cases:

```go
	switch backend.Name {
	case BackendBubblewrap:
		if backend.Available && backend.Executable != "" {
			return bubblewrapCommandPlan(spec, workspaceRoot, relativeDir, engine.writeRoots(workspaceRoot), policy, backend), nil
		}
	case BackendSandboxExec:
		if backend.Available && backend.Executable != "" {
			return sandboxExecCommandPlan(spec, workspaceRoot, engine.writeRoots(workspaceRoot), policy, backend), nil
		}
	}
```

(c) In `resolveCommandDir`, replace the `validateWorkspacePath(workspaceRoot, commandDir)` call (line 133) with the engine scope, keeping the Violation construction identical:

```go
	if policy.EnforceWorkspace {
		if violation := engine.scopeFor(engine.workspaceRoot).validate(commandDir); violation != nil {
```

Note `resolveCommandDir` is already an Engine method, so `engine` is in scope. The `relativeDir` computation below it stays, but a cwd inside an extra root yields a `..`-prefixed relativeDir — handled in (d).

(d) `bubblewrapCommandPlan` gains a `writeRoots []string` parameter (after `relativeDir`). After the workspace `--bind`, add extra binds, and fall back to the real path for an extra-root cwd:

```go
func bubblewrapCommandPlan(spec CommandSpec, workspaceRoot string, relativeDir string, writeRoots []string, policy Policy, backend Backend) CommandPlan {
	sandboxDir := bubblewrapWorkspace
	if relativeDir != "" {
		sandboxDir = filepath.ToSlash(filepath.Join(bubblewrapWorkspace, relativeDir))
	}
	// A cwd inside an extra write root is outside the /workspace remount; the
	// extra root is bound at its real host path, so chdir there directly.
	if relativeDir == ".." || strings.HasPrefix(relativeDir, ".."+string(filepath.Separator)) {
		sandboxDir = filepath.ToSlash(spec.Dir)
	}
	args := []string{
		"--die-with-parent",
		"--unshare-pid",
		"--unshare-ipc",
		"--unshare-uts",
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--bind", workspaceRoot, bubblewrapWorkspace,
	}
	for _, root := range writeRoots {
		if root == workspaceRoot {
			continue
		}
		args = append(args, "--bind", root, root)
	}
	args = append(args, "--chdir", sandboxDir)
```

The rest of the function (network, ro-binds, env, trailing `--`) is unchanged — keep the existing lines after `--chdir` exactly as they are today (the `--chdir` pair moves from the initial literal into this appended position).

(e) `sandboxExecCommandPlan` gains `writeRoots []string` (after `workspaceRoot`) and passes it to the profile:

```go
func sandboxExecCommandPlan(spec CommandSpec, workspaceRoot string, writeRoots []string, policy Policy, backend Backend) CommandPlan {
	args := []string{"-p", sandboxExecProfile(writeRoots, policy), spec.Name}
```

(f) `sandboxExecProfile` takes the roots list:

```go
func sandboxExecProfile(writeRoots []string, policy Policy) string {
	networkRule := "(deny network*)"
	if policy.Network == NetworkAllow {
		networkRule = "(allow network*)"
	}
	writeRule := "(allow file-write*)"
	if policy.EnforceWorkspace {
		subpaths := make([]string, 0, len(writeRoots))
		for _, root := range writeRoots {
			subpaths = append(subpaths, `(subpath "`+sandboxProfileString(root)+`")`)
		}
		writeRule = "(allow file-write* " + strings.Join(subpaths, " ") + ")"
	}
```

The trailing `strings.Join([...])` block is unchanged.

(g) Fix any existing callers of the old signatures surfaced by the compiler (tests in `runner_test.go` that call `sandboxExecProfile(workspaceRoot, policy)` become `sandboxExecProfile([]string{workspaceRoot}, policy)`).

- [ ] **Step 4: Run the sandbox suite**

Run: `go test ./internal/sandbox/ 2>&1 | tail -5`
Expected: `ok github.com/Gitlawb/zero/internal/sandbox`.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/runner.go internal/sandbox/runner_test.go
git commit -m "sandbox: widen seatbelt/bubblewrap profiles and command cwd to scope roots"
```

---

## Task 4: File tools honor the scope

**Files:**
- Modify: `internal/tools/workspace.go` (scoped resolver variants)
- Modify: `internal/tools/registry.go` (scoped Core*Tools)
- Modify: `internal/tools/read_file.go`, `list_directory.go`, `glob.go`, `grep.go`, `write_file.go`, `edit_file.go`, `apply_patch.go`, `bash.go` (scope field + scoped constructor + resolver call sites)
- Test: `internal/tools/file_tools_test.go` (append)

- [ ] **Step 1: Write the failing tests**

Append to `internal/tools/file_tools_test.go`:

```go
func TestScopedToolsAllowExtraRootWrites(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := sandbox.NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	target := filepath.Join(extra, "saved.txt")

	res := NewScopedWriteFileTool(workspace, scope).Run(context.Background(), map[string]any{
		"path":    target,
		"content": "hello",
	})
	if res.Status != StatusOK {
		t.Fatalf("scoped write_file status=%s output=%s", res.Status, res.Output)
	}
	read := NewScopedReadFileTool(workspace, scope).Run(context.Background(), map[string]any{"path": target})
	if read.Status != StatusOK || !strings.Contains(read.Output, "hello") {
		t.Fatalf("scoped read_file status=%s output=%s", read.Status, read.Output)
	}
}

func TestScopedToolsKeepRelativePathsInWorkspace(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := sandbox.NewScope(workspace, []string{extra})
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	res := NewScopedWriteFileTool(workspace, scope).Run(context.Background(), map[string]any{
		"path":    "rel.txt",
		"content": "workspace",
	})
	if res.Status != StatusOK {
		t.Fatalf("relative write status=%s output=%s", res.Status, res.Output)
	}
	if _, err := os.Stat(filepath.Join(workspace, "rel.txt")); err != nil {
		t.Fatalf("relative path must land in workspace: %v", err)
	}
}

func TestUnscopedToolsStillRejectOutsideWrites(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.txt")
	res := NewWriteFileTool(workspace).Run(context.Background(), map[string]any{
		"path":    outside,
		"content": "nope",
	})
	if res.Status == StatusOK {
		t.Fatalf("unscoped write outside workspace must fail, got OK: %s", res.Output)
	}
}
```

Add `"github.com/Gitlawb/zero/internal/sandbox"` to the test imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tools/ -run 'TestScopedTools|TestUnscopedToolsStillReject' -v`
Expected: FAIL — `undefined: NewScopedWriteFileTool` (compile error).

- [ ] **Step 3: Add the scoped resolvers to `internal/tools/workspace.go`**

Append:

```go
// PathScope is the multi-root write scope shared with the sandbox engine.
// *sandbox.Scope satisfies it; nil means workspace-only (today's behavior).
type PathScope interface {
	Roots() []string
}

// scopedRoots returns the ordered roots to try for an absolute path:
// the scope's roots when present, else just the workspace root.
func scopedRoots(workspaceRoot string, scope PathScope) []string {
	if scope == nil {
		return []string{workspaceRoot}
	}
	return scope.Roots()
}

// resolveScopedPath is resolveWorkspacePath generalized to a scope: relative
// paths resolve against the workspace root only; an absolute path resolves
// against the first root that contains it. The workspace root's error is
// returned when no root matches so messages stay stable.
func resolveScopedPath(workspaceRoot string, scope PathScope, requestedPath string) (string, string, error) {
	if requestedPath == "" || !filepath.IsAbs(requestedPath) {
		return resolveWorkspacePath(workspaceRoot, requestedPath)
	}
	var firstErr error
	for _, root := range scopedRoots(workspaceRoot, scope) {
		absolute, relative, err := resolveWorkspacePath(root, requestedPath)
		if err == nil {
			return absolute, relative, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", "", firstErr
}

// resolveScopedTargetPath mirrors resolveWorkspaceTargetPath for write targets
// (the target may not exist yet) across all scope roots.
func resolveScopedTargetPath(workspaceRoot string, scope PathScope, requestedPath string) (string, string, error) {
	if requestedPath == "" || !filepath.IsAbs(requestedPath) {
		return resolveWorkspaceTargetPath(workspaceRoot, requestedPath)
	}
	var firstErr error
	for _, root := range scopedRoots(workspaceRoot, scope) {
		absolute, relative, err := resolveWorkspaceTargetPath(root, requestedPath)
		if err == nil {
			return absolute, relative, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", "", firstErr
}

// recheckScopedWriteTarget mirrors recheckWorkspaceWriteTarget across roots.
func recheckScopedWriteTarget(workspaceRoot string, scope PathScope, requestedPath string) error {
	if requestedPath == "" || !filepath.IsAbs(requestedPath) {
		return recheckWorkspaceWriteTarget(workspaceRoot, requestedPath)
	}
	var firstErr error
	for _, root := range scopedRoots(workspaceRoot, scope) {
		err := recheckWorkspaceWriteTarget(root, requestedPath)
		if err == nil {
			return nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
```

- [ ] **Step 4: Thread the scope through the eight tools**

For each tool the change is mechanical and identical in shape. Shown in full for `write_file.go`; repeat for the others with their own type/constructor names.

(a) `internal/tools/write_file.go` — add the field and scoped constructor (adapt to the actual struct/constructor names in the file; the struct currently holds `workspaceRoot string`):

```go
type WriteFileTool struct {
	workspaceRoot string
	scope         PathScope
}

func NewWriteFileTool(workspaceRoot string) WriteFileTool {
	return NewScopedWriteFileTool(workspaceRoot, nil)
}

func NewScopedWriteFileTool(workspaceRoot string, scope PathScope) WriteFileTool {
	return WriteFileTool{workspaceRoot: normalizeWorkspaceRoot(workspaceRoot), scope: scope}
}
```

If the existing constructor does extra setup, keep it and move the body into the scoped variant. Then replace every `resolveWorkspacePath(tool.workspaceRoot, X)` with `resolveScopedPath(tool.workspaceRoot, tool.scope, X)`, every `resolveWorkspaceTargetPath(tool.workspaceRoot, X)` with `resolveScopedTargetPath(tool.workspaceRoot, tool.scope, X)`, and every `recheckWorkspaceWriteTarget(tool.workspaceRoot, X)` with `recheckScopedWriteTarget(tool.workspaceRoot, tool.scope, X)` inside the tool's methods.

(b) Repeat (a) for:
- `read_file.go` → `NewScopedReadFileTool` (resolver at read_file.go:56)
- `list_directory.go` → `NewScopedListDirectoryTool` (list_directory.go:59)
- `glob.go` → `NewScopedGlobTool` (glob.go:63)
- `grep.go` → `NewScopedGrepTool` (grep.go:96)
- `edit_file.go` → `NewScopedEditFileTool` (edit_file.go:55 and the recheck at :82)
- `apply_patch.go` → `NewScopedApplyPatchTool` (apply_patch.go:47 and the recheck at :146 — the recheck takes a local `root`; pass `tool.scope` alongside)
- `bash.go` → `NewScopedBashTool` (cwd resolution at bash.go:80)

Where a method passes `tool.workspaceRoot` into package helpers like `mutation_targets.go`, leave those untouched — checkpoint/rewind stays workspace-only by design (extra roots are not checkpointed).

(c) `internal/tools/registry.go` — add scoped variants, with the old ones delegating:

```go
func CoreReadOnlyTools(workspaceRoot string) []Tool {
	return CoreReadOnlyToolsScoped(workspaceRoot, nil)
}

func CoreReadOnlyToolsScoped(workspaceRoot string, scope PathScope) []Tool {
	return []Tool{
		NewScopedReadFileTool(workspaceRoot, scope),
		NewScopedListDirectoryTool(workspaceRoot, scope),
		NewScopedGlobTool(workspaceRoot, scope),
		NewScopedGrepTool(workspaceRoot, scope),
		NewSkillTool(""),
		NewAskUserTool(),
	}
}

func CoreWriteTools(workspaceRoot string) []Tool {
	return CoreWriteToolsScoped(workspaceRoot, nil)
}

func CoreWriteToolsScoped(workspaceRoot string, scope PathScope) []Tool {
	return []Tool{
		NewScopedWriteFileTool(workspaceRoot, scope),
		NewScopedEditFileTool(workspaceRoot, scope),
		NewScopedApplyPatchTool(workspaceRoot, scope),
		NewUpdatePlanTool(),
	}
}

func CoreShellTools(workspaceRoot string) []Tool {
	return CoreShellToolsScoped(workspaceRoot, nil)
}

func CoreShellToolsScoped(workspaceRoot string, scope PathScope) []Tool {
	return []Tool{
		NewScopedBashTool(workspaceRoot, scope),
	}
}

func CoreTools(workspaceRoot string) []Tool {
	return CoreToolsScoped(workspaceRoot, nil)
}

func CoreToolsScoped(workspaceRoot string, scope PathScope) []Tool {
	tools := append([]Tool{}, CoreReadOnlyToolsScoped(workspaceRoot, scope)...)
	tools = append(tools, CoreWriteToolsScoped(workspaceRoot, scope)...)
	tools = append(tools, CoreShellToolsScoped(workspaceRoot, scope)...)
	tools = append(tools, CoreNetworkTools()...)
	return tools
}
```

Keep the existing comments on the skill tool. Preserve the original `NewSkillTool`/`NewAskUserTool`/`NewUpdatePlanTool`/`CoreNetworkTools` lines verbatim.

- [ ] **Step 5: Run the tools suite**

Run: `go test ./internal/tools/ 2>&1 | tail -5`
Expected: `ok github.com/Gitlawb/zero/internal/tools` — both the new tests and all pre-existing confinement tests (nil scope must be byte-identical behavior).

- [ ] **Step 6: Commit**

```bash
git add internal/tools/
git commit -m "tools: resolve paths against the shared write scope"
```

---

## Task 5: Config key

**Files:**
- Modify: `internal/config/types.go:57` (SandboxConfig)
- Modify: `internal/config/resolver.go` (merge, near lines 150-155)
- Test: `internal/config/resolver_test.go` or the existing SandboxConfig test file (locate with `grep -rn "Sandbox.Network" internal/config/*_test.go`)

- [ ] **Step 1: Write the failing test**

Append next to the existing Sandbox.Network merge test (same file, same style — note `mergeConfig` takes `*FileConfig`):

```go
func TestMergeConfigUnionsSandboxAdditionalWriteRoots(t *testing.T) {
	dst := FileConfig{}
	dst.Sandbox.AdditionalWriteRoots = []string{"/global/one"}
	src := FileConfig{}
	src.Sandbox.AdditionalWriteRoots = []string{"/extra/one", "/global/one"}
	mergeConfig(&dst, src)
	want := []string{"/global/one", "/extra/one"}
	if !reflect.DeepEqual(dst.Sandbox.AdditionalWriteRoots, want) {
		t.Fatalf("AdditionalWriteRoots=%v want union %v (append + dedupe, not replace)", dst.Sandbox.AdditionalWriteRoots, want)
	}
}

func TestMergeProjectConfigIgnoresAdditionalWriteRoots(t *testing.T) {
	dst := FileConfig{}
	dst.Sandbox.AdditionalWriteRoots = []string{"/global/one"}
	src := FileConfig{}
	src.Sandbox.AdditionalWriteRoots = []string{"/repo/sneaky"}
	if err := mergeProjectConfig(&dst, src); err != nil {
		t.Fatalf("mergeProjectConfig: %v", err)
	}
	if !reflect.DeepEqual(dst.Sandbox.AdditionalWriteRoots, []string{"/global/one"}) {
		t.Fatalf("AdditionalWriteRoots=%v — project config must NOT be able to add write roots", dst.Sandbox.AdditionalWriteRoots)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestResolveMergesSandboxAdditionalWriteRoots -v`
Expected: FAIL — `AdditionalWriteRoots undefined` (compile error).

- [ ] **Step 3: Implement**

In `internal/config/types.go`, extend SandboxConfig:

```go
type SandboxConfig struct {
	MaxAutonomy string `json:"maxAutonomy,omitempty"`
	// Network controls whether shell commands classified as network-touching
	// (curl, git push, package installs, …) are allowed: "allow" or "deny".
	// Empty keeps the built-in default (deny). Without this knob the engine's
	// hard-coded NetworkDeny was unreachable from any config surface.
	Network string `json:"network,omitempty"`
	// AdditionalWriteRoots lists directories outside the workspace the sandbox
	// allows writes in. Each entry must be an existing directory; entries are
	// normalized (~-expanded, absolutized, symlink-resolved) at startup and an
	// invalid entry fails the run. Session-only grants use /add-dir instead.
	AdditionalWriteRoots []string `json:"additionalWriteRoots,omitempty"`
}
```

In `internal/config/resolver.go`, inside `mergeConfig` next to the `src.Sandbox.Network` block (~line 153), add a UNION merge (append + dedupe — a later config layer must add to, not replace, the global grants):

```go
	dst.Sandbox.AdditionalWriteRoots = unionStrings(dst.Sandbox.AdditionalWriteRoots, src.Sandbox.AdditionalWriteRoots)
```

with the helper (place near the other small helpers in resolver.go):

```go
// unionStrings appends the values of extra that are not already present in
// base, preserving order. Used for additive config keys like
// sandbox.additionalWriteRoots where a later layer must not erase earlier
// grants.
func unionStrings(base []string, extra []string) []string {
	seen := make(map[string]struct{}, len(base))
	for _, value := range base {
		seen[value] = struct{}{}
	}
	for _, value := range extra {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		base = append(base, value)
	}
	return base
}
```

Do NOT touch `mergeProjectConfig`: it is an explicit allowlist of project-settable keys, and `additionalWriteRoots` must stay off it — a repo-controlled `.zero/config.json` must not be able to widen write access outside the workspace. Add this comment above `mergeProjectConfig`'s Sandbox block so the omission reads as deliberate:

```go
	// Sandbox.AdditionalWriteRoots is intentionally NOT merged from project
	// config: a cloned repo's .zero/config.json must not be able to grant
	// itself write access outside the workspace. Global config and CLI flags
	// are the only config sources for write roots.
```

`Resolve` copies the whole Sandbox struct into the resolved view (`Sandbox: cfg.Sandbox` ~line 103), so no further change is needed there.

- [ ] **Step 4: Run config tests**

Run: `go test ./internal/config/ 2>&1 | tail -3`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "config: add sandbox.additionalWriteRoots"
```

---

## Task 6: CLI wiring (`--add-dir` on `zero` and `zero exec`)

**Files:**
- Modify: `internal/cli/app.go` (`runWithDeps` arg handling at lines 138-155, `runInteractiveTUI` at 310-389, `newCoreRegistry` at 398)
- Modify: `internal/cli/exec.go` (`execOptions` struct at :50, `buildExecSandboxEngine` and the exec registry construction — locate with `grep -n "newCoreRegistry\|buildExecSandboxEngine\|CoreTools" internal/cli/exec.go`)
- Modify: `internal/cli/exec_parse.go` (flag cases, mirror `--image` at lines 80-92)
- Modify: help text (`writeHelp` in app.go's help file and `writeExecHelp` — locate with `grep -rn "func writeHelp\|func writeExecHelp" internal/cli/`)
- Test: `internal/cli/exec_parse_image_test.go` pattern → new `internal/cli/exec_parse_add_dir_test.go`; app-level test next to existing `runWithDeps` tests

- [ ] **Step 1: Write the failing parse tests**

Create `internal/cli/exec_parse_add_dir_test.go`:

```go
package cli

import "testing"

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
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cli/ -run 'TestParseExecArgsCollectsAddDirs|TestParseExecArgsAddDirRequiresValue|TestSplitLeadingAddDirFlags' -v`
Expected: FAIL — `options.addDirs undefined`, `undefined: splitLeadingAddDirFlags`.

- [ ] **Step 3: Implement parsing**

(a) `internal/cli/exec.go` — add `addDirs []string` to `execOptions` (after `cwd`).

(b) `internal/cli/exec_parse.go` — add cases mirroring the `--image` pair at lines 80-92:

```go
		case arg == "--add-dir":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.addDirs = append(options.addDirs, value)
			index = next
		case strings.HasPrefix(arg, "--add-dir="):
			value, err := requiredInlineFlagValue(arg, "--add-dir")
			if err != nil {
				return options, false, err
			}
			options.addDirs = append(options.addDirs, value)
```

(c) `internal/cli/app.go` — add the leading-flag splitter:

```go
// splitLeadingAddDirFlags strips leading --add-dir flags from the root
// argument list (zero --add-dir <path> [--add-dir <path>] [subcommand …]).
// Subcommands like exec parse their own --add-dir occurrences.
func splitLeadingAddDirFlags(args []string) ([]string, []string, error) {
	addDirs := []string{}
	for len(args) > 0 {
		switch {
		case args[0] == "--add-dir":
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				return nil, nil, errors.New("--add-dir requires a directory path")
			}
			addDirs = append(addDirs, args[1])
			args = args[2:]
		case strings.HasPrefix(args[0], "--add-dir="):
			value := strings.TrimSpace(strings.TrimPrefix(args[0], "--add-dir="))
			if value == "" {
				return nil, nil, errors.New("--add-dir requires a directory path")
			}
			addDirs = append(addDirs, value)
			args = args[1:]
		default:
			return addDirs, args, nil
		}
	}
	return addDirs, args, nil
}
```

In `runWithDeps` (line 138), immediately after `deps = fillAppDeps(deps)`:

```go
	addDirs, args, err := splitLeadingAddDirFlags(args)
	if err != nil {
		return writeAppError(stderr, err.Error(), 1)
	}
```

Then pass `addDirs` into the two `runInteractiveTUI` calls (lines 145 and 154); `runInteractiveTUI` gains a final `addDirs []string` parameter.

- [ ] **Step 4: Wire the scope in `runInteractiveTUI` and exec**

(a) In `runInteractiveTUI` (app.go:310), after `resolved, err := deps.resolveConfig(...)` succeeds, build the scope and use it for both the registry and the engine:

```go
	scope, err := sandbox.NewScope(workspaceRoot, append(append([]string{}, resolved.Sandbox.AdditionalWriteRoots...), addDirs...))
	if err != nil {
		return writeAppError(stderr, err.Error(), 1)
	}
```

Change `registry := newCoreRegistry(workspaceRoot)` to `registry := newCoreRegistryScoped(workspaceRoot, scope)` and add `Scope: scope,` to the `sandbox.EngineOptions{...}` literal at line 362.

(b) Update `newCoreRegistry` (app.go:398):

```go
func newCoreRegistry(workspaceRoot string) *tools.Registry {
	return newCoreRegistryScoped(workspaceRoot, nil)
}

func newCoreRegistryScoped(workspaceRoot string, scope tools.PathScope) *tools.Registry {
	registry := tools.NewRegistry()
	for _, tool := range tools.CoreToolsScoped(workspaceRoot, scope) {
		registry.Register(tool)
	}
	return registry
}
```

(c) In `internal/cli/exec.go`: `buildExecSandboxEngine` gains a `scope *sandbox.Scope` parameter and sets `Scope: scope` in its `EngineOptions`. In the exec run path (`grep -n "buildExecSandboxEngine\|newCoreRegistry" internal/cli/exec.go` to find both call sites), build the scope first from the exec workspace root, `resolved.Sandbox.AdditionalWriteRoots`, and `options.addDirs` (same `sandbox.NewScope` call as (a), erroring out via exec's existing error path), then pass it to both the registry construction and `buildExecSandboxEngine`.

(d) Help text: add to the root help (`writeHelp`) flag list:
```text
  --add-dir <path>   Allow writes in an extra directory (repeatable)
```
and the same line to `writeExecHelp`'s flag section, matching surrounding formatting.

- [ ] **Step 5: Run CLI tests and build**

Run: `go build ./... && go test ./internal/cli/ 2>&1 | tail -5`
Expected: build OK, `ok github.com/Gitlawb/zero/internal/cli`.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/
git commit -m "cli: add repeatable --add-dir flag wiring scope into registry and engine"
```

---

## Task 7: TUI `/add-dir` command

**Files:**
- Modify: `internal/tui/commands.go` (kind const block at lines 9-37, definitions table at 64-230)
- Modify: `internal/tui/model.go` (command dispatch switch, next to `case commandImage:` at ~line 1096)
- Create: `internal/tui/add_dir.go`
- Test: `internal/tui/add_dir_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/tui/add_dir_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/sandbox"
)

func TestParseCommandRecognizesAddDir(t *testing.T) {
	parsed := parseCommand("/add-dir /tmp/extra")
	if parsed.kind != commandAddDir {
		t.Fatalf("kind=%v want commandAddDir", parsed.kind)
	}
	if parsed.text != "/tmp/extra" {
		t.Fatalf("text=%q want path argument", parsed.text)
	}
}

func TestHandleAddDirCommand(t *testing.T) {
	workspace := t.TempDir()
	extra := t.TempDir()
	scope, err := sandbox.NewScope(workspace, nil)
	if err != nil {
		t.Fatalf("NewScope: %v", err)
	}
	engine := sandbox.NewEngine(sandbox.EngineOptions{WorkspaceRoot: workspace, Policy: sandbox.DefaultPolicy(), Scope: scope})
	m := model{agentOptions: agent.Options{Sandbox: engine}}

	m = m.handleAddDirCommand(extra)
	if len(scope.Roots()) != 2 {
		t.Fatalf("Roots()=%v want workspace + extra after /add-dir", scope.Roots())
	}
	last := m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "write access added") || !strings.Contains(last.text, "session only") {
		t.Fatalf("confirmation line=%q want added + session-only notice", last.text)
	}

	m = m.handleAddDirCommand("")
	last = m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, workspace) {
		t.Fatalf("bare /add-dir output=%q want root listing", last.text)
	}

	m = m.handleAddDirCommand("/definitely/not/a/real/dir")
	last = m.transcript[len(m.transcript)-1]
	if !strings.Contains(last.text, "add-dir:") {
		t.Fatalf("invalid path output=%q want add-dir error", last.text)
	}
}
```

Adapt the transcript access (`m.transcript[len(m.transcript)-1].text`) to the actual transcript entry shape — check how existing TUI tests read appended system lines (`grep -rn "actionAppendSystem" internal/tui/*_test.go | head -3`) and mirror that accessor.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tui/ -run 'TestParseCommandRecognizesAddDir|TestHandleAddDirCommand' -v`
Expected: FAIL — `undefined: commandAddDir`.

- [ ] **Step 3: Implement**

(a) `internal/tui/commands.go`: add `commandAddDir` to the kind const block (before `commandUnknown`), and this entry to `commandDefinitions` (after the `/image` entry to keep session-group commands together):

```go
	{
		name:        "/add-dir",
		usage:       "/add-dir [path]",
		group:       commandGroupRuntime,
		description: "Grant write access to a directory outside the workspace for this session; bare form lists current write roots.",
		kind:        commandAddDir,
	},
```

(b) Create `internal/tui/add_dir.go`:

```go
package tui

import (
	"strings"
)

// handleAddDirCommand processes "/add-dir [path]". A bare "/add-dir" lists the
// current write roots; with a path it grants session-scoped write access via
// the sandbox engine's shared scope, so the policy gate, the OS profile of the
// next bash command, and the file tools all widen immediately.
func (m model) handleAddDirCommand(arg string) model {
	engine := m.agentOptions.Sandbox
	if engine == nil || engine.Scope() == nil {
		return m.appendSystemNotice("add-dir: sandbox scope is unavailable in this session.")
	}
	scope := engine.Scope()
	trimmed := strings.TrimSpace(arg)
	if trimmed == "" {
		return m.appendSystemNotice("Write roots:\n  " + strings.Join(scope.Roots(), "\n  ") + "\nUsage: /add-dir <path>  (grants are session-only; use sandbox.additionalWriteRoots in config to persist)")
	}
	root, err := scope.Add(trimmed)
	if err != nil {
		return m.appendSystemNotice("add-dir: " + err.Error())
	}
	return m.appendSystemNotice("write access added: " + root + " (this session only)")
}

func (m model) appendSystemNotice(text string) model {
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
	return m
}
```

If `appendImageNotice` in `image_attach.go` is byte-identical to `appendSystemNotice`, refactor `image_attach.go` to call the new shared helper instead of keeping two copies.

(c) `internal/tui/model.go`: in the command dispatch switch, next to `case commandImage:` (~line 1096):

```go
	case commandAddDir:
		m = m.handleAddDirCommand(command.text)
		return m, nil
```

- [ ] **Step 4: Run TUI tests**

Run: `go test ./internal/tui/ 2>&1 | tail -3`
Expected: PASS (including any command-table snapshot/help tests — update their expected lists if they enumerate commands).

- [ ] **Step 5: Commit**

```bash
git add internal/tui/
git commit -m "tui: add /add-dir command for session write-root grants"
```

---

## Task 8: Observability (`zero sandbox` status + snapshots)

**Files:**
- Modify: `internal/cli/sandbox.go` (`runSandboxPolicyEffective` at :117, `formatEffectiveSandboxPolicy` at :146; find the caller with `grep -n "runSandboxPolicyEffective(" internal/cli/sandbox.go`)
- Modify: `internal/zerocommands/sandbox_snapshots.go` (`SandboxPlanSnapshot` at :75; find builders with `grep -rn "SandboxPlanSnapshot{" internal/`)
- Test: the existing tests for these surfaces (`grep -rn "formatEffectiveSandboxPolicy\|SandboxPlanSnapshot" internal/cli/*_test.go internal/zerocommands/*_test.go`)

- [ ] **Step 1: Write the failing test**

Append to the test file that already covers `formatEffectiveSandboxPolicy`:

```go
func TestEffectiveSandboxPolicyListsWriteRoots(t *testing.T) {
	out := formatEffectiveSandboxPolicy("/ws", zeroSandbox.DefaultPolicy(), zeroSandbox.Backend{}, zeroSandbox.BackendPlan{}, resolveSandboxGuards(zeroSandbox.DefaultPolicy()), "/grants", []string{"/ws", "/extra"})
	if !strings.Contains(out, "write_roots: /ws, /extra") {
		t.Fatalf("missing write_roots line:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/cli/ -run TestEffectiveSandboxPolicyListsWriteRoots -v`
Expected: FAIL — signature mismatch (compile error).

- [ ] **Step 3: Implement**

(a) `formatEffectiveSandboxPolicy` gains a trailing `writeRoots []string` parameter; insert after the `enforce_workspace` line:

```go
		"write_roots: " + strings.Join(writeRoots, ", "),
```

(b) `runSandboxPolicyEffective` gains the same trailing parameter, adds `WriteRoots []string \`json:"writeRoots"\`` to its inline JSON payload struct (populated with the parameter), and forwards it to `formatEffectiveSandboxPolicy`.

(c) The caller of `runSandboxPolicyEffective` has the resolved config and workspace root; compute the roots the same way the engine does (workspace first, then `resolved.Sandbox.AdditionalWriteRoots` normalized via `sandbox.NewScope(workspaceRoot, roots)` then `scope.Roots()`; on NewScope error, fall back to `[]string{workspaceRoot}` and let the run continue — status must not crash on a stale config entry; append the error to the output line instead: `"write_roots_error: " + err.Error()`).

(d) `internal/zerocommands/sandbox_snapshots.go`: add to `SandboxPlanSnapshot`:

```go
	WriteRoots []string `json:"writeRoots,omitempty"`
```

Update each `SandboxPlanSnapshot{` builder found by the grep to populate `WriteRoots` from the engine scope where one is reachable (`engine.Scope().Roots()`); where no engine/scope exists in the builder's inputs, leave it empty (omitempty keeps the JSON shape stable).

- [ ] **Step 4: Run both suites**

Run: `go test ./internal/cli/ ./internal/zerocommands/ 2>&1 | tail -4`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/sandbox.go internal/zerocommands/ internal/cli/*_test.go
git commit -m "sandbox: surface write roots in zero sandbox status and plan snapshots"
```

---

## Task 9: Full verification + docs

**Files:**
- Modify: `docs/` CLI/flag documentation if it exists (`grep -rln "image\b\|--autonomy" docs/ | head -5` — add `--add-dir` and `/add-dir` wherever flags/commands are enumerated)

- [ ] **Step 1: Full build, vet, test**

Run: `go build ./... && go vet ./... && go test ./... 2>&1 | grep -v "^ok" | head -20`
Expected: no FAIL lines. (`internal/config` provider-command tests are known to time out under full-suite load on this machine; re-run that package in isolation if it fails: `go test ./internal/config/`.)

- [ ] **Step 2: Manual smoke test (macOS seatbelt — the original bug)**

```bash
mkdir -p /tmp/zero-adddir-demo
go run ./cmd/zero exec --add-dir /tmp/zero-adddir-demo --autonomy high --prompt 'run: echo hello > /tmp/zero-adddir-demo/out.txt, then run: cat /tmp/zero-adddir-demo/out.txt'
cat /tmp/zero-adddir-demo/out.txt
```
Expected: `hello` — the write outside the workspace succeeds. Then confirm the deny path still works and is actionable:

```bash
go run ./cmd/zero exec --autonomy high --prompt 'run: echo hi > /tmp/zero-adddir-demo/denied.txt' 2>&1 | grep -i "add-dir"
```
Expected: the failure output mentions `--add-dir`.

- [ ] **Step 3: Update docs found by the grep, then commit**

```bash
git add docs/
git commit -m "docs: document --add-dir and /add-dir write-root grants"
```

---

## Self-review notes (already applied)

- Spec coverage: Scope (Task 1), engine+risk (Task 2), seatbelt/bubblewrap/cwd (Task 3), file tools incl. bash cwd (Task 4), config key (Task 5), `--add-dir` on both entrypoints + fail-fast (Task 6), `/add-dir` session-only + listing (Task 7), status/snapshots (Task 8), security invariants are encoded in Task 1's rejections and Task 2/3's tests.
- Known judgment calls an implementer must preserve: relative paths NEVER resolve against extra roots; nil scope must be byte-identical to today's behavior; checkpoint/rewind (`mutation_targets.go`) stays workspace-only.
- Line numbers reference commit `6fb0231`; re-grep the named anchors if the tree has moved.
