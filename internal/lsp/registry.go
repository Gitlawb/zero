package lsp

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// serverCommands maps a file extension to the language-server command (argv) ZERO
// will spawn. The first element is the binary looked up on PATH; missing binaries
// are not an error, the agent just degrades to text-only for that file.
var serverCommands = map[string][]string{
	".go":  {"gopls", "serve"},
	".ts":  {"typescript-language-server", "--stdio"},
	".tsx": {"typescript-language-server", "--stdio"},
	".js":  {"typescript-language-server", "--stdio"},
	".jsx": {"typescript-language-server", "--stdio"},
	".py":  {"pyright-langserver", "--stdio"},
	".rs":  {"rust-analyzer"},
}

// languageIDs maps a file extension to the LSP languageId used in didOpen.
var languageIDs = map[string]string{
	".go":  "go",
	".ts":  "typescript",
	".tsx": "typescriptreact",
	".js":  "javascript",
	".jsx": "javascriptreact",
	".py":  "python",
	".rs":  "rust",
}

// ServerBinaries returns the unique set of language-server binaries ZERO may
// spawn, sorted for stable output. It is the canonical list `zero doctor` checks
// against PATH, so the configured commands stay the single source of truth.
func ServerBinaries() []string {
	seen := map[string]bool{}
	binaries := make([]string, 0, len(serverCommands))
	for _, command := range serverCommands {
		if len(command) == 0 {
			continue
		}
		binary := command[0]
		if binary == "" || seen[binary] {
			continue
		}
		seen[binary] = true
		binaries = append(binaries, binary)
	}
	sort.Strings(binaries)
	return binaries
}

// ServerFor returns the server command for a path's extension, and whether one is
// configured. It does not check PATH (use Available for that).
func ServerFor(path string) ([]string, bool) {
	cmd, ok := serverCommands[extKey(path)]
	if !ok {
		return nil, false
	}
	return append([]string(nil), cmd...), true
}

// LanguageID returns the LSP languageId for a path's extension.
func LanguageID(path string) (string, bool) {
	id, ok := languageIDs[extKey(path)]
	return id, ok
}

// Available reports whether a configured server for the path exists on PATH.
func Available(path string) bool {
	cmd, ok := ServerFor(path)
	if !ok {
		return false
	}
	_, err := exec.LookPath(cmd[0])
	return err == nil
}

func extKey(path string) string {
	return strings.ToLower(filepath.Ext(path))
}
