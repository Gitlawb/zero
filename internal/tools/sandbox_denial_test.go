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
		{name: "unsandboxed command", sandboxed: false, exitCode: 1, output: "Read-only file system", want: false},
		{name: "ordinary application failure", sandboxed: true, exitCode: 1, output: "invalid package name", want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			meta := map[string]string{}
			markLikelySandboxDenial(meta, test.sandboxed, test.exitCode, test.output)
			if got := meta[SandboxLikelyDeniedMeta] == "true"; got != test.want {
				t.Fatalf("sandbox denial = %t, want %t; meta=%#v", got, test.want, meta)
			}
		})
	}
}
