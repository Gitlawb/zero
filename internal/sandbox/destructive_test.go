package sandbox

import (
	"context"
	"testing"
)

// TestScopedDestructivePromptsWhileCatastrophicDenies pins the split between a
// scoped delete a user can approve and the irrecoverable system-level forms that
// stay a hard block even in unsafe mode.
func TestScopedDestructivePromptsWhileCatastrophicDenies(t *testing.T) {
	engine := NewEngine(EngineOptions{WorkspaceRoot: t.TempDir(), Policy: DefaultPolicy()})

	shellReq := func(command string, mode PermissionMode) Request {
		return Request{
			ToolName:       "bash",
			SideEffect:     SideEffectShell,
			Permission:     PermissionPrompt,
			PermissionMode: mode,
			Autonomy:       AutonomyHigh,
			Args:           map[string]any{"command": command},
		}
	}

	// rm -rf <subdir> in ask mode is now a prompt the user can approve, not a hard
	// block — and it must be "destructive" but NOT "destructive_catastrophic".
	scoped := engine.Evaluate(context.Background(), shellReq("rm -rf site", PermissionModeAsk))
	if scoped.Action != ActionPrompt {
		t.Fatalf("rm -rf <subdir> in ask mode = %#v, want ActionPrompt", scoped)
	}
	if !HasRiskCategory(scoped.Risk, "destructive") || HasRiskCategory(scoped.Risk, "destructive_catastrophic") {
		t.Fatalf("rm -rf <subdir> categories = %v, want destructive (not catastrophic)", scoped.Risk.Categories)
	}

	// The same scoped delete is allowed in unsafe mode (the operator opted in).
	if d := engine.Evaluate(context.Background(), shellReq("rm -rf site", PermissionUnsafe)); d.Action != ActionAllow {
		t.Fatalf("rm -rf <subdir> in unsafe = %#v, want ActionAllow", d)
	}

	// A nested relative path is still scoped, not catastrophic.
	if d := engine.Evaluate(context.Background(), shellReq("rm -rf build/output", PermissionModeAsk)); d.Action != ActionPrompt {
		t.Fatalf("rm -rf build/output in ask = %#v, want ActionPrompt", d)
	}

	// A bare-glob delete (rm -rf *) is workspace-local: promptable in ask mode and
	// allowed once opted into via unsafe — NOT a hard-denied catastrophic command.
	// (Regression guard: it used to be over-tagged as destructive_catastrophic.)
	if d := engine.Evaluate(context.Background(), shellReq("rm -rf *", PermissionModeAsk)); d.Action != ActionPrompt {
		t.Fatalf("rm -rf * in ask = %#v, want ActionPrompt", d)
	}
	glob := engine.Evaluate(context.Background(), shellReq("rm -rf *", PermissionModeAsk))
	if !HasRiskCategory(glob.Risk, "destructive") || HasRiskCategory(glob.Risk, "destructive_catastrophic") {
		t.Fatalf("rm -rf * categories = %v, want destructive (not catastrophic)", glob.Risk.Categories)
	}
	if d := engine.Evaluate(context.Background(), shellReq("rm -rf *", PermissionUnsafe)); d.Action != ActionAllow {
		t.Fatalf("rm -rf * in unsafe = %#v, want ActionAllow", d)
	}

	// Catastrophic system-level commands stay a hard block — even in unsafe mode.
	// The path-traversal escapes (rm -rf ../..) are the regression Vasanth flagged:
	// they used to fall through to allow in unsafe mode and delete outside the
	// workspace.
	catastrophic := []string{
		"rm -rf /",
		"rm -rf $HOME",
		"rm -rf ~",
		"mkfs.ext4 /dev/sda1",
		"dd if=/dev/zero of=/dev/sda",
		":(){ :|:& };:",
		"rm -rf ../../",
		"rm -rf ../../etc",
		"rm --no-preserve-root -rf ../../",
	}
	for _, command := range catastrophic {
		d := engine.Evaluate(context.Background(), shellReq(command, PermissionUnsafe))
		if d.Action != ActionDeny || d.Violation == nil || d.Violation.Code != ViolationDestructiveCommand {
			t.Fatalf("catastrophic %q in unsafe = %#v, want destructive deny", command, d)
		}
		if !HasRiskCategory(d.Risk, "destructive_catastrophic") {
			t.Fatalf("catastrophic %q categories = %v, want destructive_catastrophic", command, d.Risk.Categories)
		}
	}
}
