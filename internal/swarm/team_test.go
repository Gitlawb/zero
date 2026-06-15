package swarm

import "testing"

func TestResolveModelInherit(t *testing.T) {
	pol := Policy{Model: "orchestrator-model"}
	if got := resolveModel(pol, Definition{Model: modelInherit}); got != "orchestrator-model" {
		t.Fatalf("inherit model = %q, want orchestrator-model", got)
	}
	if got := resolveModel(pol, Definition{Model: ""}); got != "orchestrator-model" {
		t.Fatalf("empty model = %q, want orchestrator-model", got)
	}
	if got := resolveModel(pol, Definition{Model: "explicit"}); got != "explicit" {
		t.Fatalf("explicit model = %q, want explicit", got)
	}
}

func TestResolvePermissionModeNeverWidens(t *testing.T) {
	parent := Policy{PermissionMode: permissionModeAuto} // "auto"
	// A definition asking for MORE than the parent (unsafe > auto) is clamped down.
	if got := resolvePermissionMode(parent, Definition{PermissionMode: permissionModeUnsafe}); got != permissionModeAuto {
		t.Fatalf("clamp = %q, want auto (never widen)", got)
	}
	// A definition asking for LESS keeps its stricter mode (ask < auto).
	if got := resolvePermissionMode(parent, Definition{PermissionMode: permissionModeAsk}); got != permissionModeAsk {
		t.Fatalf("stricter = %q, want ask", got)
	}
	// Empty inherits the parent.
	if got := resolvePermissionMode(parent, Definition{PermissionMode: ""}); got != permissionModeAuto {
		t.Fatalf("empty = %q, want auto", got)
	}
	// An unsafe parent still honors a stricter definition (auto < unsafe).
	if got := resolvePermissionMode(Policy{PermissionMode: permissionModeUnsafe}, Definition{PermissionMode: permissionModeAuto}); got != permissionModeAuto {
		t.Fatalf("unsafe parent + auto def = %q, want auto (stricter honored)", got)
	}
}

func TestTeamAdmitAndQueue(t *testing.T) {
	tm := &Team{Name: "t", members: map[string]*Member{}, maxSize: 2}
	if !tm.admit(MemberSpec{ID: "a"}) || !tm.admit(MemberSpec{ID: "b"}) {
		t.Fatal("first two admits should launch now")
	}
	if tm.admit(MemberSpec{ID: "c"}) {
		t.Fatal("third admit should queue, not launch")
	}
	if tm.Running() != 2 || tm.QueueDepth() != 1 {
		t.Fatalf("running=%d queue=%d, want 2/1", tm.Running(), tm.QueueDepth())
	}
	// One exit drains the single queued spec (re-reserving its slot).
	next, ok := tm.onExit()
	if !ok || next.ID != "c" {
		t.Fatalf("onExit = %+v ok=%v, want spec c", next, ok)
	}
	if tm.Running() != 2 || tm.QueueDepth() != 0 {
		t.Fatalf("after drain running=%d queue=%d, want 2/0", tm.Running(), tm.QueueDepth())
	}
	// Two more exits empty the team (no queue left).
	if _, ok := tm.onExit(); ok {
		t.Fatal("onExit with empty queue should not return a spec")
	}
	if _, ok := tm.onExit(); ok {
		t.Fatal("onExit with empty queue should not return a spec")
	}
	if tm.Running() != 0 {
		t.Fatalf("running = %d, want 0", tm.Running())
	}
}

func TestIsRetryable(t *testing.T) {
	if isRetryable(nil) {
		t.Fatal("nil is not retryable")
	}
	if !isRetryable(ErrMemberTemporary) {
		t.Fatal("ErrMemberTemporary must be retryable")
	}
	if isRetryable(errPlain("permanent")) {
		t.Fatal("a plain error is not retryable")
	}
	if !isRetryable(tempErr{}) {
		t.Fatal("an error with Temporary()==true must be retryable")
	}
}

type errPlain string

func (e errPlain) Error() string { return string(e) }

type tempErr struct{}

func (tempErr) Error() string   { return "temp" }
func (tempErr) Temporary() bool { return true }
