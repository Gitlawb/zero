//go:build windows

package sandbox

import (
	"os"
	"testing"
)

// Setup must stay inert unless the principal backend is explicitly opted into.
// This is the property that makes the branch safe to merge while the privileged
// paths are still being validated: without the opt-in, `zero sandbox setup`
// creates no local account and the capability-SID backend is the whole of setup.
func TestWindowsSandboxIdentityGating(t *testing.T) {
	for name, testCase := range map[string]struct {
		env  map[string]string
		want bool
	}{
		// An explicit map entry is authoritative; these cases never reach the
		// process environment.
		"empty":          {env: map[string]string{windowsSandboxIdentityEnv: ""}, want: false},
		"zero":           {env: map[string]string{windowsSandboxIdentityEnv: "0"}, want: false},
		"true not one":   {env: map[string]string{windowsSandboxIdentityEnv: "true"}, want: false},
		"one":            {env: map[string]string{windowsSandboxIdentityEnv: "1"}, want: true},
		"one with space": {env: map[string]string{windowsSandboxIdentityEnv: " 1 "}, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Pin the process variable too. Every case here supplies an explicit
			// map entry so none of them should consult it, and pinning proves
			// that rather than assuming it: without this a developer who exports
			// the opt-in would see different results from CI.
			t.Setenv(windowsSandboxIdentityEnv, "1")
			if got := windowsSandboxIdentityEnabled(testCase.env); got != testCase.want {
				t.Fatalf("enabled = %v, want %v for %q", got, testCase.want, testCase.env[windowsSandboxIdentityEnv])
			}
		})
	}
}

// With no map entry the process environment decides. That fallback is what the
// elevated setup path actually runs on — it passes no Env — so it needs its own
// coverage rather than riding on a case that also has a map entry.
func TestWindowsSandboxIdentityGatingFallsBackToTheProcessEnvironment(t *testing.T) {
	for name, testCase := range map[string]struct {
		value string
		set   bool
		want  bool
	}{
		"unset":          {set: false, want: false},
		"empty":          {value: "", set: true, want: false},
		"zero":           {value: "0", set: true, want: false},
		"one":            {value: "1", set: true, want: true},
		"one with space": {value: " 1 ", set: true, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			// t.Setenv registers the restore even when the variable is then
			// cleared, which is the only way to test a genuinely absent variable
			// without leaking that state into the rest of the package.
			t.Setenv(windowsSandboxIdentityEnv, testCase.value)
			if !testCase.set {
				if err := os.Unsetenv(windowsSandboxIdentityEnv); err != nil {
					t.Fatalf("unset %s: %v", windowsSandboxIdentityEnv, err)
				}
			}
			if got := windowsSandboxIdentityEnabled(nil); got != testCase.want {
				t.Fatalf("enabled = %v, want %v (set=%v value=%q)", got, testCase.want, testCase.set, testCase.value)
			}
		})
	}
}

// The command environment wins over the process environment, so a run can opt in
// or out without depending on how the parent shell was launched.
func TestWindowsSandboxIdentityEnvOverridesProcess(t *testing.T) {
	t.Setenv(windowsSandboxIdentityEnv, "1")
	if windowsSandboxIdentityEnabled(map[string]string{windowsSandboxIdentityEnv: "0"}) {
		t.Fatal("command env set to 0 must override a process env of 1")
	}
	if !windowsSandboxIdentityEnabled(map[string]string{}) {
		t.Fatal("with no command-env entry the process env should apply")
	}
}

// One workspace maps to one principal, and different workspaces must not share
// an account, or two projects would run under the same identity and could reach
// each other's granted roots.
func TestWindowsSandboxWorkspaceKeyIsStableAndDistinct(t *testing.T) {
	first := windowsSandboxWorkspaceKey([]string{`C:\ws\alpha`})
	again := windowsSandboxWorkspaceKey([]string{`C:\ws\alpha`})
	other := windowsSandboxWorkspaceKey([]string{`C:\ws\beta`})
	if first != again {
		t.Fatalf("key is not stable: %q vs %q", first, again)
	}
	if first == other {
		t.Fatal("two different workspaces produced the same principal key")
	}
	if first == "" {
		t.Fatal("empty key")
	}
	// An empty root list still has to yield a usable key rather than a blank one.
	if windowsSandboxWorkspaceKey(nil) == "" {
		t.Fatal("no workspace roots produced an empty key")
	}
}

// Network denial is enforced by WFP filters keyed to the offline-marker SID,
// which only the restricted token carries. A principal token would leave those
// filters matching nothing, so the principal backend must stand down whenever
// the network is denied rather than silently trading network enforcement for
// read confinement.
// The eligibility predicate is asserted rather than the token lookup, because on
// a machine with no principal provisioned the lookup declines for its own reasons
// and would report success here whether or not the guard existed.
func TestPrincipalBackendDefersToRestrictedTokenWhenNetworkDenied(t *testing.T) {
	eligible := func(mode NetworkMode, optIn string) bool {
		config := WindowsSandboxCommandConfig{
			SandboxHome:    t.TempDir(),
			WorkspaceRoots: []string{t.TempDir()},
			Env:            map[string]string{windowsSandboxIdentityEnv: optIn},
		}
		config.PermissionProfile.Network.Mode = mode
		return windowsSandboxPrincipalEligible(config)
	}

	if eligible(NetworkDeny, "1") {
		t.Fatal("principal backend eligible with the network denied; the WFP filters key on the offline-marker SID, which a logon token does not carry, so egress would be unenforced")
	}
	// The guard must be specific to denial, not a blanket disable that would make
	// the whole backend dead code.
	if !eligible(NetworkAllow, "1") {
		t.Fatal("principal backend refused with the network allowed; the guard is over-broad and disables the backend entirely")
	}
	if eligible(NetworkAllow, "0") {
		t.Fatal("principal backend eligible without the opt-in")
	}
}
