package update

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The path rule itself, checked for every target from any machine. Gating this
// on runtime.GOOS would skip it on Windows, leaving the Windows branch of the
// rule to be verified by nobody.
func TestIsHomebrewPathPerTarget(t *testing.T) {
	cases := []struct {
		goos string
		path string
		want bool
	}{
		{"darwin", "/opt/homebrew/Cellar/zero/0.7.1/bin/zero", true},
		{"darwin", "/usr/local/Cellar/zero/0.7.1/bin/zero", true},
		{"linux", "/home/linuxbrew/.linuxbrew/Cellar/zero/0.7.1/bin/zero", true},
		{"darwin", "/usr/local/bin/zero", false},
		{"darwin", "/Users/someone/.local/bin/zero", false},
		{"linux", "/opt/cellar/bin/zero", false},          // lowercase is not a keg
		{"linux", "/opt/CellarX/bin/zero", false},         // segment must match exactly
		{"windows", `C:\Cellar\zero\bin\zero.exe`, false}, // Homebrew does not run here
	}
	for _, testCase := range cases {
		if got := isHomebrewPath(testCase.goos, testCase.path); got != testCase.want {
			t.Errorf("isHomebrewPath(%q, %q) = %v, want %v", testCase.goos, testCase.path, got, testCase.want)
		}
	}
}

// A Homebrew install is a keg under <prefix>/Cellar with <prefix>/bin/zero
// linked to it. Detection has to survive both spellings, because Check passes
// the unresolved path and Apply passes the resolved one.
func TestDetectInstallMethodRecognisesAHomebrewKeg(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("DetectInstallMethod is GOOS-gated; the rule itself is covered by TestIsHomebrewPathPerTarget")
	}
	prefix := t.TempDir()
	keg := filepath.Join(prefix, "Cellar", "zero", "0.7.1", "bin")
	if err := os.MkdirAll(keg, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(keg, "zero")
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := DetectInstallMethod(binary); got != InstallMethodHomebrew {
		t.Errorf("keg path: got %q, want %q", got, InstallMethodHomebrew)
	}

	linkDir := filepath.Join(prefix, "bin")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(linkDir, "zero")
	if err := os.Symlink(binary, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	// The path Check sees. Without symlink resolution this reads as standalone
	// and the guidance tells a Homebrew user to run `zero upgrade`.
	if got := DetectInstallMethod(link); got != InstallMethodHomebrew {
		t.Errorf("linked path: got %q, want %q", got, InstallMethodHomebrew)
	}
}

// The failure that matters more than a missed detection: refusing to update an
// install Homebrew has never heard of. /usr/local is a Homebrew prefix on Intel
// macOS and an ordinary install location everywhere.
func TestDetectInstallMethodLeavesOrdinaryInstallsAlone(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "usr", "local", "bin"),
		filepath.Join(root, "home", "user", ".local", "bin"),
		filepath.Join(root, "opt", "homebrewish", "bin"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		binary := filepath.Join(dir, "zero")
		if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := DetectInstallMethod(binary); got != InstallMethodStandalone {
			t.Errorf("%s: got %q, want %q", dir, got, InstallMethodStandalone)
		}
	}
}

// An npm install must not start reading as Homebrew now that a second check
// runs first.
func TestDetectInstallMethodStillRecognisesNpm(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "zero")
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".zero-binary-version"), []byte("0.7.1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DetectInstallMethod(binary); got != InstallMethodNpm {
		t.Errorf("got %q, want %q", got, InstallMethodNpm)
	}
}

func TestUpgradeGuidanceSendsHomebrewUsersToBrew(t *testing.T) {
	guidance := upgradeGuidance(AssetCheck{}, "", InstallMethodHomebrew)
	if !strings.Contains(guidance, "brew upgrade zero") {
		t.Errorf("guidance does not name the command that works: %q", guidance)
	}
	if strings.Contains(guidance, "Run `zero upgrade`") {
		t.Errorf("guidance still offers the command that refuses: %q", guidance)
	}
}

// A custom source flag must not talk a Homebrew user back into `zero upgrade`.
func TestUpgradeGuidanceIgnoresSourceFlagForHomebrew(t *testing.T) {
	guidance := upgradeGuidance(AssetCheck{}, "--source", InstallMethodHomebrew)
	if !strings.Contains(guidance, "brew upgrade zero") {
		t.Errorf("source flag changed the Homebrew answer: %q", guidance)
	}
}
