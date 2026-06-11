# Permission-UX Phase 1 — scope-enforced grants

Status: approved (2026-06-11). Branch: `feat/permission-ux` (continues Phase 0, commit `742ff48`).

## Problem

Phase 0 derives a human-readable *scope* for a tool call (the file, directory, or
cwd it touches) and shows it on the permission card and the persisted decision
row. But the grant that an "always allow" writes is still **tool-wide**:

- `persistPermissionGrant` (agent/loop.go) stores only `ToolName`.
- the grant file is keyed by tool name: `state.Grants[toolName]`.
- enforcement (`engine.Decide` → `store.Lookup(toolName, autonomy)`, engine.go:100)
  matches on tool name alone.

So clicking "always allow" on a write to `src/main.go` silently authorizes *every*
future write — the blind-yes the Phase-0 card was written to warn against.

## Goal

Persist and enforce the scope so an "always" grant covers exactly what the card
showed: that file, or that directory subtree — nothing else.

## Decisions

1. **Match semantics:** a `file` scope re-matches only its exact path; a `dir`
   scope matches that directory and any descendant; an empty scope (a call with no
   path-like arg, e.g. `bash` with no `cwd`) stays tool-wide. A tool-wide *request*
   is **not** covered by a narrower grant — it re-prompts (fail-safe).
2. **Path anchoring:** scopes are normalized to an absolute, cleaned path at grant
   time, anchored to the workspace root. Matching compares absolute paths, so
   `./src/main.go` == `src/main.go` == `/proj/src/main.go`, and a grant made in
   `/proj-a` never matches `/proj-b`. The card still displays the short relative form.
3. **Storage:** `grants` becomes `map[toolName][]Grant`, one record per
   (scope, decision). Schema bumps `1 → 2`; a v1 file is migrated by reading each
   entry as a single tool-wide grant, so existing grants keep working unchanged.
4. **Precedence:** among grants that cover a request, a covering **deny wins**
   (regardless of autonomy); otherwise the most-specific covering **allow**
   (file > dir > tool-wide; ties broken by longer path, then newest) whose
   `MaxAutonomy` satisfies the request.

## Design

### Scope derivation — single source of truth (`internal/sandbox/grant_scope.go`)

`DeriveScope(toolName, args) (raw string, kind ScopeKind)` is the one place that
knows which args are path-like, reusing Phase 0's key priority (most specific
first): `path`,`file` → `file`; `directory`,`dir`,`cwd` → `dir`. Empty / `.` →
tool-wide. `resolveScopeAbs(raw, workspaceRoot)` cleans and absolutizes (relative
anchored to the workspace root; falls back to `filepath.Abs` when the root is
empty). `agent.permissionScope` (Phase-0 display) is refactored to call
`DeriveScope`, so the displayed scope and the stored/matched scope can never diverge.

`ScopeKind`: `""` (tool-wide) | `"file"` | `"dir"`.

### Data model (`internal/sandbox/grants.go`)

`Grant` gains `Scope` (absolute path, `omitempty`) and `ScopeKind` (`omitempty`).
`grantFile.Grants` becomes `map[string][]Grant`; `grantSchemaVersion` = 2.
`GrantInput` gains `Scope` (raw) + `ScopeKind`.

### Grant creation

The agent derives `(raw, kind)` via `DeriveScope` and passes them in `GrantInput`.
`engine.Grant` resolves `Scope` to absolute using its configured `workspaceRoot`
(the engine already holds one — engine.go:33) before delegating to `store.Grant`,
which appends to the tool's slice (replacing a same-scope entry). CLI grants
(`zero sandbox grants allow|deny <tool>`) leave the scope empty → tool-wide, as today.

### Matching (`engine.Decide` → `store.Lookup`)

`Decide` derives the request's absolute scope from `request.Args` +
`request.WorkspaceRoot` (already populated at engine.go:63) and passes it to a
scope-aware `Lookup(toolName, reqScopeAbs, autonomy)`. `grantCovers` implements the
match semantics above; `Lookup` applies the deny-wins / most-specific-allow
precedence and the per-grant autonomy check. The surrounding allow/deny/ceiling
flow (engine.go:99-126) is unchanged.

### Migration & a fixed bug

`readState` peeks at `schemaVersion`: v1 (`map[string]Grant`) entries become single
tool-wide grants; v2 reads `map[string][]Grant`; anything else still errors with
"unsupported schemaVersion". Keys are canonicalized (trimmed) on read so a
whitespace-padded tool name matches at lookup — closing audit finding #90
(grants.go:146). `writeState` always emits v2.

### CLI surface

`List()` flattens to `[]Grant` (sorted by tool, then scope). `FormatGrantList`
shows the scope (`*` for tool-wide). `Revoke(toolName)` removes all of a tool's
grants and returns the count. Per-scope revoke is out of scope.

## Testing

- `grant_scope_test` / `grants_test`: `DeriveScope` kinds; `resolveScopeAbs` (relative
  anchored, `./` equivalence, absolute passthrough, cross-project non-match);
  `grantCovers` matrix (tool-wide/file/dir × equal/descendant/sibling/parent/
  empty-request); deny-precedence; per-grant + policy autonomy; v1→v2 migration
  round-trip; whitespace-key canonicalization; `FormatGrantList` scope rendering.
- `engine_test`: covered scoped request auto-allows; uncovered sibling re-prompts;
  dir deny blocks a subtree request; legacy tool-wide grant still allows.
- `agent`: `persistPermissionGrant` forwards scope+kind; `TestPermissionScope`
  stays green (display unchanged).

## Out of scope

Card UI changes (Phase 0), per-scope revoke, glob/pattern scopes.
