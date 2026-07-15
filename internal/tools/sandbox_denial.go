package tools

import (
	"strings"

	zeroSandbox "github.com/Gitlawb/zero/internal/sandbox"
)

var sandboxDeniedKeywords = []string{
	"operation not permitted",
	"permission denied",
	"read-only file system",
	"seccomp",
	"sandbox",
	"landlock",
	"failed to write file",
}

var sandboxNetworkDeniedKeywords = []string{
	"cannot open a network socket",
	"cannot open netlink socket",
	"uv_interface_addresses",
	"listen eperm",
	"getaddrinfo eai_again",
	"network is unreachable",
}

func markLikelySandboxDenial(meta map[string]string, plan zeroSandbox.CommandPlan, exitCode int, sections ...string) {
	kind, keyword := sandboxDenialKind(plan, exitCode, sections...)
	if meta == nil || kind == "" {
		return
	}
	meta[SandboxLikelyDeniedMeta] = "true"
	meta[SandboxDenialKindMeta] = kind
	meta[SandboxDenialReasonMeta] = "sandbox blocked command execution"
	if keyword != "" {
		meta[SandboxDenialKeywordMeta] = keyword
	}
}

func likelySandboxDenied(plan zeroSandbox.CommandPlan, exitCode int, sections ...string) bool {
	kind, _ := sandboxDenialKind(plan, exitCode, sections...)
	return kind != ""
}

func sandboxDenialKind(plan zeroSandbox.CommandPlan, exitCode int, sections ...string) (string, string) {
	if !plan.Wrapped {
		return "", ""
	}
	if networkDeniedBySandbox(plan) {
		if keyword := sandboxNetworkDenialKeyword(sections...); keyword != "" {
			return SandboxDenialKindNetwork, keyword
		}
	}
	if exitCode == 0 {
		return "", ""
	}
	if sandboxDenialKeyword(sections...) != "" {
		if networkDeniedBySandbox(plan) {
			if keyword := sandboxNetworkDenialKeyword(sections...); keyword != "" {
				return SandboxDenialKindNetwork, keyword
			}
		}
		return SandboxDenialKindSandbox, sandboxDenialKeyword(sections...)
	}
	if plan.TargetBackend == zeroSandbox.BackendLinuxBwrap && exitCode == 128+31 {
		return SandboxDenialKindSandbox, "seccomp"
	}
	// Windows restricted-token failures are often SILENT: a spawn the token
	// cannot satisfy (executable or DLL unreadable under the restricted-SID
	// check, MSYS runtime init death, loader status 0xC0000135/0xC0000142)
	// produces a nonzero exit with no output at all, so none of the keyword
	// heuristics above can ever fire. Treat "wrapped Windows command failed
	// without writing a single byte" as a likely sandbox denial so the caller
	// offers the unsandboxed-retry prompt instead of surfacing a bare
	// "command completed with no output". A command that legitimately fails
	// silently (findstr with no match, for one) costs one extra approval
	// prompt; the inverse misclassification silently strands the whole
	// session, so the trade goes to prompting.
	if windowsWrappedBackend(plan.TargetBackend) && sectionsEmpty(sections...) {
		return SandboxDenialKindSandbox, "no output from sandboxed command"
	}
	return "", ""
}

func windowsWrappedBackend(backend zeroSandbox.BackendName) bool {
	return backend == zeroSandbox.BackendWindowsRestrictedToken || backend == zeroSandbox.BackendWindowsElevated
}

func sectionsEmpty(sections ...string) bool {
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			return false
		}
	}
	return true
}

func networkDeniedBySandbox(plan zeroSandbox.CommandPlan) bool {
	return plan.PermissionProfile.Network.Mode == zeroSandbox.NetworkDeny || plan.Policy.Network == zeroSandbox.NetworkDeny
}

func sandboxNetworkDenialKeyword(sections ...string) string {
	for _, section := range sections {
		lower := strings.ToLower(section)
		for _, keyword := range sandboxNetworkDeniedKeywords {
			if strings.Contains(lower, keyword) {
				return keyword
			}
		}
	}
	return ""
}

func sandboxDenialKeyword(sections ...string) string {
	for _, section := range sections {
		lower := strings.ToLower(section)
		for _, keyword := range sandboxDeniedKeywords {
			if strings.Contains(lower, keyword) {
				return keyword
			}
		}
	}
	return ""
}
