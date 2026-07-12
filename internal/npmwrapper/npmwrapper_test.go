package npmwrapper

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPackageBinPointsToNodeWrapper(t *testing.T) {
	root := repoRoot(t)
	bytes, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatalf("ReadFile package.json: %v", err)
	}
	var pkg struct {
		Name       string            `json:"name"`
		Module     string            `json:"module"`
		Bin        map[string]string `json:"bin"`
		Scripts    map[string]string `json:"scripts"`
		License    string            `json:"license"`
		Files      []string          `json:"files"`
		Deps       map[string]string `json:"dependencies"`
		Repository json.RawMessage   `json:"repository"`
		Engines    map[string]string `json:"engines"`
	}
	if err := json.Unmarshal(bytes, &pkg); err != nil {
		t.Fatalf("Unmarshal package.json: %v", err)
	}
	if pkg.Name != "@gitlawb/zero" {
		t.Fatalf("name = %q, want @gitlawb/zero", pkg.Name)
	}
	if pkg.Bin["zero"] != "bin/zero.js" {
		t.Fatalf("bin.zero = %q, want bin/zero.js", pkg.Bin["zero"])
	}
	if pkg.Module != "bin/zero.js" {
		t.Fatalf("module = %q, want bin/zero.js", pkg.Module)
	}
	// The published package must carry NO lifecycle scripts at all: the native
	// binary arrives as a platform optionalDependency, with scripts/postinstall.mjs
	// kept only as a first-run fallback the wrapper invokes itself. Any script
	// here would resurrect the npm/Bun/pnpm install-script warnings the platform
	//-package model exists to eliminate (see docs/NPM_PACKAGING.md).
	for name := range pkg.Scripts {
		t.Fatalf("package.json scripts contains %q; the published package must have no lifecycle scripts", name)
	}
	if pkg.License == "" {
		t.Fatalf("package.json license is empty; set it (ties to the pending LICENSE file) so npm publish is not unlicensed")
	}
	if len(pkg.Repository) == 0 {
		t.Fatalf("package.json repository is missing; npm needs it for provenance")
	}
	if pkg.Engines["node"] == "" {
		t.Fatalf("package.json engines.node is empty; the wrapper and installer require a modern Node")
	}
	for _, name := range []string{"agent-browser", "tuistory"} {
		if pkg.Deps[name] == "" {
			t.Fatalf("package.json dependencies is missing %q", name)
		}
	}
	wantFiles := map[string]bool{"bin/zero.js": false, "scripts/postinstall.mjs": false}
	for _, f := range pkg.Files {
		if _, ok := wantFiles[f]; ok {
			wantFiles[f] = true
		}
	}
	for f, present := range wantFiles {
		if !present {
			t.Fatalf("package.json files is missing %q; it would not be published in the tarball", f)
		}
	}
}

func TestPostinstallComputesAssetPlan(t *testing.T) {
	version := packageVersion(t)
	cases := []struct {
		platform, arch        string
		wantAsset, wantBinary string
	}{
		{"linux", "x64", "zero-v" + version + "-linux-x64.tar.gz", "zero"},
		{"darwin", "arm64", "zero-v" + version + "-macos-arm64.tar.gz", "zero"},
		{"win32", "x64", "zero-v" + version + "-windows-x64.zip", "zero.exe"},
	}
	for _, tc := range cases {
		stdout, stderr, err := runPostinstall(t,
			"ZERO_INSTALL_DRY_RUN=1",
			"ZERO_INSTALL_PLATFORM="+tc.platform,
			"ZERO_INSTALL_ARCH="+tc.arch,
		)
		if err != nil {
			t.Fatalf("%s/%s: dry-run err=%v stderr=%s", tc.platform, tc.arch, err, stderr)
		}
		var plan struct {
			AssetName  string `json:"assetName"`
			AssetURL   string `json:"assetUrl"`
			BinaryName string `json:"binaryName"`
			Tag        string `json:"tag"`
		}
		if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
			t.Fatalf("%s/%s: parse plan %q: %v", tc.platform, tc.arch, stdout, err)
		}
		if plan.AssetName != tc.wantAsset {
			t.Fatalf("%s/%s: assetName=%q want %q", tc.platform, tc.arch, plan.AssetName, tc.wantAsset)
		}
		if plan.BinaryName != tc.wantBinary {
			t.Fatalf("%s/%s: binaryName=%q want %q", tc.platform, tc.arch, plan.BinaryName, tc.wantBinary)
		}
		wantURL := "https://github.com/Gitlawb/zero/releases/download/v" + version + "/" + tc.wantAsset
		if plan.AssetURL != wantURL {
			t.Fatalf("%s/%s: assetUrl=%q want %q", tc.platform, tc.arch, plan.AssetURL, wantURL)
		}
		if plan.Tag != "v"+version {
			t.Fatalf("%s/%s: tag=%q want v%s", tc.platform, tc.arch, plan.Tag, version)
		}
	}
}

func TestPostinstallSkipsUnsupportedPlatform(t *testing.T) {
	stdout, stderr, err := runPostinstall(t,
		"ZERO_INSTALL_DRY_RUN=1",
		"ZERO_INSTALL_PLATFORM=plan9",
		"ZERO_INSTALL_ARCH=x64",
	)
	if err != nil {
		t.Fatalf("unsupported platform should exit 0, got err=%v stderr=%s", err, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("unsupported platform should not print a plan, got %q", stdout)
	}
	if !strings.Contains(stderr, "no prebuilt binary") {
		t.Fatalf("stderr=%q, want it to mention no prebuilt binary", stderr)
	}
}

func TestPostinstallSkipsWindowsArm64(t *testing.T) {
	// (win32, arm64) resolves to a valid platform/arch but the release matrix has
	// no windows-arm64 artifact, so the install must skip gracefully (exit 0)
	// rather than hard-fail on a 404 download.
	stdout, stderr, err := runPostinstall(t,
		"ZERO_INSTALL_DRY_RUN=1",
		"ZERO_INSTALL_PLATFORM=win32",
		"ZERO_INSTALL_ARCH=arm64",
	)
	if err != nil {
		t.Fatalf("windows-arm64 should exit 0, got err=%v stderr=%s", err, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("windows-arm64 should not print a plan, got %q", stdout)
	}
	if !strings.Contains(stderr, "no prebuilt binary for windows-arm64") {
		t.Fatalf("stderr=%q, want the windows-arm64 skip message", stderr)
	}
}

func TestPostinstallHonorsSkipEnv(t *testing.T) {
	stdout, stderr, err := runPostinstall(t, "ZERO_SKIP_DOWNLOAD=1")
	if err != nil {
		t.Fatalf("ZERO_SKIP_DOWNLOAD should exit 0, got err=%v stderr=%s", err, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		t.Fatalf("skip should print nothing to stdout, got %q", stdout)
	}
	if !strings.Contains(stderr, "skipping native binary download") {
		t.Fatalf("stderr=%q, want skip message", stderr)
	}
}

func runPostinstall(t *testing.T, env ...string) (string, string, error) {
	t.Helper()
	node := requireNode(t)
	root := repoRoot(t)
	script := filepath.Join(root, "scripts", "postinstall.mjs")
	ctx, cancel := context.WithTimeout(context.Background(), nodeWrapperTimeout())
	defer cancel()
	command := exec.CommandContext(ctx, node, script)
	command.Env = append(append(os.Environ(), "NODE_OPTIONS="), env...)
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	if ctx.Err() != nil {
		t.Fatalf("postinstall timed out: %v; stderr: %s", ctx.Err(), stderr.String())
	}
	return stdout.String(), stderr.String(), runErr
}

func packageVersion(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		t.Fatalf("ReadFile package.json: %v", err)
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		t.Fatalf("Unmarshal package.json: %v", err)
	}
	if pkg.Version == "" {
		t.Fatal("package.json version is empty")
	}
	return pkg.Version
}

func TestNodeWrapperIsExecutableAndDoesNotImportBun(t *testing.T) {
	root := repoRoot(t)
	wrapperPath := filepath.Join(root, "bin", "zero.js")
	bytes, err := os.ReadFile(wrapperPath)
	if err != nil {
		t.Fatalf("ReadFile wrapper: %v", err)
	}
	source := string(bytes)
	firstLine := strings.TrimSuffix(strings.SplitN(source, "\n", 2)[0], "\r")
	if firstLine != "#!/usr/bin/env node" {
		t.Fatalf("wrapper shebang = %q, want node", firstLine)
	}
	for _, forbidden := range []string{"#!/usr/bin/env bun", "Bun.", "../scripts/npm-wrapper", "bun run build"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("wrapper still contains %q", forbidden)
		}
	}
	info, err := os.Stat(wrapperPath)
	if err != nil {
		t.Fatalf("Stat wrapper: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		t.Fatalf("wrapper mode = %v, want executable bit", info.Mode())
	}
}

func TestNodeWrapperReportsMissingNativeBinary(t *testing.T) {
	node := requireNode(t)
	wrapperPath := copyWrapperFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), nodeWrapperTimeout())
	defer cancel()
	command := nodeWrapperCommand(ctx, node, wrapperPath, "--version")
	command.Env = append(withoutEnvKey(command.Env, "ZERO_LOCAL_CONTROL_HELPERS"), "ZERO_LOCAL_CONTROL_HELPERS=")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("wrapper timed out reporting missing native binary: %v; output: %s", ctx.Err(), output)
	}
	if err == nil {
		t.Fatalf("wrapper exited successfully without native binary: %s", output)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("wrapper err = %v, want exit 1; output: %s", err, output)
	}
	if !strings.Contains(string(output), "No native binary is available for this install") {
		t.Fatalf("missing-native output = %q", string(output))
	}
}

func TestNodeWrapperPrefersPlatformPackageBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock executable fixture uses a POSIX shell script")
	}
	node := requireNode(t)
	wrapperPath := copyWrapperFixture(t)
	root := filepath.Dir(filepath.Dir(wrapperPath))

	// A stale downloaded binary next to the wrapper must lose to the platform
	// package: the platform version is pinned to the wrapper release, the
	// downloaded copy is whatever a previous fallback fetched.
	if err := os.WriteFile(filepath.Join(root, "zero"), []byte("#!/usr/bin/env sh\nprintf 'downloaded-zero\\n'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile downloaded fixture: %v", err)
	}

	platformDir := filepath.Join(root, "node_modules", "@gitlawb", "zero-"+nodePlatformName()+"-"+nodeArchName())
	if err := os.MkdirAll(platformDir, 0o755); err != nil {
		t.Fatalf("MkdirAll platform package: %v", err)
	}
	if err := os.WriteFile(filepath.Join(platformDir, "package.json"), []byte(`{"name":"@gitlawb/zero"}`), 0o644); err != nil {
		t.Fatalf("WriteFile platform package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(platformDir, "zero"), []byte("#!/usr/bin/env sh\nprintf 'platform-zero'; for arg in \"$@\"; do printf ' %s' \"$arg\"; done; printf '\\n'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile platform binary: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), nodeWrapperTimeout())
	defer cancel()
	command := nodeWrapperCommand(ctx, node, wrapperPath, "--version")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("wrapper timed out launching platform binary: %v; output: %s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("wrapper returned error: %v; output: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "platform-zero --version" {
		t.Fatalf("wrapper output = %q, want platform-zero --version", got)
	}
}

func TestNodeWrapperFallsBackToDownloadedBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock executable fixture uses a POSIX shell script")
	}
	node := requireNode(t)
	wrapperPath := copyWrapperFixture(t)
	root := filepath.Dir(filepath.Dir(wrapperPath))
	if err := os.WriteFile(filepath.Join(root, "zero"), []byte("#!/usr/bin/env sh\nprintf 'downloaded-zero\\n'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile downloaded fixture: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), nodeWrapperTimeout())
	defer cancel()
	command := nodeWrapperCommand(ctx, node, wrapperPath, "--version")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("wrapper timed out launching downloaded binary: %v; output: %s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("wrapper returned error: %v; output: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "downloaded-zero" {
		t.Fatalf("wrapper output = %q, want downloaded-zero", got)
	}
}

func TestNodeWrapperRunsFirstRunDownloaderWhenBinaryMissing(t *testing.T) {
	node := requireNode(t)
	wrapperPath := copyWrapperFixture(t)
	root := filepath.Dir(filepath.Dir(wrapperPath))

	// Give the fixture the real downloader so the wrapper's first-run path
	// executes it; ZERO_SKIP_DOWNLOAD keeps the test offline, so the download
	// "succeeds" without producing a binary and the wrapper must exit 1 with
	// guidance.
	scriptsDir := filepath.Join(root, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll scripts: %v", err)
	}
	postinstall, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "postinstall.mjs"))
	if err != nil {
		t.Fatalf("ReadFile postinstall: %v", err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "postinstall.mjs"), postinstall, 0o644); err != nil {
		t.Fatalf("WriteFile postinstall fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"type":"module","name":"@gitlawb/zero","version":"0.0.0"}`), 0o644); err != nil {
		t.Fatalf("WriteFile package fixture: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), nodeWrapperTimeout())
	defer cancel()
	command := nodeWrapperCommand(ctx, node, wrapperPath, "--version")
	command.Env = append(command.Env, "ZERO_SKIP_DOWNLOAD=1")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("wrapper timed out on first-run download path: %v; output: %s", ctx.Err(), output)
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("wrapper err = %v, want exit 1; output: %s", err, output)
	}
	text := string(output)
	if !strings.Contains(text, "fetching the native binary from the GitHub Release") {
		t.Fatalf("output missing fallback download notice: %q", text)
	}
	if !strings.Contains(text, "skipping native binary download") {
		t.Fatalf("output shows the downloader did not run: %q", text)
	}
	if !strings.Contains(text, "No native binary is available for this install") {
		t.Fatalf("output missing guidance: %q", text)
	}
}

func nodePlatformName() string {
	switch runtime.GOOS {
	case "darwin":
		return "darwin"
	case "windows":
		return "win32"
	default:
		return runtime.GOOS
	}
}

func nodeArchName() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	default:
		return runtime.GOARCH
	}
}

func TestNodeWrapperLaunchesNativeBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock executable fixture uses a POSIX shell script")
	}
	node := requireNode(t)
	wrapperPath := copyWrapperFixture(t)
	root := filepath.Dir(filepath.Dir(wrapperPath))
	nativePath := filepath.Join(root, "zero")
	if err := os.WriteFile(nativePath, []byte("#!/usr/bin/env sh\nprintf 'mock-zero'; for arg in \"$@\"; do printf ' %s' \"$arg\"; done; printf '\\n'\n"), 0o755); err != nil {
		t.Fatalf("WriteFile native fixture: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), nodeWrapperTimeout())
	defer cancel()
	command := nodeWrapperCommand(ctx, node, wrapperPath, "--version")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("wrapper timed out launching native binary: %v; output: %s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("wrapper returned error: %v; output: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "mock-zero --version" {
		t.Fatalf("wrapper output = %q", got)
	}
}

func TestNodeWrapperPassesLocalControlHelperManifest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock executable fixture uses a POSIX shell script")
	}
	node := requireNode(t)
	wrapperPath := copyWrapperFixture(t)
	root := filepath.Dir(filepath.Dir(wrapperPath))
	nativePath := filepath.Join(root, "zero")
	if err := os.WriteFile(nativePath, []byte("#!/usr/bin/env sh\nprintf '%s\\n' \"$ZERO_LOCAL_CONTROL_HELPERS\"\n"), 0o755); err != nil {
		t.Fatalf("WriteFile native fixture: %v", err)
	}
	binDir := filepath.Join(root, "node_modules", ".bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll node_modules/.bin: %v", err)
	}
	for _, name := range []string{"agent-browser", "tuistory"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/usr/bin/env sh\n"), 0o755); err != nil {
			t.Fatalf("WriteFile helper %s: %v", name, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), nodeWrapperTimeout())
	defer cancel()
	command := nodeWrapperCommand(ctx, node, wrapperPath, "--version")
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("wrapper timed out launching native binary: %v; output: %s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("wrapper returned error: %v; output: %s", err, output)
	}
	var manifest struct {
		Version int `json:"version"`
		Helpers map[string]struct {
			Command     string   `json:"command"`
			PrefixArgs  []string `json:"prefixArgs"`
			PathPrepend []string `json:"pathPrepend"`
		} `json:"helpers"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(output))), &manifest); err != nil {
		t.Fatalf("manifest JSON = %q: %v", output, err)
	}
	if manifest.Version != 1 {
		t.Fatalf("manifest version = %d, want 1", manifest.Version)
	}
	for _, name := range []string{"agent-browser", "tuistory"} {
		helper, ok := manifest.Helpers[name]
		if !ok {
			t.Fatalf("manifest missing helper %q: %#v", name, manifest.Helpers)
		}
		wantCommand := canonicalTestPath(t, filepath.Join(binDir, name))
		if helper.Command != wantCommand {
			t.Fatalf("%s command = %q, want %q", name, helper.Command, wantCommand)
		}
		wantBinDir := canonicalTestPath(t, binDir)
		if len(helper.PathPrepend) != 1 || helper.PathPrepend[0] != wantBinDir {
			t.Fatalf("%s pathPrepend = %#v, want [%q]", name, helper.PathPrepend, wantBinDir)
		}
	}
}

func TestNodeWrapperClearsInheritedLocalControlHelperManifestWhenNoHelpers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mock executable fixture uses a POSIX shell script")
	}
	node := requireNode(t)
	wrapperPath := copyWrapperFixture(t)
	root := filepath.Dir(filepath.Dir(wrapperPath))
	nativePath := filepath.Join(root, "zero")
	if err := os.WriteFile(nativePath, []byte("#!/usr/bin/env sh\nif [ -n \"${ZERO_LOCAL_CONTROL_HELPERS+x}\" ]; then printf 'set\\n'; else printf 'unset\\n'; fi\n"), 0o755); err != nil {
		t.Fatalf("WriteFile native fixture: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), nodeWrapperTimeout())
	defer cancel()
	command := nodeWrapperCommand(ctx, node, wrapperPath, "--version")
	command.Env = append(withoutEnvKey(command.Env, "ZERO_LOCAL_CONTROL_HELPERS"), `ZERO_LOCAL_CONTROL_HELPERS={"version":1,"helpers":{"agent-browser":{"command":"stale"}}}`)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("wrapper timed out launching native binary: %v; output: %s", ctx.Err(), output)
	}
	if err != nil {
		t.Fatalf("wrapper returned error: %v; output: %s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "unset" {
		t.Fatalf("ZERO_LOCAL_CONTROL_HELPERS state = %q, want unset", got)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks %s: %v", path, err)
	}
	return realPath
}

func withoutEnvKey(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func copyWrapperFixture(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	bytes, err := os.ReadFile(filepath.Join(root, "bin", "zero.js"))
	if err != nil {
		t.Fatalf("ReadFile wrapper: %v", err)
	}
	dir := t.TempDir()
	// Create a package.json with "type": "module" so the isolated .js fixture
	// is treated as ESM (matching how it runs when installed from the real package.json).
	// Without this, Node treats .js as CJS on all platforms, causing top-level import
	// to fail with SyntaxError before reaching the missing-binary logic.
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatalf("WriteFile package.json fixture: %v", err)
	}
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll bin: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"type":"module"}`), 0o644); err != nil {
		t.Fatalf("WriteFile package fixture: %v", err)
	}
	wrapperPath := filepath.Join(binDir, "zero.js")
	if err := os.WriteFile(wrapperPath, bytes, 0o755); err != nil {
		t.Fatalf("WriteFile wrapper fixture: %v", err)
	}
	return wrapperPath
}

func requireNode(t *testing.T) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available")
	}
	return node
}

func nodeWrapperCommand(ctx context.Context, node string, wrapperPath string, args ...string) *exec.Cmd {
	commandArgs := append([]string{wrapperPath}, args...)
	command := exec.CommandContext(ctx, node, commandArgs...)
	// Keep wrapper behavior independent from developer or runner Node settings
	// such as --inspect-brk, which would block this smoke test.
	command.Env = append(os.Environ(), "NODE_OPTIONS=")
	return command
}

func nodeWrapperTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		// Windows CI runners cold-start node slowly under load; 30s intermittently
		// timed out with empty output (a flake, not a wrapper bug — the same run
		// passes in ~2.5s on a warm runner), so give the spawn ample headroom.
		return 90 * time.Second
	}
	return 10 * time.Second
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
