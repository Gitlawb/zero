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

// EnvSandboxed marks a process that zero has already wrapped in a sandbox: every
// wrapped command carries ZERO_SANDBOXED=1 in its environment. When such a
// process spawns another command through the engine, the re-entrancy guard
// returns a pass-through plan instead of double-wrapping it; nested platform
// wrappers fail, and a second egress proxy would be redundant. Unset by default.
const EnvSandboxed = "ZERO_SANDBOXED"

// EnvSandboxBackend records which backend wrapped the command. sandboxEnvironment
// always sets it alongside EnvSandboxed, so it serves as a corroborating marker:
// the re-entrancy guard requires BOTH, raising the provenance bar above a single
// ambient flag (a stray or hand-exported ZERO_SANDBOXED=1 with no backend marker
// no longer forces an unsandboxed pass-through).
const EnvSandboxBackend = "ZERO_SANDBOX_BACKEND"

// IsAlreadySandboxed reports whether the current process is already running
// inside a zero-created sandbox. It requires BOTH correlated markers that
// sandboxEnvironment sets together — EnvSandboxed == "1" AND a non-empty
// EnvSandboxBackend — so a single user-set/inherited ZERO_SANDBOXED=1 cannot by
// itself disable wrapping. zero sets both only on genuinely wrapped commands;
// pass-through (direct) plans set neither.
func IsAlreadySandboxed() bool {
	return os.Getenv(EnvSandboxed) == "1" && strings.TrimSpace(os.Getenv(EnvSandboxBackend)) != ""
}

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
type EnforcementLevel string

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
	// ViolationPolicyDenied is the catch-all for a denied decision that carries no
	// more specific violation code.
	ViolationPolicyDenied ViolationCode = "policy_denied"
)

const (
	GrantAllow GrantDecision = "allow"
	GrantDeny  GrantDecision = "deny"
)

const (
	BackendNone                   BackendName = "none"
	BackendMacOSSeatbelt          BackendName = "macos-seatbelt"
	BackendLinuxBwrap             BackendName = "linux-bwrap"
	BackendLinuxLandlock          BackendName = "linux-landlock"
	BackendWindowsRestrictedToken BackendName = "windows-restricted-token"
	BackendWindowsElevated        BackendName = "windows-elevated"
	BackendPolicyOnly             BackendName = "policy-only"
	// BackendWSL is the policy-only fallback used under WSL when bubblewrap is
	// unavailable/unreliable: there is no native OS isolation, but network egress
	// is still routed through the local filtering proxy and the command runs under
	// the full policy engine. It is surfaced via ZERO_SANDBOX_BACKEND=wsl.
	BackendWSL BackendName = "wsl"
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

const (
	EnforcementNative   EnforcementLevel = "native"
	EnforcementDegraded EnforcementLevel = "degraded"
	EnforcementDisabled EnforcementLevel = "disabled"
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
	// EnforceToolNetwork, when true, subjects the first-party in-process network
	// tools (web_search / web_fetch) to the sandbox network policy via
	// NetworkHostAllowed. It is OFF by default: that policy exists to confine the
	// sandboxed SHELL's egress, which these tools do not use, so by default they
	// are allowed to run (keeping their own SSRF/port/redirect/redaction
	// safeguards). The sandboxed-shell egress decision is independent of this flag.
	// Turn it on to also hold web_search/web_fetch to the allow/scoped/deny policy.
	EnforceToolNetwork bool `json:"enforceToolNetwork,omitempty"`
	// BlockUnixSockets, when true on the Linux helper backend, installs a
	// best-effort seccomp filter in the inner helper stage that denies AF_UNIX
	// socket creation. It is an extra hardening layer over the native sandbox and
	// is ignored on non-Linux backends.
	BlockUnixSockets bool `json:"blockUnixSockets,omitempty"`
	// AutoAllowBashWhenSandboxed, when true, auto-allows the bash tool WITHOUT a
	// permission prompt — but only when the sandbox is actually active (a
	// native-isolation backend wraps the command). The sandbox is then the safety
	// boundary. When the sandbox is not active the flag is ignored: unsandboxed
	// bash is never auto-allowed. Off by default.
	AutoAllowBashWhenSandboxed bool `json:"autoAllowBashWhenSandboxed,omitempty"`
	// MonitorDenials, when true on macOS, tags the sandbox-exec profile's denials
	// and tails `log stream` for them so blocked operations can be surfaced back to
	// the agent. Off by default: it starts a `log stream` subprocess per command and
	// appends a <sandbox_violations> note to the command's stderr, so it is opt-in.
	// Ignored on non-macOS backends, and a no-op where the OS does not deliver
	// seatbelt denials to the unified log.
	MonitorDenials bool `json:"monitorDenials,omitempty"`
	// AllowRead/DenyRead/AllowWrite/DenyWrite are fine-grained path lists layered
	// ON TOP of the workspace + Scope guards; they never bypass the symlink /
	// out-of-workspace protections. Each entry is home-expanded, made absolute, and
	// symlink-resolved (an entry that does not exist is dropped). All default empty,
	// so an unconfigured policy behaves exactly as before. Semantics:
	//
	//   - Read: a path readable under the base workspace/Scope guard is denied if it
	//     falls under a DenyRead entry, UNLESS a more-specific AllowRead entry (one
	//     nested inside that DenyRead entry) re-includes it. AllowRead only
	//     re-includes within a DenyRead carve-out; it never extends reads beyond the
	//     workspace.
	//   - Write: DenyWrite wins over everything; otherwise a path writable under the
	//     workspace/Scope guard is allowed; otherwise an absolute path under an
	//     AllowWrite root is allowed; otherwise it is denied. AllowWrite roots are
	//     also reflected in the OS backend write binds, and on sandbox-exec DenyWrite
	//     entries are emitted as explicit deny rules so the precedence holds for
	//     shell commands too.
	AllowRead  []string `json:"allowRead,omitempty"`
	DenyRead   []string `json:"denyRead,omitempty"`
	AllowWrite []string `json:"allowWrite,omitempty"`
	DenyWrite  []string `json:"denyWrite,omitempty"`
	// InspectTLS, when true, lets the in-process scoped-egress proxy TERMINATE TLS
	// (using a locally generated, ephemeral CA) so it can enforce the per-host
	// allow/deny rules on the DECRYPTED request Host — today a CONNECT tunnel only
	// reveals the host once. OFF by default: the proxy stays a pure CONNECT
	// passthrough, byte-for-byte unchanged. SECURITY/TRUST: enabling this means the
	// sandbox can read the plaintext of the sandboxed process's TLS traffic. The
	// MITM only re-signs toward the sandboxed client; the upstream connection is
	// ALWAYS validated against the system roots (never InsecureSkipVerify), and the
	// decrypted Host still passes the SAME authorize()/domainAllowed() gate — MITM
	// widens visibility, never authority. The generated CA's public cert is written
	// to the sandbox runtime dir and surfaced via ZERO_SANDBOX_CA_CERT so the
	// sandboxed client can trust it.
	InspectTLS bool `json:"inspectTLS,omitempty"`
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
