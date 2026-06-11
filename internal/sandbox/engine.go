package sandbox

import (
	"context"
	"errors"
	"strings"
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
	workspaceRoot string
	policy        Policy
	store         *GrantStore
	backend       Backend
	scope         *Scope
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
		workspaceRoot: workspaceRoot,
		policy:        policy,
		store:         options.Store,
		backend:       options.Backend,
		scope:         scope,
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
	policy := engine.policy
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
	if policy.EnforceWorkspace && request.WorkspaceRoot != "" {
		for _, requested := range requestPaths(request) {
			if violation := scope.validate(requested); violation != nil {
				return deny(request, risk, violation.Code, violation.Path, violation.Reason, false)
			}
		}
	}
	if policy.Network == NetworkDeny && HasRiskCategory(risk, "network") {
		return deny(request, risk, ViolationNetwork, "", "network access is blocked by sandbox policy", false)
	}
	if policy.DenyDestructiveShell && HasRiskCategory(risk, "destructive") {
		return deny(request, risk, ViolationDestructiveCommand, "", "destructive shell command is blocked by sandbox policy", false)
	}
	if engine.store != nil {
		match, err := engine.store.Lookup(request.ToolName, request.Autonomy)
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
	if request.Permission == PermissionAllow {
		return Decision{Action: ActionAllow, Risk: risk, Reason: permissionReason(request)}
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
	return engine.store.Grant(input)
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
