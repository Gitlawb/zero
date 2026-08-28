package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func denyCovered(denied []string, target string) bool {
	norm := normalizeProfilePath(target)
	for _, entry := range denied {
		if entry == norm || pathWithinRoot(entry, norm) {
			return true
		}
	}
	return false
}

func denyListedExact(denied []string, target string) bool {
	for _, entry := range denied {
		if entry == target {
			return true
		}
	}
	return false
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func sshGPGDenied(t *testing.T, home string, allowRead []string) []string {
	t.Helper()
	return credentialDenyReadPathsIn(credentialPathOptions{
		Homes:      []string{home},
		ConfigDirs: []string{filepath.Join(home, ".config")},
	}, allowRead).Paths
}

func sshGPGNormalizationHome() (home, sshDir string) {
	if runtime.GOOS == "windows" {
		home = `C:\Users\zero-sandbox`
	} else {
		home = "/home/zero-sandbox"
	}
	return home, filepath.Join(home, ".ssh")
}

// Option 2 of #815: deny SSH private key material and the GPG keyring, not
// the whole of ~/.ssh. git credential files from #816 must stay denied.
func TestCredentialDenyReadPathsDeniesSSHKeyMaterialNotDirectory(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	idEd := filepath.Join(sshDir, "id_ed25519")
	idPub := filepath.Join(sshDir, "id_ed25519.pub")
	config := filepath.Join(sshDir, "config")
	knownHosts := filepath.Join(sshDir, "known_hosts")
	fooPEM := filepath.Join(sshDir, "foo.pem")
	rsaPEM := filepath.Join(sshDir, "id_rsa.pem")
	secring := filepath.Join(home, ".gnupg", "secring.gpg")
	privateKey := filepath.Join(home, ".gnupg", "private-keys-v1.d", "keygrip.key")
	gitCredentials := filepath.Join(home, ".git-credentials")
	xdgCredentials := filepath.Join(home, ".config", "git", "credentials")

	// Path-based denials (id_*, *.pem, ~/.gnupg, credential stores). Empty or
	// obviously-fake bodies so scanners do not treat fixtures as live keys.
	mustWriteFile(t, idEd, "")
	mustWriteFile(t, idPub, "ssh-ed25519 AAAA public\n")
	mustWriteFile(t, config, "Host *\n")
	mustWriteFile(t, knownHosts, "github.com ssh-ed25519 AAAA\n")
	mustWriteFile(t, fooPEM, "")
	mustWriteFile(t, rsaPEM, "")
	mustWriteFile(t, secring, "fake-secring")
	mustWriteFile(t, privateKey, "fake-keygrip")
	mustWriteFile(t, gitCredentials, "https://user:token@github.com")
	mustWriteFile(t, xdgCredentials, "https://user:token@github.com")

	denied := sshGPGDenied(t, home, nil)

	if !denyCovered(denied, idEd) {
		t.Fatalf("~/.ssh/id_ed25519 is readable; deny list = %v", denied)
	}
	if denyCovered(denied, idPub) {
		t.Fatalf("~/.ssh/id_ed25519.pub was denied; public keys must stay readable")
	}
	if denyCovered(denied, config) {
		t.Fatalf("~/.ssh/config was denied; git host resolution would break")
	}
	if denyCovered(denied, knownHosts) {
		t.Fatalf("~/.ssh/known_hosts was denied; git host resolution would break")
	}
	if denyCovered(denied, sshDir) {
		t.Fatalf("~/.ssh was denied wholesale; option 2 keeps the directory readable")
	}
	if !denyCovered(denied, fooPEM) {
		t.Fatalf("~/.ssh/foo.pem is readable; deny list = %v", denied)
	}
	if !denyCovered(denied, rsaPEM) {
		t.Fatalf("~/.ssh/id_rsa.pem is readable; deny list = %v", denied)
	}
	if !denyCovered(denied, secring) {
		t.Fatalf("~/.gnupg/secring.gpg is readable; deny list = %v", denied)
	}
	if !denyCovered(denied, privateKey) {
		t.Fatalf("~/.gnupg/private-keys-v1.d file is readable; deny list = %v", denied)
	}
	if !denyCovered(denied, gitCredentials) {
		t.Fatalf("~/.git-credentials is readable after #815 SSH work; deny list = %v", denied)
	}
	if !denyCovered(denied, xdgCredentials) {
		t.Fatalf("~/.config/git/credentials is readable after #815 SSH work; deny list = %v", denied)
	}
}

func TestCredentialDenyReadPathsDeniesSSHConfigIdentityFileOutsideSSH(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	workKey := filepath.Join(home, "keys", "work_ed25519")
	mustWriteFile(t, workKey, "")
	mustWriteFile(t, workKey+".pub", "ssh-ed25519 AAAA work\n")
	mustWriteFile(t, filepath.Join(sshDir, "config"), `Host work
    IdentityFile ~/keys/work_ed25519
    CertificateFile ~/keys/work_ed25519.pub
    UserKnownHostsFile ~/.ssh/known_hosts
`)
	mustWriteFile(t, filepath.Join(sshDir, "known_hosts"), "example.com ssh-ed25519 AAAA\n")

	denied := sshGPGDenied(t, home, nil)
	if !denyCovered(denied, workKey) {
		t.Fatalf("IdentityFile ~/keys/work_ed25519 is readable; deny list = %v", denied)
	}
	if denyCovered(denied, filepath.Join(sshDir, "known_hosts")) {
		t.Fatalf("known_hosts was denied because a path-valued directive pointed at it")
	}
	if denyCovered(denied, workKey+".pub") {
		t.Fatalf("CertificateFile *.pub was denied; option 2 keeps public keys readable")
	}
	if denyCovered(denied, filepath.Join(sshDir, "config")) {
		t.Fatalf("~/.ssh/config was denied")
	}
}

func TestCredentialDenyReadPathsDeniesSSHConfigIdentityFilePercentD(t *testing.T) {
	home := t.TempDir()
	workKey := filepath.Join(home, "keys", "work_ed25519")
	mustWriteFile(t, workKey, "")
	mustWriteFile(t, filepath.Join(home, ".ssh", "config"), "IdentityFile %d/keys/work_ed25519\n")

	denied := sshGPGDenied(t, home, nil)
	if !denyCovered(denied, workKey) {
		t.Fatalf("IdentityFile %%d/keys/work_ed25519 is readable; deny list = %v", denied)
	}
	if denyCovered(denied, filepath.Join(home, ".ssh")) {
		t.Fatalf("~/.ssh was denied wholesale")
	}
}

func TestCredentialDenyReadPathsFollowsSSHConfigIncludeAndStopsCycles(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	includedKey := filepath.Join(home, "keys", "included_ed25519")
	cycleKey := filepath.Join(home, "keys", "cycle_ed25519")
	mustWriteFile(t, includedKey, "")
	mustWriteFile(t, cycleKey, "")
	mustWriteFile(t, filepath.Join(sshDir, "config"), "Include extra_config\nInclude cycle_a\nInclude missing_include\n")
	mustWriteFile(t, filepath.Join(sshDir, "extra_config"), "IdentityFile ~/keys/included_ed25519\n")
	mustWriteFile(t, filepath.Join(sshDir, "cycle_a"), "Include cycle_b\n")
	mustWriteFile(t, filepath.Join(sshDir, "cycle_b"), "Include cycle_a\nIdentityFile ~/keys/cycle_ed25519\n")

	denied := sshGPGDenied(t, home, nil)
	if !denyCovered(denied, includedKey) {
		t.Fatalf("Include IdentityFile is readable; deny list = %v", denied)
	}
	if !denyCovered(denied, cycleKey) {
		t.Fatalf("cyclic Include IdentityFile is readable; deny list = %v", denied)
	}
}

func TestSSHKeyDenyYieldsToExplicitAllowRead(t *testing.T) {
	home := t.TempDir()
	idEd := filepath.Join(home, ".ssh", "id_ed25519")
	mustWriteFile(t, idEd, "")
	target := normalizeProfilePath(idEd)
	listed := func(entries []string) bool {
		for _, entry := range entries {
			if entry == target {
				return true
			}
		}
		return false
	}

	if !listed(sshGPGDenied(t, home, nil)) {
		t.Fatalf("~/.ssh/id_ed25519 is not denied without an allowRead; nothing for the grant to override")
	}
	if listed(sshGPGDenied(t, home, []string{home})) {
		t.Fatalf("explicit allowRead %q did not re-include the SSH private key", home)
	}
}

// Path-sensitive SSH/GPG handling needs a non-Linux case (or a hermetic fake
// of the same normalization). Token expansion is GOOS-independent, so a
// Windows home spelling exercises %d without touching the host filesystem.
func TestExpandSSHConfigPathTokensWindowsStyleHome(t *testing.T) {
	home := `C:\Users\zero-sandbox`
	got, ok := expandSSHConfigPathTokens(`%d\keys\work_ed25519`, home)
	if !ok {
		t.Fatal("supported %d token was rejected")
	}
	want := `C:\Users\zero-sandbox\keys\work_ed25519`
	if got != want {
		t.Fatalf("Windows-style %%d expansion = %q, want %q", got, want)
	}
	got, ok = expandSSHConfigPathTokens("%d/keys/work_ed25519", home)
	if !ok {
		t.Fatal("supported %d token with slash was rejected")
	}
	want = `C:\Users\zero-sandbox/keys/work_ed25519`
	if got != want {
		t.Fatalf("Windows-style %%d with slash = %q, want %q", got, want)
	}
	if _, ok := expandSSHConfigPathTokens("%h/keys/work_ed25519", home); ok {
		t.Fatal("unsupported %h token must be rejected")
	}
	got, ok = expandSSHConfigPathTokens("id%%ed25519", home)
	if !ok || got != "id%ed25519" {
		t.Fatalf("%% -> %% expansion = %q ok=%v, want %q", got, ok, "id%ed25519")
	}
}

func TestExpandSSHConfigPathPercentDUsesSuppliedHome(t *testing.T) {
	home, sshDir := sshGPGNormalizationHome()
	got := expandSSHConfigPath("%d/keys/work_ed25519", home, sshDir)
	want := filepath.Join(home, "keys", "work_ed25519")
	if got != want {
		t.Fatalf("expandSSHConfigPath(%%d) = %q, want %q", got, want)
	}
	if expandSSHConfigPath("%h/keys/work_ed25519", home, sshDir) != "" {
		t.Fatalf("unsupported %%h token must be dropped")
	}
	if expandSSHConfigPath("%d/%h/keys", home, sshDir) != "" {
		t.Fatalf("remaining unsupported token after %%d must be dropped")
	}
	got = expandSSHConfigPath("id%%ed25519", home, sshDir)
	want = filepath.Join(sshDir, "id%ed25519")
	if got != want {
		t.Fatalf("literal %% expansion = %q, want %q", got, want)
	}
	if sshShouldDenyReferencedPath(sshDir, home, sshDir) {
		t.Fatalf("~/.ssh was denied wholesale under the fake home")
	}
	idEd := filepath.Join(sshDir, "id_ed25519")
	if !sshShouldDenyReferencedPath(idEd, home, sshDir) {
		t.Fatalf("well-known SSH key under fake home was not a deny candidate")
	}
	gnupg := filepath.Join(home, ".gnupg")
	gitCredentials := filepath.Join(home, ".git-credentials")
	if filepath.Base(gnupg) != ".gnupg" || filepath.Base(gitCredentials) != ".git-credentials" {
		t.Fatalf("GPG/git credential join lost the host separator; gnupg=%q git=%q", gnupg, gitCredentials)
	}
}

func TestCredentialDenyReadPathsKeepsLexicalSymlinkCandidates(t *testing.T) {
	home := t.TempDir()
	realDir := t.TempDir()

	gnupgLink := filepath.Join(home, ".gnupg")
	gnupgTarget := filepath.Join(realDir, "gnupg-store")
	if err := os.MkdirAll(gnupgTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	mustSymlink(t, gnupgTarget, gnupgLink)

	gitLink := filepath.Join(home, ".git-credentials")
	gitTarget := filepath.Join(realDir, "git-credentials")
	mustWriteFile(t, gitTarget, "x")
	mustSymlink(t, gitTarget, gitLink)

	sshLink := filepath.Join(home, ".ssh", "id_ed25519")
	sshTarget := filepath.Join(realDir, "id_ed25519")
	mustWriteFile(t, sshTarget, "")
	mustSymlink(t, sshTarget, sshLink)

	denied := sshGPGDenied(t, home, nil)
	for _, candidate := range []string{gnupgLink, gitLink, sshLink} {
		lexical := normalizeProfilePathLexically(candidate)
		if !denyListedExact(denied, lexical) {
			t.Fatalf("lexical candidate %q missing from deny list %v", lexical, denied)
		}
		resolved := normalizeProfilePath(candidate)
		if resolved != "" && resolved != lexical && !denyListedExact(denied, resolved) {
			t.Fatalf("resolved target %q missing from deny list %v", resolved, denied)
		}
	}
	if denyCovered(denied, filepath.Join(home, ".ssh")) {
		t.Fatalf("~/.ssh was denied wholesale")
	}
}
