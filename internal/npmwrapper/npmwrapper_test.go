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
	// Only a postinstall hook (which downloads the prebuilt binary) is allowed.
	// Repository build scripts (build/prepare/prepack/…) must not ship in the
	// published package — the tarball has no Go source to build from.
	if pkg.Scripts["postinstall"] != "node scripts/postinstall.mjs" {
		t.Fatalf("scripts.postinstall = %q, want node scripts/postinstall.mjs", pkg.Scripts["postinstall"])
	}
	for name := range pkg.Scripts {
		if name != "postinstall" {
			t.Fatalf("package.json scripts contains %q; only a postinstall hook is allowed (no repository build scripts)", name)
		}
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
	if !strings.Contains(string(output), "No native binary found next to the npm wrapper") {
		t.Fatalf("missing-native output = %q", string(output))
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
