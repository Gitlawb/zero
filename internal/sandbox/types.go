package sandbox

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// EnvAutoAllowBash is the environment variable that opts in to auto-allowing the
// bash tool when the sandbox is active for the command. It is OFF by default;
// only an explicit truthy value enables it.
const EnvAutoAllowBash = "ZERO_SANDBOX_AUTO_ALLOW_BASH"

type SideEffect string
type Permission string
type PermissionMode string
type Autonomy string
type PolicyMode string
type NetworkMode string
type Action string
type RiskLevel string
type ViolationCode string
type GrantDecision string
type BackendName string
type BackendSupportLevel string
type CapabilityStatus string

const (
	SideEffectRead           SideEffect = "read"
	SideEffectWrite          SideEffect = "write"
	SideEffectShell          SideEffect = "shell"
	SideEffectNetwork        SideEffect = "network"
	SideEffectOutOfWorkspace SideEffect = "out_of_workspace"
	// SideEffectNone marks a control-only tool that performs no read/write/
	// shell/network effect (e.g. escalate_model). It must be recognized so it is
	// not normalized to out_of_workspace and falsely classified as critical.
	SideEffectNone SideEffect = "none"
)

const (
	PermissionAllow  Permission = "allow"
	PermissionPrompt Permission = "prompt"
	PermissionDeny   Permission = "deny"
)

const (
	PermissionModeAuto PermissionMode = "auto"
	PermissionModeAsk  PermissionMode = "ask"
	PermissionUnsafe   PermissionMode = "unsafe"
)

const (
	AutonomyLow    Autonomy = "low"
	AutonomyMedium Autonomy = "medium"
	AutonomyHigh   Autonomy = "high"
)

const (
	ModeDisabled PolicyMode = "disabled"
	ModeEnforce  PolicyMode = "enforce"
)

const (
	NetworkDeny  NetworkMode = "deny"
	NetworkAllow NetworkMode = "allow"
	// NetworkScoped is the middle ground between deny and allow: the sandboxed
	// process may reach only the policy's AllowedDomains (minus DeniedDomains) and
	// nothing else, routed through a local filtering egress proxy. An empty
	// effective allowlist makes it behave exactly like NetworkDeny (fail closed).
	NetworkScoped NetworkMode = "scoped"
)

const (
	ActionAllow  Action = "allow"
	ActionPrompt Action = "prompt"
	ActionDeny   Action = "deny"
)

const (
	RiskLow      RiskLevel = "low"
	RiskMedium   RiskLevel = "medium"
	RiskHigh     RiskLevel = "high"
	RiskCritical RiskLevel = "critical"
)

const (
	ViolationContextCanceled    ViolationCode = "context_canceled"
	ViolationDeniedPermission   ViolationCode = "denied_permission"
	ViolationOutsideWorkspace   ViolationCode = "outside_workspace"
	ViolationSymlinkTraversal   ViolationCode = "symlink_traversal"
	ViolationNetwork            ViolationCode = "network"
	ViolationDestructiveCommand ViolationCode = "destructive_command"
	ViolationPersistentDeny     ViolationCode = "persistent_deny"
)

const (
	GrantAllow GrantDecision = "allow"
	GrantDeny  GrantDecision = "deny"
)

const (
	BackendBubblewrap  BackendName = "bubblewrap"
	BackendSandboxExec BackendName = "sandbox-exec"
	BackendPolicyOnly  BackendName = "policy-only"
)

const (
	BackendSupportNative     BackendSupportLevel = "native"
	BackendSupportPolicyOnly BackendSupportLevel = "policy-only"
)

const (
	CapabilityNative      CapabilityStatus = "native"
	CapabilityPreflight   CapabilityStatus = "preflight"
	CapabilityUnavailable CapabilityStatus = "unavailable"
	CapabilityDisabled    CapabilityStatus = "disabled"
)

type Policy struct {
	Mode                  PolicyMode  `json:"mode"`
	Network               NetworkMode `json:"network"`
	EnforceWorkspace      bool        `json:"enforceWorkspace"`
	DenyDestructiveShell  bool        `json:"denyDestructiveShell"`
	AllowPolicyOnlyRunner bool        `json:"allowPolicyOnlyRunner"`
	MaxAutonomy           Autonomy    `json:"maxAutonomy,omitempty"`
	// AllowedDomains / DeniedDomains apply only when Network is NetworkScoped:
	// the sandboxed process may reach the allowed domains (exact host or any
	// subdomain) minus the denied ones. They are ignored for NetworkAllow and
	// NetworkDeny so existing policies keep their exact behaviour.
	AllowedDomains []string `json:"allowedDomains,omitempty"`
	DeniedDomains  []string `json:"deniedDomains,omitempty"`
	// AutoAllowBashWhenSandboxed, when true, auto-allows the bash tool WITHOUT a
	// permission prompt — but only when the sandbox is actually active (a
	// native-isolation backend wraps the command). The sandbox is then the safety
	// boundary. When the sandbox is not active the flag is ignored: unsandboxed
	// bash is never auto-allowed. Off by default.
	AutoAllowBashWhenSandboxed bool `json:"autoAllowBashWhenSandboxed,omitempty"`
}

type Request struct {
	WorkspaceRoot     string         `json:"workspaceRoot,omitempty"`
	ToolName          string         `json:"toolName"`
	SideEffect        SideEffect     `json:"sideEffect"`
	Permission        Permission     `json:"permission"`
	PermissionGranted bool           `json:"permissionGranted,omitempty"`
	PermissionMode    PermissionMode `json:"permissionMode"`
	Autonomy          Autonomy       `json:"autonomy"`
	Args              map[string]any `json:"args,omitempty"`
	Reason            string         `json:"reason,omitempty"`
}

type Decision struct {
	Action       Action     `json:"action"`
	Reason       string     `json:"reason,omitempty"`
	Risk         Risk       `json:"risk"`
	GrantMatched bool       `json:"grantMatched,omitempty"`
	Grant        *Grant     `json:"grant,omitempty"`
	Violation    *Violation `json:"violation,omitempty"`
	// AutoAllowed marks an allow that the sandbox itself authorized without a user
	// prompt or persistent grant — currently only AutoAllowBashWhenSandboxed for a
	// sandboxed shell command. Enforcement points treat it like a grant-authorized
	// allow so a prompt tool runs without a separately-recorded PermissionGranted.
	AutoAllowed bool `json:"autoAllowed,omitempty"`
}

type Risk struct {
	Level      RiskLevel `json:"level"`
	Categories []string  `json:"categories,omitempty"`
	Reason     string    `json:"reason,omitempty"`
}

type Violation struct {
	Code        ViolationCode `json:"code"`
	ToolName    string        `json:"toolName,omitempty"`
	Action      Action        `json:"action"`
	Risk        Risk          `json:"risk"`
	Path        string        `json:"path,omitempty"`
	Reason      string        `json:"reason"`
	Recoverable bool          `json:"recoverable"`
}

func (violation Violation) Error() string {
	if violation.Path != "" {
		return fmt.Sprintf("Sandbox violation [%s] for %s at %s: %s", violation.Code, violation.ToolName, violation.Path, violation.Reason)
	}
	return fmt.Sprintf("Sandbox violation [%s] for %s: %s", violation.Code, violation.ToolName, violation.Reason)
}

func (decision Decision) ErrorString() string {
	if decision.Violation != nil {
		return decision.Violation.Error()
	}
	if decision.Reason != "" {
		return "Sandbox decision: " + decision.Reason
	}
	return "Sandbox decision denied."
}

func DefaultPolicy() Policy {
	return Policy{
		Mode:                  ModeEnforce,
		Network:               NetworkDeny,
		EnforceWorkspace:      true,
		DenyDestructiveShell:  true,
		AllowPolicyOnlyRunner: true,
		MaxAutonomy:           AutonomyHigh,
	}
}

// AutoAllowBashEnvEnabled reports whether the EnvAutoAllowBash environment
// variable is set to a truthy value. It is the env surface for
// AutoAllowBashWhenSandboxed and is OFF for any unset/blank/falsey value, so the
// safe default (prompt) holds unless the operator explicitly opts in.
func AutoAllowBashEnvEnabled() bool {
	value := strings.TrimSpace(os.Getenv(EnvAutoAllowBash))
	if value == "" {
		return false
	}
	enabled, err := strconv.ParseBool(value)
	return err == nil && enabled
}

// ApplyAutoAllowBashEnv overlays the EnvAutoAllowBash opt-in onto a policy: when
// the env var is truthy it enables AutoAllowBashWhenSandboxed. It never disables
// an already-enabled policy field, so an explicit config opt-in is preserved
// even when the env var is unset. Wire this where the engine policy is built so
// the env surface takes effect.
func ApplyAutoAllowBashEnv(policy Policy) Policy {
	if AutoAllowBashEnvEnabled() {
		policy.AutoAllowBashWhenSandboxed = true
	}
	return policy
}
