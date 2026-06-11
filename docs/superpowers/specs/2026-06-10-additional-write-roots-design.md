# Additional write roots (`--add-dir` / `/add-dir`)

**Date:** 2026-06-10
**Status:** Approved

## Problem

Zero's sandbox confines all writes to the workspace root, with no escape hatch.
`EnforceWorkspace` is hardcoded `true` in `DefaultPolicy()`
(`internal/sandbox/types.go`) and `SandboxConfig` exposes only `maxAutonomy`
and `network`. When a user asks the agent to save output outside the workspace
(e.g. "save this to ~/Desktop/dev/untitled folder"), every layer denies it:

1. **Policy engine** — `Engine.Evaluate` → `validateWorkspacePaths`
   (`internal/sandbox/paths.go`) denies tool requests whose paths fall outside
   the workspace root.
2. **macOS seatbelt** — `sandboxExecProfile` (`internal/sandbox/runner.go`)
   emits `(allow file-write* (subpath "<workspaceRoot>"))`, so shell commands
   fail with `Operation not permitted`.
3. **Linux bubblewrap** — `bubblewrapCommandPlan` binds only the workspace
   read-write; system paths are read-only binds.
4. **File tools** — `resolveWorkspacePath` / `recheckWorkspaceWriteTarget`
   (`internal/tools/workspace.go`) confine `write_file`, `edit`, `read_file`,
   `list_directory`, `glob`, `grep`, and the bash tool's `cwd`.

The agent's only recourse is workarounds (zip in workspace, ask the user to
move it), and the denial surfaces as a raw OS error with no guidance.

## Goal

Let users grant zero write access to specific directories outside the
workspace — at launch (flag/config) and mid-session (TUI command) — without
weakening the sandbox elsewhere. Decided UX: **launch flag + runtime command**
(prompt-on-denial may layer on later).

## Non-goals

- No interactive prompt-on-denial flow in this iteration.
- No persistence of `/add-dir` grants across sessions (config is the durable
  surface).
- No widening of network or destructive-shell policy.
- No `enforceWorkspace: false` config knob.

## Design

### 1. Core type: `sandbox.Scope`

New concurrency-safe type in `internal/sandbox` (e.g. `scope.go`):

```go
type Scope struct {
    mu            sync.RWMutex
    workspaceRoot string   // normalized at construction
    extraRoots    []string // normalized, deduplicated
}

func NewScope(workspaceRoot string, extras []string) (*Scope, error)
func (s *Scope) Roots() []string            // workspace first, then extras
func (s *Scope) Add(path string) (string, error) // returns normalized root
func (s *Scope) validate(path string) *pathViolation // package-internal
```

`Add` normalization and guardrails:

- Expand a leading `~` to the user home directory.
- Make absolute, `filepath.Clean`, then `filepath.EvalSymlinks`.
- The path must exist and be a directory (no auto-create).
- Reject the filesystem root `/` (and Windows volume roots) — granting it
  would disable confinement entirely.
- Adding the workspace root or a path inside any existing root is a
  no-op (idempotent success, not an error).

`Validate` generalizes today's `validateWorkspacePath`: a path is allowed if
it validates under **any** root, including the per-segment symlink-traversal
check against that root. The existing single-root function remains as the
per-root helper.

One `*Scope` is constructed at startup and shared by the engine, the command
runner, and the tool registry, so a mid-session `Add` is immediately visible
to all layers.

### 2. Policy engine

- `EngineOptions` gains `Scope *Scope`; `NewEngine` builds a workspace-only
  scope when nil (back-compat for tests/embedders).
- `Evaluate`'s workspace check (engine.go) calls `scope.Validate` per
  requested path instead of `validateWorkspacePaths(root, request)`.
- Risk classification (`risk.go`) treats paths inside any scope root as
  in-workspace, so writes to granted roots are not classified
  `out_of_workspace`/critical.
- Denial reason becomes actionable:
  `"<path> is outside the workspace (use /add-dir or --add-dir to allow writes there)"`.

### 3. OS runners

- `sandboxExecProfile` emits one `(subpath "...")` per scope root inside the
  single `(allow file-write* ...)` rule.
- `bubblewrapCommandPlan` adds `--bind <root> <root>` (read-write, real path)
  for each extra root. The workspace keeps its `/workspace` remount; extra
  roots keep their host paths so absolute paths in commands work unchanged.
- `resolveCommandDir` accepts a command `cwd` inside any scope root.
- Profiles are built per command plan from `scope.Roots()`, so commands run
  after `/add-dir` get the widened profile with no restart.

### 4. File tools

- New narrow interface in `internal/tools`:
  `type PathScope interface { Roots() []string }` (satisfied by
  `*sandbox.Scope`) to avoid a hard dependency direction problem.
- `resolveWorkspacePath` and `recheckWorkspaceWriteTarget` gain scope-aware
  variants: absolute paths are resolved against the first root that contains
  them; **relative paths still resolve against the workspace root only**.
- Tool constructors (`CoreReadOnlyTools`, `CoreWriteTools`, `CoreShellTools`,
  `CoreTools` in `registry.go`) accept the scope (workspace-only default kept
  via a convenience wrapper so existing call sites/tests keep compiling).
- Result: `write_file`, `edit`, `read_file`, `list_directory`, `glob`, `grep`,
  and bash `cwd` all work consistently inside extra roots. Write access
  implies read access within a granted root.

### 5. Config, CLI, TUI

- **Config:** `SandboxConfig` gains
  `AdditionalWriteRoots []string \`json:"additionalWriteRoots,omitempty"\``.
  The key is honored from the **global user config**
  (`os.UserConfigDir()/zero/config.json`) so a grant can apply to every
  project, and from CLI flags/overrides. It is **deliberately excluded from
  the project config allowlist** (`mergeProjectConfig`): a repo-controlled
  `.zero/config.json` must not be able to widen write access outside the
  workspace. Sources are merged as a union (append + dedupe), not replace.
- **CLI:** repeatable `--add-dir <path>` flag on `zero` (TUI) and `zero exec`.
  Effective set = union(flag, config), normalized through `Scope` at startup;
  an invalid root fails fast with a clear error.
- **TUI:** new `/add-dir` command (`internal/tui/commands.go`):
  - `/add-dir` (bare) — lists current write roots.
  - `/add-dir <path>` — calls `scope.Add`; on success appends a system line:
    `write access added: <path> (this session only)`; on failure shows the
    normalization error.
- **Observability:** `zero sandbox` status output and sandbox snapshots
  (`internal/zerocommands/sandbox_snapshots.go`) list extra roots.

### 6. Security invariants

- Extra roots only widen *file-write* (and file-tool read) access under the
  named directories; network and destructive-shell policy are untouched.
- Symlink-traversal protection applies per root exactly as it does for the
  workspace today.
- `/` and volume roots are rejected; roots are symlink-resolved before being
  trusted so a symlinked grant cannot silently point elsewhere later.
- Runtime grants are session-only; durable grants require editing config.

### 7. Testing

- **Scope unit tests:** normalization (`~`, relative, symlink), rejection of
  `/`, missing dirs, files; idempotent adds; multi-root `Validate` including
  symlink-traversal escapes from one root into another denied area.
- **Runner tests:** seatbelt profile string contains one `subpath` per root;
  bubblewrap args contain the extra `--bind`s; `resolveCommandDir` accepts
  extra-root cwd (extend `runner_test.go`).
- **Engine tests:** out-of-workspace write allowed when inside an extra root;
  still denied (with the new actionable message) when outside all roots.
- **Tool tests:** `write_file`/`edit`/`read_file` in an extra root succeed;
  relative paths never resolve against extra roots (extend
  `file_tools_test.go`).
- **TUI tests:** `parseCommand` recognizes `/add-dir`; handler list/add/error
  paths.
- **Config/CLI tests:** flag-config union, fail-fast on invalid root.
- Platform-gated integration tests follow the existing sandbox test patterns.

## Implementation order

1. `sandbox.Scope` + tests.
2. Engine + risk + runner integration (flag-less, scope seeded empty).
3. File-tool scope plumbing.
4. Config key + CLI flags.
5. TUI `/add-dir` command + status output.
