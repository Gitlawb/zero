package lsp

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// serverCommands maps a file extension to the language-server command (argv) ZERO
// will spawn. The first element is the binary looked up on PATH; missing binaries
// are not an error, the agent just degrades to text-only for that file. Every
// command is the language's community-standard server invoked in stdio mode, so
// a wrong guess can never spawn something surprising — only fail the PATH check.
var serverCommands = map[string][]string{
	".go":     {"gopls", "serve"},
	".ts":     {"typescript-language-server", "--stdio"},
	".tsx":    {"typescript-language-server", "--stdio"},
	".mts":    {"typescript-language-server", "--stdio"},
	".cts":    {"typescript-language-server", "--stdio"},
	".js":     {"typescript-language-server", "--stdio"},
	".jsx":    {"typescript-language-server", "--stdio"},
	".mjs":    {"typescript-language-server", "--stdio"},
	".cjs":    {"typescript-language-server", "--stdio"},
	".py":     {"pyright-langserver", "--stdio"},
	".pyi":    {"pyright-langserver", "--stdio"},
	".rs":     {"rust-analyzer"},
	".c":      {"clangd"},
	".h":      {"clangd"},
	".cpp":    {"clangd"},
	".cc":     {"clangd"},
	".cxx":    {"clangd"},
	".hpp":    {"clangd"},
	".java":   {"jdtls"},
	".kt":     {"kotlin-language-server"},
	".kts":    {"kotlin-language-server"},
	".rb":     {"ruby-lsp"},
	".php":    {"intelephense", "--stdio"},
	".zig":    {"zls"},
	".lua":    {"lua-language-server"},
	".ex":     {"elixir-ls"},
	".exs":    {"elixir-ls"},
	".hs":     {"haskell-language-server-wrapper", "--lsp"},
	".swift":  {"sourcekit-lsp"},
	".ml":     {"ocamllsp"},
	".mli":    {"ocamllsp"},
	".scala":  {"metals"},
	".clj":    {"clojure-lsp"},
	".cljs":   {"clojure-lsp"},
	".cljc":   {"clojure-lsp"},
	".dart":   {"dart", "language-server"},
	".gleam":  {"gleam", "lsp"},
	".nix":    {"nixd"},
	".tf":     {"terraform-ls", "serve"},
	".sh":     {"bash-language-server", "start"},
	".bash":   {"bash-language-server", "start"},
	".yaml":   {"yaml-language-server", "--stdio"},
	".yml":    {"yaml-language-server", "--stdio"},
	".json":   {"vscode-json-language-server", "--stdio"},
	".css":    {"vscode-css-language-server", "--stdio"},
	".scss":   {"vscode-css-language-server", "--stdio"},
	".less":   {"vscode-css-language-server", "--stdio"},
	".html":   {"vscode-html-language-server", "--stdio"},
	".svelte": {"svelteserver", "--stdio"},
	".vue":    {"vue-language-server", "--stdio"},
	".astro":  {"astro-ls", "--stdio"},
}

// languageIDs maps a file extension to the LSP languageId used in didOpen.
var languageIDs = map[string]string{
	".go":     "go",
	".ts":     "typescript",
	".tsx":    "typescriptreact",
	".mts":    "typescript",
	".cts":    "typescript",
	".js":     "javascript",
	".jsx":    "javascriptreact",
	".mjs":    "javascript",
	".cjs":    "javascript",
	".py":     "python",
	".pyi":    "python",
	".rs":     "rust",
	".c":      "c",
	".h":      "c",
	".cpp":    "cpp",
	".cc":     "cpp",
	".cxx":    "cpp",
	".hpp":    "cpp",
	".java":   "java",
	".kt":     "kotlin",
	".kts":    "kotlin",
	".rb":     "ruby",
	".php":    "php",
	".zig":    "zig",
	".lua":    "lua",
	".ex":     "elixir",
	".exs":    "elixir",
	".hs":     "haskell",
	".swift":  "swift",
	".ml":     "ocaml",
	".mli":    "ocaml",
	".scala":  "scala",
	".clj":    "clojure",
	".cljs":   "clojure",
	".cljc":   "clojure",
	".dart":   "dart",
	".gleam":  "gleam",
	".nix":    "nix",
	".tf":     "terraform",
	".sh":     "shellscript",
	".bash":   "shellscript",
	".yaml":   "yaml",
	".yml":    "yaml",
	".json":   "json",
	".css":    "css",
	".scss":   "scss",
	".less":   "less",
	".html":   "html",
	".svelte": "svelte",
	".vue":    "vue",
	".astro":  "astro",
}

// coreServerBinaries are the tier-1 servers for the languages agents hit most;
// `zero doctor` treats their absence as warn-worthy. The long tail of servers
// configured above for breadth is reported informationally only — warning about
// a missing zls on a machine with no Zig code would be permanent noise.
var coreServerBinaries = []string{
	"gopls",
	"typescript-language-server",
	"pyright-langserver",
	"rust-analyzer",
}

// CoreServerBinaries returns the tier-1 server binaries (sorted copy).
func CoreServerBinaries() []string {
	binaries := append([]string(nil), coreServerBinaries...)
	sort.Strings(binaries)
	return binaries
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
