// Package agentcli is the single source of truth for detecting installed
// third-party agent-harness CLIs (Claude Code, Codex, Gemini CLI, ...), probing
// whether each is logged in, and — where the harness's local credential store is
// understood — extracting those credentials for reuse by zero's own providers.
// The onboarding wizard, the provider factory, and the specialist harness
// launcher all build on this API, so the catalog below is deliberately encoded
// as data (one line per harness) rather than scattered logic: these third-party
// CLIs change their binary names, cred-file locations, and CLI flags over time,
// and a correction should be a one-line edit here rather than a hunt across
// packages.
//
// Login probing is heuristic, not authoritative: a hit means "this harness has
// a local credential store with something in it," not "the stored token is
// still valid." Callers that need certainty must still make a real request and
// handle auth failure.
package agentcli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// keychainTimeout bounds the `security` shell-out in readKeychain. Without it
// a stalled keychain (locked/prompting) blocks Detect indefinitely — and
// agentclicreds.go re-probes credentials on every outbound provider request,
// so an unbounded hang here would freeze real requests, not just TUI startup.
const keychainTimeout = 5 * time.Second

// LoginState reports what Detect could establish about a harness's login
// status from local, non-interactive probes.
type LoginState int

const (
	// LoginUnknown means the harness has no known credential-file or keychain
	// probe (e.g. it is purely API-key driven via environment variables), so
	// binary presence is all Detect can report.
	LoginUnknown LoginState = iota
	// LoggedOut means the harness has at least one probe, but none of them
	// found a stored credential.
	LoggedOut
	// LoggedIn means at least one probe (a credential file or keychain entry)
	// was found.
	LoggedIn
)

// Harness describes one known agent CLI: how to find its binary, how to guess
// whether it is logged in, and how to invoke it non-interactively for a single
// prompt.
type Harness struct {
	// ID is the stable slug used everywhere else in zero to refer to this
	// harness ("claude", "codex", ...). Never change an existing ID; downstream
	// config/state references it by value.
	ID string
	// DisplayName is the human-readable name shown in UI ("Claude Code").
	DisplayName string
	// Binaries are executable names tried with LookPath, in order. The first
	// one found on PATH wins.
	Binaries []string
	// Vendor is the upstream company slug ("anthropic", "openai", "google",
	// "alibaba"), or "" for harnesses that are provider-agnostic.
	Vendor string
	// CatalogID is the zero providercatalog id whose provider can run directly
	// on this CLI's extracted credentials, or "" when the harness's credentials
	// are not reusable that way (either extraction is unsupported, or the
	// harness has no single backing provider).
	CatalogID string
	// CredFiles are $HOME-relative paths whose existence implies a stored
	// login. Checked in order; any hit is enough.
	CredFiles []string
	// KeychainService is the macOS keychain generic-password service name that
	// holds this harness's credentials, or "" when it does not use the
	// keychain.
	KeychainService string
	// PrintArgs returns the argv (excluding the binary itself) for a one-shot,
	// non-interactive run of prompt. Never nil for a cataloged harness.
	PrintArgs func(prompt string) []string
	// Stream names the stdout format PrintArgs's invocation produces:
	// "claude-stream-json", "codex-json", "gemini-json", or "text".
	Stream string
	// LoginHint is the command to suggest when the harness is installed but not
	// logged in ("codex login"). Empty when the harness has no known dedicated
	// login subcommand — LoginCommand falls back to the bare binary name, which
	// is always a safe suggestion (every one of these CLIs walks the user
	// through login on first interactive run).
	LoginHint string
}

// LoginCommand returns the command to suggest a user run to authenticate this
// harness: LoginHint when known, otherwise the first binary name.
func (h Harness) LoginCommand() string {
	if hint := strings.TrimSpace(h.LoginHint); hint != "" {
		return hint
	}
	if len(h.Binaries) > 0 {
		return h.Binaries[0]
	}
	return h.ID
}

// Detection is one probed harness: where its binary resolved to, and what
// Detect could infer about its login state.
type Detection struct {
	Harness Harness
	Path    string // resolved binary path
	Login   LoginState
}

// Deps are the side-effecting operations Detect/ExtractCredentials perform,
// injectable so tests never touch a real PATH, filesystem, or keychain. Any nil
// field falls back to the real implementation.
type Deps struct {
	// LookPath resolves a binary name on PATH; nil -> exec.LookPath.
	LookPath func(string) (string, error)
	// Home is the directory CredFiles are resolved relative to; "" ->
	// os.UserHomeDir().
	Home string
	// ReadFile reads a file's contents; nil -> os.ReadFile.
	ReadFile func(string) ([]byte, error)
	// Keychain reads a macOS generic password by service name; nil -> the real
	// implementation via `/usr/bin/security find-generic-password -s <svc> -w`,
	// and even that real default is only wired up on darwin. An explicitly
	// injected Keychain always wins regardless of GOOS, so tests can exercise
	// keychain-backed harnesses portably.
	Keychain func(service string) ([]byte, error)
}

var catalog = []Harness{
	{
		ID:              "claude",
		DisplayName:     "Claude Code",
		Binaries:        []string{"claude"},
		Vendor:          "anthropic",
		CatalogID:       "anthropic",
		CredFiles:       []string{".claude/.credentials.json"},
		KeychainService: "Claude Code-credentials",
		PrintArgs: func(prompt string) []string {
			return []string{"-p", prompt, "--output-format", "stream-json", "--verbose"}
		},
		Stream: "claude-stream-json",
	},
	{
		ID:          "codex",
		DisplayName: "OpenAI Codex CLI",
		Binaries:    []string{"codex"},
		Vendor:      "openai",
		CatalogID:   "chatgpt",
		CredFiles:   []string{".codex/auth.json"},
		PrintArgs: func(prompt string) []string {
			return []string{"exec", "--json", prompt}
		},
		Stream:    "codex-json",
		LoginHint: "codex login",
	},
	{
		ID:          "gemini",
		DisplayName: "Gemini CLI",
		Binaries:    []string{"gemini"},
		Vendor:      "google",
		CredFiles:   []string{".gemini/oauth_creds.json"},
		PrintArgs: func(prompt string) []string {
			return []string{"-p", prompt}
		},
		Stream: "text",
	},
	{
		ID:          "qwen",
		DisplayName: "Qwen Code",
		Binaries:    []string{"qwen"},
		Vendor:      "alibaba",
		CredFiles:   []string{".qwen/oauth_creds.json"},
		PrintArgs: func(prompt string) []string {
			return []string{"-p", prompt}
		},
		Stream: "text",
	},
	{
		ID:          "opencode",
		DisplayName: "OpenCode",
		Binaries:    []string{"opencode"},
		CredFiles:   []string{".local/share/opencode/auth.json"},
		PrintArgs: func(prompt string) []string {
			return []string{"run", prompt}
		},
		Stream: "text",
	},
	{
		ID:          "aider",
		DisplayName: "Aider",
		Binaries:    []string{"aider"},
		// No CredFiles: aider is driven purely by API-key environment variables,
		// so there is nothing local to probe. Detect reports LoginUnknown.
		PrintArgs: func(prompt string) []string {
			return []string{"--message", prompt, "--yes-always", "--no-auto-commits"}
		},
		Stream: "text",
	},
	{
		ID:          "goose",
		DisplayName: "Goose",
		Binaries:    []string{"goose"},
		CredFiles:   []string{".config/goose/config.yaml"},
		PrintArgs: func(prompt string) []string {
			return []string{"run", "-t", prompt}
		},
		Stream: "text",
	},
	{
		ID:          "amp",
		DisplayName: "Amp",
		Binaries:    []string{"amp"},
		CredFiles:   []string{".config/amp/settings.json"},
		PrintArgs: func(prompt string) []string {
			return []string{"-x", prompt}
		},
		Stream: "text",
	},
	{
		ID:          "crush",
		DisplayName: "Crush",
		Binaries:    []string{"crush"},
		CredFiles:   []string{".config/crush/crush.json", ".local/share/crush/crush.json"},
		PrintArgs: func(prompt string) []string {
			return []string{"run", "-q", prompt}
		},
		Stream: "text",
	},
	{
		ID:          "cursor-agent",
		DisplayName: "Cursor Agent",
		Binaries:    []string{"cursor-agent"},
		CredFiles:   []string{".cursor/cli-config.json"},
		PrintArgs: func(prompt string) []string {
			return []string{"-p", prompt, "--output-format", "stream-json"}
		},
		Stream: "claude-stream-json",
	},
	{
		ID:          "copilot",
		DisplayName: "GitHub Copilot CLI",
		Binaries:    []string{"copilot"},
		CredFiles:   []string{".copilot/config.json"},
		PrintArgs: func(prompt string) []string {
			return []string{"-p", prompt}
		},
		Stream: "text",
	},
	{
		ID:          "droid",
		DisplayName: "Factory Droid",
		Binaries:    []string{"droid"},
		CredFiles:   []string{".factory/auth.json"},
		PrintArgs: func(prompt string) []string {
			return []string{"exec", prompt}
		},
		Stream: "text",
	},
}

// Harnesses returns the known-CLI catalog, in stable declaration order. It is a
// copy: callers may not mutate zero's catalog through the returned slice.
func Harnesses() []Harness {
	out := make([]Harness, len(catalog))
	for i, h := range catalog {
		out[i] = cloneHarness(h)
	}
	return out
}

// Lookup returns the harness registered under id.
func Lookup(id string) (Harness, bool) {
	normalized := strings.ToLower(strings.TrimSpace(id))
	for _, h := range catalog {
		if h.ID == normalized {
			return cloneHarness(h), true
		}
	}
	return Harness{}, false
}

func cloneHarness(h Harness) Harness {
	h.Binaries = append([]string{}, h.Binaries...)
	h.CredFiles = append([]string{}, h.CredFiles...)
	return h
}

// Detect scans PATH for every known harness and probes login state for each
// one found. Harnesses whose binary is not on PATH are omitted entirely — a
// Detection always names a CLI that is actually installed.
func Detect(deps Deps) []Detection {
	deps = resolveDeps(deps)
	out := make([]Detection, 0, len(catalog))
	for _, h := range catalog {
		path, found := lookupBinary(h, deps)
		if !found {
			continue
		}
		out = append(out, Detection{Harness: cloneHarness(h), Path: path, Login: probeLogin(h, deps)})
	}
	return out
}

// DetectOne probes a single harness by id. It reports false when id is not in
// the catalog or its binary is not on PATH.
func DetectOne(id string, deps Deps) (Detection, bool) {
	h, ok := Lookup(id)
	if !ok {
		return Detection{}, false
	}
	deps = resolveDeps(deps)
	path, found := lookupBinary(h, deps)
	if !found {
		return Detection{}, false
	}
	return Detection{Harness: h, Path: path, Login: probeLogin(h, deps)}, true
}

func lookupBinary(h Harness, deps Deps) (string, bool) {
	for _, name := range h.Binaries {
		if path, err := deps.LookPath(name); err == nil {
			return path, true
		}
	}
	return "", false
}

// probeLogin applies the heuristic described in the package doc: LoggedIn if
// any CredFile exists under Home or the keychain probe returns data, LoggedOut
// if the harness has probes but none hit, LoginUnknown if it has none.
func probeLogin(h Harness, deps Deps) LoginState {
	hasKeychainProbe := h.KeychainService != "" && deps.Keychain != nil
	if len(h.CredFiles) == 0 && !hasKeychainProbe {
		return LoginUnknown
	}
	for _, rel := range h.CredFiles {
		if _, err := deps.ReadFile(homeJoin(deps.Home, rel)); err == nil {
			return LoggedIn
		}
	}
	if hasKeychainProbe {
		if data, err := deps.Keychain(h.KeychainService); err == nil && len(data) > 0 {
			return LoggedIn
		}
	}
	return LoggedOut
}

func resolveDeps(deps Deps) Deps {
	if deps.LookPath == nil {
		deps.LookPath = exec.LookPath
	}
	if deps.Home == "" {
		if home, err := os.UserHomeDir(); err == nil {
			deps.Home = home
		}
	}
	if deps.ReadFile == nil {
		deps.ReadFile = os.ReadFile
	}
	if deps.Keychain == nil && runtime.GOOS == "darwin" {
		deps.Keychain = readKeychain
	}
	return deps
}

func homeJoin(home, rel string) string {
	if home == "" {
		return rel
	}
	return home + string(os.PathSeparator) + rel
}

// readKeychain is the real Deps.Keychain implementation, wired up only on
// darwin (see resolveDeps): it shells out to the `security` CLI rather than
// linking a CGo/keychain-access library, keeping this package stdlib-only.
func readKeychain(service string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), keychainTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return nil, fmt.Errorf("agentcli: keychain lookup for %q: %w", service, err)
	}
	return bytes.TrimRight(out, "\n"), nil
}
