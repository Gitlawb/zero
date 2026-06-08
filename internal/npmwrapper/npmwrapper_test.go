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
		Module  string            `json:"module"`
		Bin     map[string]string `json:"bin"`
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(bytes, &pkg); err != nil {
		t.Fatalf("Unmarshal package.json: %v", err)
	}
	if pkg.Bin["zero"] != "bin/zero.js" {
		t.Fatalf("bin.zero = %q, want bin/zero.js", pkg.Bin["zero"])
	}
	if pkg.Module != "bin/zero.js" {
		t.Fatalf("module = %q, want bin/zero.js", pkg.Module)
	}
	if len(pkg.Scripts) != 0 {
		t.Fatalf("package.json scripts = %#v, want no repository build scripts in npm package metadata", pkg.Scripts)
	}
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
