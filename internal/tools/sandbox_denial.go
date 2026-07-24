package tools

import (
	"strings"

	"github.com/Gitlawb/zero/internal/execution"
)

var sandboxDenialKeywords = []string{
	"operation not permitted",
	"permission denied",
	"read-only file system",
	"seccomp",
	"sandbox",
	"landlock",
	"failed to write file",
}

// markStructuredSandboxDenial mirrors typed adapter facts into legacy metadata
// for presentation and backward-compatible session readers. Classification is
// never inferred from stdout or stderr.
func markStructuredSandboxDenial(meta map[string]string, denial execution.Denial) {
	if meta == nil {
		return
	}
	meta[SandboxLikelyDeniedMeta] = "true"
	meta[SandboxDenialKindMeta] = SandboxDenialKindSandbox
	meta[SandboxDenialReasonMeta] = denial.Reason
	meta["sandbox_denial_capability"] = string(denial.Capability.Kind)
	if denial.Capability.Scope != "" {
		meta["sandbox_denial_scope"] = denial.Capability.Scope
	}
}

func markLikelySandboxDenial(meta map[string]string, sandboxed bool, exitCode int, outputSections ...string) *execution.Denial {
	if meta == nil || !sandboxed || exitCode == 0 {
		return nil
	}
	for _, section := range outputSections {
		lower := strings.ToLower(section)
		for _, keyword := range sandboxDenialKeywords {
			if strings.Contains(lower, keyword) {
				denial := &execution.Denial{
					Capability:  execution.Capability{Kind: execution.CapabilityUnrestricted, Scope: "host"},
					Source:      execution.DenialSourcePlatformSandbox,
					Reason:      "sandbox blocked command execution",
					Recoverable: true,
					NextAction:  execution.DenialNextActionRequestApproval,
				}
				meta[SandboxLikelyDeniedMeta] = "true"
				meta[SandboxDenialKindMeta] = SandboxDenialKindSandbox
				meta[SandboxDenialReasonMeta] = denial.Reason
				meta[SandboxDenialKeywordMeta] = keyword
				return denial
			}
		}
	}
	return nil
}
