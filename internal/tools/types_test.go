package tools

import "testing"

// SideEffectNone marks a control-only tool (no read/write/shell/network/
// out-of-workspace effect). escalate_model is its first consumer.
func TestSideEffectNoneConstant(t *testing.T) {
	if SideEffectNone != "none" {
		t.Fatalf("SideEffectNone = %q, want %q", SideEffectNone, "none")
	}
	// It must be distinct from every existing side-effect value.
	for _, other := range []SideEffect{
		SideEffectRead, SideEffectWrite, SideEffectShell, SideEffectNetwork, SideEffectLocalControl, SideEffectLocalBrowser, SideEffectLocalDesktop, SideEffectLocalTerminal, SideEffectOutOfWorkspace,
	} {
		if SideEffectNone == other {
			t.Fatalf("SideEffectNone collides with existing side effect %q", other)
		}
	}
}
