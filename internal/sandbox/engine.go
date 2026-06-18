package sandbox

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

// reasonAboveCeiling is the Decision.Reason emitted when a grant-allow or unsafe
// escalation is clamped to a prompt because it exceeds the configured autonomy ceiling.
const reasonAboveCeiling = "above policy ceiling"

type EngineOptions struct {
	WorkspaceRoot string
	Policy        Policy
	Store         *GrantStore
	Backend       Backend
	Scope         *Scope
}

type Engine struct {
	workspaceRoot   string
	policy          Policy
	store           *GrantStore
	backend         Backend
	scope           *Scope
	sessionGrants   *memoryGrantSet
	sessionProfiles *permissionProfileGrantSet
	turnProfiles    *permissionProfileGrantSet
}

func NewEngine(options EngineOptions) *Engine {
	policy := options.Policy
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	scope := options.Scope
	workspaceRoot := strings.TrimSpace(options.WorkspaceRoot)
	if scope != nil && workspaceRoot == "" {
		// Scope-only construction must still populate workspaceRoot: Evaluate's
		// path classification and EnforceWorkspace denial both guard on
		// request.WorkspaceRoot != "", and resolveCommandDir hard-requires it, so
		// leaving it blank would silently skip enforcement. Roots()[0] is the
		// workspace root by the Scope contract.
		if roots := scope.Roots(); len(roots) > 0 {
			workspaceRoot = roots[0]
		}
	}
	if scope == nil && workspaceRoot != "" {
		scope = &Scope{workspaceRoot: normalizeWorkspaceRootBestEffort(workspaceRoot)}
	}
	return &Engine{
		workspaceRoot:   workspaceRoot,
		policy:          policy,
		store:           options.Store,
		backend:         options.Backend,
		scope:           scope,
		sessionGrants:   newMemoryGrantSet(),
		sessionProfiles: newPermissionProfileGrantSet(),
		turnProfiles:    newPermissionProfileGrantSet(),
	}
}

// Scope returns the engine's shared write scope (nil when the engine was
// built without a workspace root and no explicit Scope option). The TUI uses
// it for /add-dir.
func (engine *Engine) Scope() *Scope {
	if engine == nil {
		return nil
	}
	return engine.scope
}

func (engine *Engine) CanPersistGrants() bool {
	return engine != nil && engine.store != nil
}

// ReadExclusions returns the resolved DenyRead/AllowRead exclusion matcher for
// this engine's policy, resolving each policy entry ONCE. The search tools build
// it a single time per run and reuse it across the whole walk so the predicates
// don't re-run Abs/EvalSymlinks per visited path. Returns nil for a nil engine
// (the matcher's methods treat nil as "exclude nothing").
func (engine *Engine) ReadExclusions() *ReadExclusions {
	// A disabled policy enforces nothing, so it must not filter search results
	// either (Evaluate already allows every request under ModeDisabled).
	if engine == nil {
		return nil
	}
	policy := engine.effectivePolicy(engine.policy)
	if policy.Mode == ModeDisabled {
		return nil
	}
	return &ReadExclusions{
		workspaceRoot: engine.workspaceRoot,
		denyRoots:     resolvePolicyPaths(policy.DenyRead),
		allowRoots:    resolvePolicyPaths(policy.AllowRead),
	}
}

// ReadExclusionGlobs returns the ripgrep-style --glob exclusion args for this
// engine's policy + scope (see the package-level ReadExclusionGlobs). Empty when
// DenyRead is unset or the engine has no scope.
func (engine *Engine) ReadExclusionGlobs() []string {
	// A disabled policy filters nothing (parity with ReadExclusions / Evaluate).
	if engine == nil {
		return nil
	}
	policy := engine.effectivePolicy(engine.policy)
	if policy.Mode == ModeDisabled {
		return nil
	}
	return ReadExclusionGlobs(policy, engine.scope)
}

// effectiveNetworkMode is the single source of truth for the engine's active
// network mode: it collapses an empty-allowlist scoped policy to deny, and ALSO
// downgrades scoped to deny when the backend cannot actually route scoped egress
// (only sandbox-exec can; bubblewrap's isolated netns and policy-only cannot), so
// a scoped policy that can't be enforced fails closed. Both Evaluate and
// NetworkHostAllowed go through this so the engine-level decision and the
// per-tool gate never diverge.
func (engine *Engine) effectiveNetworkMode(policy Policy) NetworkMode {
	mode := effectiveNetwork(policy)
	if mode == NetworkScoped && !engine.backend.EnforcesScopedEgress() {
		return NetworkDeny
	}
	return mode
}

// NetworkHostAllowed reports whether the engine's policy permits a network
// connection to host, plus the effective network mode that decided it. It is the
// shared gate so non-shell network tools (e.g. web_fetch) honor the SAME
// allow/deny/scoped policy — including the backend-aware fail-closed downgrade —
// that Evaluate and the bash egress proxy enforce, rather than each tool
// reimplementing domain matching. host may include a :port; only the hostname is
// matched. A disabled policy or nil engine allows everything (network tools keep
// their pre-sandbox behaviour); deny blocks everything; scoped allows only hosts
// in AllowedDomains minus DeniedDomains (an empty allowlist, or a backend that
// can't enforce scoped egress, collapses to deny).
//
// By default the first-party, in-process tools that consult this gate are NOT
// subject to the network policy (EnforceToolNetwork is off): that policy exists
// to confine the sandboxed SHELL's egress, which these tools don't use, and they
// retain their own SSRF/port/redirect safeguards. Set EnforceToolNetwork to also
// hold them to the allow/scoped/deny policy. The sandboxed-shell egress decision
// lives in Evaluate via effectiveNetworkMode and is unaffected by this flag.
func (engine *Engine) NetworkHostAllowed(host string) (bool, NetworkMode) {
	if engine == nil {
		return true, NetworkAllow
	}
	policy := engine.effectivePolicy(engine.policy)
	if policy.Mode == ModeDisabled {
		return true, NetworkAllow
	}
	if !policy.EnforceToolNetwork {
		// First-party in-process tools are exempt unless the operator opts in.
		return true, NetworkAllow
	}
	switch mode := engine.effectiveNetworkMode(policy); mode {
	case NetworkAllow:
		return true, mode
	case NetworkScoped:
		allowed := domainAllowed(host, normalizeDomains(policy.AllowedDomains), normalizeDomains(policy.DeniedDomains))
		return allowed, mode
	default:
		return false, NetworkDeny
	}
}

// toolNetworkExempt reports whether a request is exempt from the engine-level
// network deny because it is a first-party, in-process network TOOL — one that
// declares SideEffectNetwork (web_search / web_fetch) — and the operator has not
// opted into EnforceToolNetwork. Such tools do not use the sandboxed shell's
// egress; they keep their own SSRF/host safeguards, and NetworkHostAllowed (also
// gated by EnforceToolNetwork) governs them at run time. A SHELL command merely
// classified as network (SideEffectShell) is NOT exempt, so shell egress stays
// blocked under deny. Mirrors the NetworkHostAllowed exemption so the Evaluate
// gate and the per-tool gate never diverge.
func (engine *Engine) toolNetworkExempt(policy Policy, request Request) bool {
	return !policy.EnforceToolNetwork && request.SideEffect == SideEffectNetwork
}

// scopeFor returns the scope to validate request paths against. The engine's
// shared scope applies only when the request targets the engine's own
// workspace root; a per-request override root gets an ad-hoc single-root scope
// (single-root semantics; it deliberately ignores the engine's extra roots so
// an override can never inherit broader write access). The ad-hoc root is left
// unnormalized on purpose: validateWorkspacePath re-resolves roots internally,
// and skipping normalization avoids per-Evaluate EvalSymlinks syscalls.
func (engine *Engine) scopeFor(requestRoot string) *Scope {
	if engine.scope != nil && requestRoot == engine.workspaceRoot {
		return engine.scope
	}
	return &Scope{workspaceRoot: requestRoot}
}

// shellSandboxActive reports whether a native wrapping sandbox would actually
// wrap a shell command under the given policy. It is true only when the policy
// is enforcing and the engine's backend can wrap commands with native isolation
// (bubblewrap / sandbox-exec available). A policy-only fallback, a disabled
// policy, or a nil engine all report false — so AutoAllowBashWhenSandboxed never
// auto-allows an unsandboxed command.
func (engine *Engine) shellSandboxActive(policy Policy) bool {
	if engine == nil {
		return false
	}
	if policy.Mode == ModeDisabled {
		return false
	}
	backend := engine.backend
	return backend.Available && backend.Executable != "" && backend.CommandWrapping && backend.NativeIsolation
}

// Precheck reports the sandbox violations that would block a tool request BEFORE
// it executes, so a caller (e.g. a batch confirmation or a "would this run?"
// check) can fail fast and surface the reason instead of discovering it mid-run.
// It reuses Evaluate, so policy is never duplicated: a request the engine would
// allow or merely prompt for yields no violations; a denied request yields its
// violation. A nil engine (sandbox disabled) yields no violations.
func (engine *Engine) Precheck(ctx context.Context, request Request) []Violation {
	if engine == nil {
		return nil
	}
	return violationsFromDecision(engine.Evaluate(ctx, request))
}

// violationsFromDecision extracts the blocking violations from a decision. Only a
// deny carries one; Evaluate sets Decision.Violation for policy denials, and the
// fallback synthesizes one for the rare deny without a structured violation so a
// caller always gets a reason.
func violationsFromDecision(decision Decision) []Violation {
	if decision.Action != ActionDeny {
		return nil
	}
	if decision.Violation != nil {
		return []Violation{*decision.Violation}
	}
	return []Violation{{
		Code:   ViolationPolicyDenied,
		Action: ActionDeny,
		Risk:   decision.Risk,
		Reason: decision.ErrorString(),
	}}
}

func (engine *Engine) Evaluate(ctx context.Context, request Request) Decision {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		risk := Classify(request)
		return deny(request, risk, ViolationContextCanceled, "", "sandbox evaluation cancelled: "+err.Error(), false)
	}
	if engine == nil {
		return Decision{Action: ActionAllow, Risk: Classify(request), Reason: "sandbox disabled"}
	}
	policy := engine.effectivePolicy(engine.policy)
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	if policy.MaxAutonomy == "" {
		// A directly-constructed Policy{} (bypassing DefaultPolicy) leaves the
		// ceiling empty, which NormalizeAutonomy would read as Low and clamp every
		// Medium/High decision to Prompt. Default empty to High so the ceiling is a
		// no-op unless explicitly configured (fail-open is correct here: the empty
		// value signals "unset", not "lock everything down").
		policy.MaxAutonomy = AutonomyHigh
	}
	request.WorkspaceRoot = firstNonEmpty(request.WorkspaceRoot, engine.workspaceRoot)
	request.Permission = NormalizePermission(request.Permission)
	request.PermissionMode = NormalizePermissionMode(request.PermissionMode)
	request.SideEffect = NormalizeSideEffect(request.SideEffect)
	// Preserve the raw requested autonomy for the ceiling checks below. A
	// genuinely-invalid value (NormalizeAutonomy("") is Low, not an error, so only
	// bogus values land here) gets a safe High placeholder for risk classification
	// and grant lookup, but the ceiling check uses rawAutonomy so autonomyAllowed's
	// unknown-tier guard fails it CLOSED (clamps to Prompt) under ANY ceiling —
	// including the default High, where a normalized-High value would wrongly pass
	// autonomyAllowed(High, High).
	rawAutonomy := request.Autonomy
	autonomy, err := NormalizeAutonomy(request.Autonomy)
	if err != nil {
		autonomy = AutonomyHigh
	}
	request.Autonomy = autonomy
	scope := engine.scopeFor(request.WorkspaceRoot)
	risk := classifyWithScope(request, scope)

	if policy.Mode == ModeDisabled {
		return Decision{Action: ActionAllow, Risk: risk, Reason: "sandbox policy disabled"}
	}
	if request.Permission == PermissionDeny {
		return deny(request, risk, ViolationDeniedPermission, "", permissionReason(request), false)
	}
	// The fine-grained path lists (DenyRead/DenyWrite/AllowRead/AllowWrite) apply
	// whenever the sandbox is enforcing, independent of EnforceWorkspace and even
	// when there is no workspace root (absolute paths are still resolved and
	// matched), so they are honored consistently with the grep/glob exclusion path
	// and can't be bypassed by an engine built without a workspace root. The
	// workspace boundary itself needs a root, so it is gated on having one. Mode is
	// already known to be enforcing here (ModeDisabled returned above).
	enforceWorkspace := policy.EnforceWorkspace && request.WorkspaceRoot != ""
	if violation := applyPatchPathViolation(request); violation != nil {
		return deny(request, risk, violation.Code, violation.Path, violation.Reason, false)
	}
	for _, requested := range requestPaths(request) {
		if violation := validatePathWithPolicy(scope, policy, request.SideEffect, enforceWorkspace, request.WorkspaceRoot, requested); violation != nil {
			return deny(request, risk, violation.Code, violation.Path, violation.Reason, false)
		}
	}
	// effectiveNetworkMode collapses an empty-allowlist scoped policy to deny, and
	// downgrades scoped to deny when the backend can't route through the filtering
	// proxy (bubblewrap's isolated netns has no bridge, policy-only has no
	// isolation) — so a scoped policy that can't be enforced fails closed rather
	// than running with unrestricted networking. Shared with NetworkHostAllowed so
	// the per-tool gate can't diverge from this decision. Allow is unchanged.
	netMode := engine.effectiveNetworkMode(policy)
	if netMode == NetworkDeny && HasRiskCategory(risk, "network") && !engine.toolNetworkExempt(policy, request) {
		return deny(request, risk, ViolationNetwork, "", "network access is blocked by sandbox policy", false)
	}
	if policy.DenyDestructiveShell && HasRiskCategory(risk, "destructive") {
		return deny(request, risk, ViolationDestructiveCommand, "", "destructive shell command is blocked by sandbox policy", false)
	}
	reqRaw, reqKind := DeriveScope(request.ToolName, request.Args)
	reqScope := resolveScopeForKind(reqRaw, reqKind, request.WorkspaceRoot)
	if engine.store != nil {
		match, err := engine.store.Lookup(request.ToolName, reqScope, request.Autonomy)
		if err == nil && match.Matched {
			grant := match.Grant
			if grant.Decision == GrantDeny {
				decision := deny(request, risk, ViolationPersistentDeny, "", "persistent sandbox deny grant matched", true)
				decision.GrantMatched = true
				decision.Grant = &grant
				return decision
			}
			if !autonomyAllowed(rawAutonomy, policy.MaxAutonomy) {
				return Decision{
					Action:       ActionPrompt,
					Reason:       reasonAboveCeiling,
					Risk:         risk,
					GrantMatched: true,
					Grant:        &grant,
				}
			}
			return Decision{
				Action:       ActionAllow,
				Reason:       "persistent sandbox allow grant matched",
				Risk:         risk,
				GrantMatched: true,
				Grant:        &grant,
			}
		}
	}
	if match := engine.lookupSessionGrant(request.ToolName, reqScope, request.Autonomy); match.Matched {
		grant := match.Grant
		if !autonomyAllowed(rawAutonomy, policy.MaxAutonomy) {
			return Decision{
				Action:       ActionPrompt,
				Reason:       reasonAboveCeiling,
				Risk:         risk,
				GrantMatched: true,
				Grant:        &grant,
			}
		}
		return Decision{
			Action:       ActionAllow,
			Reason:       "session sandbox allow grant matched",
			Risk:         risk,
			GrantMatched: true,
			Grant:        &grant,
		}
	}
	if request.Permission == PermissionAllow {
		return Decision{Action: ActionAllow, Risk: risk, Reason: permissionReason(request)}
	}
	if workspaceWriteAutoAllowed(policy, request, scope) {
		if !autonomyAllowed(rawAutonomy, policy.MaxAutonomy) {
			return Decision{Action: ActionPrompt, Risk: risk, Reason: reasonAboveCeiling}
		}
		return Decision{Action: ActionAllow, Risk: risk, Reason: "workspace write permitted by sandbox policy", AutoAllowed: true}
	}
	// Auto-allow a sandboxed shell command when the operator opted in: the active
	// native sandbox is the safety boundary, so the bash prompt is skipped. This
	// only applies to shell commands AND only when a wrapping sandbox is actually
	// active; an inactive sandbox (policy-only / disabled) ignores the flag so
	// unsandboxed bash is never silently allowed. It still respects the autonomy
	// ceiling, matching how an explicit permission grant is clamped.
	if policy.AutoAllowBashWhenSandboxed && request.SideEffect == SideEffectShell && engine.shellSandboxActive(policy) {
		if !autonomyAllowed(rawAutonomy, policy.MaxAutonomy) {
			return Decision{Action: ActionPrompt, Risk: risk, Reason: reasonAboveCeiling}
		}
		return Decision{Action: ActionAllow, Risk: risk, Reason: "auto-allowed: sandbox is active for this shell command", AutoAllowed: true}
	}
	if request.PermissionGranted || request.PermissionMode == PermissionUnsafe {
		if !autonomyAllowed(rawAutonomy, policy.MaxAutonomy) {
			return Decision{Action: ActionPrompt, Risk: risk, Reason: reasonAboveCeiling}
		}
		return Decision{Action: ActionAllow, Risk: risk, Reason: permissionReason(request)}
	}
	return Decision{Action: ActionPrompt, Risk: risk, Reason: permissionReason(request)}
}

func (engine *Engine) Grant(input GrantInput) (Grant, error) {
	if engine == nil || engine.store == nil {
		return Grant{}, errors.New("sandbox grant store is not configured")
	}
	input, err := engine.normalizeGrantInput(input)
	if err != nil {
		return Grant{}, err
	}
	return engine.store.Grant(input)
}

func (engine *Engine) GrantForSession(input GrantInput) (Grant, error) {
	if engine == nil {
		return Grant{}, errors.New("sandbox engine is not configured")
	}
	input, err := engine.normalizeGrantInput(input)
	if err != nil {
		return Grant{}, err
	}
	grant, err := createGrant(input, time.Now)
	if err != nil {
		return Grant{}, err
	}
	grant.Session = true
	engine.sessionGrants.add(grant)
	return grant, nil
}

func (engine *Engine) normalizeGrantInput(input GrantInput) (GrantInput, error) {
	kind, err := normalizeScopeKind(input.ScopeKind)
	if err != nil {
		return GrantInput{}, err
	}
	scope, kind := reconcileScope(strings.TrimSpace(input.Scope), kind)
	if kind != ScopeToolWide {
		// Anchor relative path scopes to this workspace so the grant cannot match
		// a same-named path in another project. Host scopes remain network hosts.
		scope = resolveScopeForKind(scope, kind, engine.workspaceRoot)
	}
	input.Scope = scope
	input.ScopeKind = kind
	return input, nil
}

func (engine *Engine) lookupSessionGrant(toolName string, reqScope string, requestedAutonomy Autonomy) GrantLookup {
	if engine == nil {
		return GrantLookup{}
	}
	requested, err := NormalizeAutonomy(requestedAutonomy)
	if err != nil {
		return GrantLookup{}
	}
	return engine.sessionGrants.lookup(toolName, reqScope, requested)
}

func workspaceWriteAutoAllowed(policy Policy, request Request, scope *Scope) bool {
	if !policy.EnforceWorkspace || request.WorkspaceRoot == "" || request.SideEffect != SideEffectWrite {
		return false
	}
	paths := requestPaths(request)
	if len(paths) == 0 || requestPathsTouchProtectedMetadata(scope, request.WorkspaceRoot, paths) {
		return false
	}
	switch request.ToolName {
	case "write_file", "edit_file", "apply_patch":
		return true
	default:
		return false
	}
}

func requestPathsTouchProtectedMetadata(scope *Scope, workspaceRoot string, paths []string) bool {
	if len(paths) == 0 {
		return false
	}
	for _, path := range paths {
		if pathTouchesProtectedMetadata(scope, workspaceRoot, path) {
			return true
		}
	}
	return false
}

func pathTouchesProtectedMetadata(scope *Scope, workspaceRoot string, path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if !filepath.IsAbs(path) {
		return relativePathTouchesProtectedMetadata(path)
	}
	roots := []string{}
	if scope != nil {
		roots = scope.Roots()
	} else if workspaceRoot != "" {
		roots = []string{workspaceRoot}
	}
	for _, root := range roots {
		root = normalizeWorkspaceRootBestEffort(root)
		if root == "" {
			continue
		}
		normalized := NormalizePrefixForRoot(filepath.Clean(path), root)
		relative, err := filepath.Rel(root, normalized)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			continue
		}
		if relativePathTouchesProtectedMetadata(relative) {
			return true
		}
	}
	return false
}

func relativePathTouchesProtectedMetadata(path string) bool {
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) || filepath.IsAbs(cleaned) {
		return false
	}
	first := cleaned
	if index := strings.Index(first, string(filepath.Separator)); index >= 0 {
		first = first[:index]
	}
	for _, protected := range protectedMetadataNames {
		if first == protected {
			return true
		}
	}
	return false
}

func deny(request Request, risk Risk, code ViolationCode, path string, reason string, recoverable bool) Decision {
	violation := &Violation{
		Code:        code,
		ToolName:    request.ToolName,
		Action:      ActionDeny,
		Risk:        risk,
		Path:        path,
		Reason:      reason,
		Recoverable: recoverable,
	}
	return Decision{
		Action:    ActionDeny,
		Reason:    reason,
		Risk:      risk,
		Violation: violation,
	}
}

func permissionReason(request Request) string {
	if request.Reason != "" {
		return request.Reason
	}
	switch request.Permission {
	case PermissionAllow:
		return "tool safety allows execution"
	case PermissionDeny:
		return "tool safety denies execution"
	default:
		return "tool requires approval before execution"
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
