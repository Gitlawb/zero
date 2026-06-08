package sandbox

import "fmt"

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
