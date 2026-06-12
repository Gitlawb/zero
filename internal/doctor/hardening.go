package doctor

import (
	"fmt"
	"os/exec"
	"runtime"

	"github.com/Gitlawb/zero/internal/lsp"
	"github.com/Gitlawb/zero/internal/sandbox"
)

// sandboxBackendCheck reports whether a native sandbox backend (bubblewrap on
// Linux, sandbox-exec on macOS) is available. A missing backend is a WARN, not a
// FAIL: ZERO still evaluates its policy engine before every tool call, it just
// loses native process isolation. The remedy line is the fix — the platform's
// install/enable command — not a restatement of the diagnosis.
func sandboxBackendCheck(goos string, lookup func(string) (string, error)) Check {
	if goos == "" {
		goos = runtime.GOOS
	}
	if lookup == nil {
		lookup = exec.LookPath
	}
	backend := sandbox.SelectBackend(sandbox.BackendOptions{GOOS: goos, LookupExecutable: lookup})
	if backend.Available {
		return check("sandbox.backend", "Sandbox backend", StatusPass, fmt.Sprintf("Native sandbox backend %s is available.", backend.Name), map[string]any{
			"backend":  string(backend.Name),
			"platform": goos,
		})
	}
	remedy := sandboxRemedy(goos)
	return check("sandbox.backend", "Sandbox backend", StatusWarn, fmt.Sprintf("No native sandbox backend on %s; ZERO falls back to policy-only preflight checks.", goos), map[string]any{
		"backend":  string(backend.Name),
		"platform": goos,
		"remedy":   remedy,
	})
}

// sandboxRemedy returns the platform-specific, actionable command to obtain a
// native sandbox backend.
func sandboxRemedy(goos string) string {
	switch goos {
	case "linux":
		return "install bubblewrap (e.g. `apt-get install bubblewrap` or `dnf install bubblewrap`) so `bwrap` is on PATH"
	case "darwin":
		return "sandbox-exec ships with macOS; ensure /usr/bin is on PATH so `sandbox-exec` resolves"
	case "windows":
		return "native sandboxing is not yet available on Windows; run inside WSL2 or a Linux container for native isolation"
	default:
		return "no native sandbox adapter exists for " + goos + "; run inside Linux (bubblewrap) or macOS (sandbox-exec) for native isolation"
	}
}

// lspServersCheck reports which language servers ZERO would use are present on
// PATH. Missing servers are not a failure — ZERO degrades to text-only edits for
// those languages — so the worst status is WARN, and each missing server gets an
// actionable install command keyed by its binary name.
func lspServersCheck(lookup func(string) (string, error)) Check {
	if lookup == nil {
		lookup = exec.LookPath
	}
	present := map[string]any{}
	missing := map[string]any{}
	for _, binary := range lsp.ServerBinaries() {
		if _, err := lookup(binary); err == nil {
			present[binary] = "on PATH"
			continue
		}
		missing[binary] = lspRemedy(binary)
	}
	if len(missing) == 0 {
		return check("lsp.servers", "LSP servers", StatusPass, "All known language servers are available on PATH.", map[string]any{
			"present": present,
		})
	}
	return check("lsp.servers", "LSP servers", StatusWarn, fmt.Sprintf("%d language server(s) missing from PATH; affected files degrade to text-only edits.", len(missing)), map[string]any{
		"present": present,
		"missing": missing,
	})
}

// lspRemedy returns an actionable install command for a missing language-server
// binary. It is provider/tooling neutral and names the ecosystem's standard
// installer.
func lspRemedy(binary string) string {
	switch binary {
	case "gopls":
		return "install with `go install golang.org/x/tools/gopls@latest` (ensure $GOBIN is on PATH)"
	case "typescript-language-server":
		return "install with `npm install -g typescript typescript-language-server`"
	case "pyright-langserver":
		return "install with `npm install -g pyright` (or `pipx install pyright`)"
	case "rust-analyzer":
		return "install with `rustup component add rust-analyzer`"
	default:
		return "install the " + binary + " language server and ensure it is on PATH"
	}
}
