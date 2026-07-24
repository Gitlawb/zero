package tools

import (
	"testing"

	"github.com/Gitlawb/zero/internal/execution"
)

func TestStructuredSandboxDenialMetadata(t *testing.T) {
	meta := map[string]string{}
	markStructuredSandboxDenial(meta, execution.Denial{
		Capability: execution.Capability{Kind: execution.CapabilityProtectedMetadata, Scope: "/workspace/.zero"},
		Reason:     "protected metadata is denied",
	})
	if meta["sandbox_denial_capability"] != string(execution.CapabilityProtectedMetadata) || meta["sandbox_denial_scope"] != "/workspace/.zero" {
		t.Fatalf("structured metadata = %#v", meta)
	}
}

func TestLikelySandboxDenialMetadataFromCommandOutput(t *testing.T) {
	for _, test := range []struct {
		name      string
		sandboxed bool
		exitCode  int
		output    string
		want      bool
	}{
		{name: "sandboxed read-only filesystem", sandboxed: true, exitCode: 1, output: "touch: Read-only file system", want: true},
		{name: "sandboxed permission denied", sandboxed: true, exitCode: 1, output: "mkdir: Permission denied", want: true},
		{name: "successful command", sandboxed: true, exitCode: 0, output: "Read-only file system", want: false},
		{name: "unsandboxed read-only command", sandboxed: false, exitCode: 1, output: "Read-only file system", want: false},
		{name: "unsandboxed permission failure", sandboxed: false, exitCode: 1, output: "mkdir: Permission denied", want: false},
		{name: "generic sandbox message", sandboxed: true, exitCode: 1, output: "application sandbox configuration is invalid", want: false},
		{name: "generic write failure", sandboxed: true, exitCode: 1, output: "failed to write file: invalid destination", want: false},
		{name: "ordinary application failure", sandboxed: true, exitCode: 1, output: "invalid package name", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := map[string]string{}
			denial := markLikelySandboxDenial(meta, test.sandboxed, test.exitCode, test.output)
			if got := denial != nil; got != test.want {
				t.Fatalf("typed sandbox denial = %t, want %t; denial=%#v", got, test.want, denial)
			}
			if test.want {
				if denial.Source != execution.DenialSourcePlatformSandbox ||
					denial.Capability.Kind != execution.CapabilityUnrestricted ||
					denial.Capability.Scope != "host" ||
					!denial.Recoverable ||
					denial.NextAction != execution.DenialNextActionRequestApproval {
					t.Fatalf("unexpected denial shape: %#v", denial)
				}
				if meta[SandboxDenialReasonMeta] != denial.Reason ||
					meta[SandboxDenialKeywordMeta] == "" {
					t.Fatalf("incomplete denial metadata: %#v", meta)
				}
			}
			if got := meta[SandboxLikelyDeniedMeta] == "true"; got != test.want {
				t.Fatalf("sandbox denial = %t, want %t; meta=%#v", got, test.want, meta)
			}
		})
	}
}
