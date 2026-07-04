package update

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// npmPackageName is the published package name for the npm distribution of
// zero (see package.json). scripts/postinstall.mjs downloads the native
// binary into the same directory as package.json and leaves a
// ".zero-binary-version" marker file next to it — both are reliable signals
// that a given executable came from an npm install.
const npmPackageName = "@gitlawb/zero"

// InstallMethod identifies how the running zero binary was installed.
type InstallMethod string

const (
	InstallMethodNpm        InstallMethod = "npm"
	InstallMethodStandalone InstallMethod = "standalone"
)

// DetectInstallMethod inspects the directory containing executablePath for
// npm-install markers left by scripts/postinstall.mjs.
func DetectInstallMethod(executablePath string) InstallMethod {
	dir := filepath.Dir(executablePath)
	if _, err := os.Stat(filepath.Join(dir, ".zero-binary-version")); err == nil {
		return InstallMethodNpm
	}
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return InstallMethodStandalone
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return InstallMethodStandalone
	}
	if pkg.Name == npmPackageName {
		return InstallMethodNpm
	}
	return InstallMethodStandalone
}
