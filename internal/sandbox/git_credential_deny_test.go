package sandbox

import (
	"os"
	"path/filepath"
	"testing"
)

// These two tests come from #816. That change landed against the single-home
// credentialDenyReadPathsForEnvironment helper, which this branch replaced with
// the multi-home credentialDenyReadPathsIn; they are ported rather than merged
// so the coverage survives the refactor. They live in an internal test file
// because profile_test.go is an external (sandbox_test) package and these need
// the unexported builder.

// git's credential store holds host passwords and personal access tokens in
// cleartext, in one of two locations depending on whether the user is on the
// XDG layout. Neither was denied, so a sandboxed command could read them
// (#815).
//
// Scoped to the credential files on purpose. Denying ~/.ssh as well would stop
// a sandboxed git push over SSH from working, which is a functional trade that
// issue tracks separately; these two cost nothing, because git reads them for
// authentication rather than identity.
func TestCredentialDenyReadPathsCoversGitCredentialStores(t *testing.T) {
	home := t.TempDir()
	configHome := filepath.Join(home, ".config")
	if err := os.MkdirAll(filepath.Join(configHome, "git"), 0o700); err != nil {
		t.Fatal(err)
	}
	gitCredentials := filepath.Join(home, ".git-credentials")
	xdgCredentials := filepath.Join(configHome, "git", "credentials")
	gitConfig := filepath.Join(configHome, "git", "config")
	for _, path := range []string{gitCredentials, xdgCredentials, gitConfig} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	denied := credentialDenyReadPathsIn(credentialPathOptions{
		Homes:      []string{home},
		ConfigDirs: []string{configHome},
	}, nil).Paths
	covered := func(target string) bool {
		norm := normalizeProfilePath(target)
		for _, entry := range denied {
			if entry == norm || pathWithinRoot(entry, norm) {
				return true
			}
		}
		return false
	}

	if !covered(gitCredentials) {
		t.Fatalf("~/.git-credentials is readable; deny list = %v", denied)
	}
	if !covered(xdgCredentials) {
		t.Fatalf("~/.config/git/credentials is readable; deny list = %v", denied)
	}
	// The global git config must stay readable. userGitConfigReadPaths grants it
	// deliberately so a sandboxed git can read user.name and aliases rather than
	// failing with "unable to access", so denying the directory instead of the
	// credential file would break that on purpose-built setups.
	if covered(gitConfig) {
		t.Fatalf("~/.config/git/config was denied; a sandboxed git loses its identity config")
	}
}

// An explicit read grant still wins, matching how every other entry behaves: a
// user who deliberately scopes a workspace over one of these paths is not
// overridden by the default deny.
//
// Asserted from both sides on purpose. Checking only that the path is absent
// under an allowRead is satisfied just as well by a deny rule that was never
// added, so the run without allowRead has to establish that there is something
// there for the grant to override.
func TestGitCredentialDenyYieldsToExplicitAllowRead(t *testing.T) {
	home := t.TempDir()
	gitCredentials := filepath.Join(home, ".git-credentials")
	if err := os.WriteFile(gitCredentials, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := normalizeProfilePath(gitCredentials)
	listed := func(entries []string) bool {
		for _, entry := range entries {
			if entry == target {
				return true
			}
		}
		return false
	}

	if !listed(credentialDenyReadPathsIn(credentialPathOptions{Homes: []string{home}}, nil).Paths) {
		t.Fatalf("~/.git-credentials is not denied without an allowRead; nothing for the grant to override")
	}
	if listed(credentialDenyReadPathsIn(credentialPathOptions{Homes: []string{home}}, []string{home}).Paths) {
		t.Fatalf("explicit allowRead %q did not re-include the credential store", home)
	}
}
