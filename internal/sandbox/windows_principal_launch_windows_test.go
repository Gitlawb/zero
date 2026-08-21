//go:build windows

package sandbox

import (
	"strings"
	"testing"
)

// The launch preflight must name what is missing and what to do about it.
//
// Without it the failure surfaces as a bare "Access is denied" from inside
// CreateProcessAsUser, which is indistinguishable from the sandboxed command
// itself being rejected. It happens before the command's executable is opened,
// so there is nothing else in the output to tell the two apart.
//
// The assertion is on the shape of the answer rather than on a fixed verdict,
// because the verdict legitimately differs by machine: an unelevated developer
// or CI account holds neither privilege, while a service context may hold both.
// Pinning "always fails" would break on the machines where this is supposed to
// work, and pinning "always succeeds" would break everywhere else.
func TestPrincipalLaunchPreflightExplainsAMissingPrivilege(t *testing.T) {
	err := enableWindowsPrincipalLaunchPrivileges()
	if err == nil {
		t.Log("this process holds the principal launch privileges; nothing to explain")
		return
	}
	for _, want := range []string{
		seAssignPrimaryTokenPrivilege,
		windowsSandboxIdentityEnv,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("preflight error does not mention %s, so an operator cannot act on it: %v", want, err)
		}
	}
	// The point of the message is the way out. Without it the operator is told
	// only that something is denied.
	if !strings.Contains(err.Error(), "restricted-token sandbox") {
		t.Errorf("preflight error does not offer the fallback, so it reads as a dead end: %v", err)
	}
}

// Repeated calls must agree. This runs once per sandboxed command, and a
// preflight that answered differently on the second call would let a command
// through that the first call refused.
func TestPrincipalLaunchPreflightIsStable(t *testing.T) {
	first := enableWindowsPrincipalLaunchPrivileges()
	second := enableWindowsPrincipalLaunchPrivileges()
	if (first == nil) != (second == nil) {
		t.Fatalf("preflight verdict changed between calls: first=%v second=%v", first, second)
	}
}
