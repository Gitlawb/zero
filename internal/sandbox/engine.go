package sandbox

import (
	"context"
	"errors"
	"strings"
)

type EngineOptions struct {
	WorkspaceRoot string
	Policy        Policy
	Store         *GrantStore
	Backend       Backend
}

type Engine struct {
	workspaceRoot string
	policy        Policy
	store         *GrantStore
	backend       Backend
}

func NewEngine(options EngineOptions) *Engine {
	policy := options.Policy
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	return &Engine{
		workspaceRoot: strings.TrimSpace(options.WorkspaceRoot),
		policy:        policy,
		store:         options.Store,
		backend:       options.Backend,
	}
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
	request.WorkspaceRoot = firstNonEmpty(request.WorkspaceRoot, engine.workspaceRoot)
	request.Permission = NormalizePermission(request.Permission)
	request.PermissionMode = NormalizePermissionMode(request.PermissionMode)
	request.SideEffect = NormalizeSideEffect(request.SideEffect)
	autonomy, err := NormalizeAutonomy(request.Autonomy)
	if err != nil {
		autonomy = AutonomyLow
	}
	request.Autonomy = autonomy
	risk := Classify(request)

	if policy.Mode == ModeDisabled {
		return Decision{Action: ActionAllow, Risk: risk, Reason: "sandbox policy disabled"}
	}
	if request.Permission == PermissionDeny {
		return deny(request, risk, ViolationDeniedPermission, "", permissionReason(request), false)
	}
	if policy.EnforceWorkspace && request.WorkspaceRoot != "" {
		if violation := validateWorkspacePaths(request.WorkspaceRoot, request); violation != nil {
			return deny(request, risk, violation.Code, violation.Path, violation.Reason, false)
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
