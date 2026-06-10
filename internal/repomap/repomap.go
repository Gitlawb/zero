// Package repomap scans a workspace into a compact, deterministic repository map.
package repomap

import (
	"errors"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	DefaultMaxFiles = 2000
	DefaultMaxDepth = 6
)

var errStopWalk = errors.New("repo map scan stopped")

type Options struct {
	MaxFiles int
	// MaxDepth is measured as path separators below the root. Zero means root
	// files only; negative values use DefaultMaxDepth.
	MaxDepth            int
	MaxBytesPerFileName int
}

type Snapshot struct {
	Root            string   `json:"root"`
	Files           []File   `json:"files"`
	FileCount       int      `json:"fileCount"`
	DirectoryCount  int      `json:"directoryCount"`
	MaxDepth        int      `json:"maxDepth"`
	LanguageCounts  []Count  `json:"languages"`
	ExtensionCounts []Count  `json:"extensions"`
	ImportantFiles  []string `json:"importantFiles"`
	Tree            []string `json:"tree"`
	Truncated       bool     `json:"truncated,omitempty"`
}

// RepoMap is kept as a semantic alias for call sites/tests that use the product
// term instead of the storage term.
type RepoMap = Snapshot

type File struct {
	Path      string `json:"path"`
	Language  string `json:"language,omitempty"`
	Extension string `json:"extension,omitempty"`
	Depth     int    `json:"depth,omitempty"`
}

type Count struct {
	Name  string `json:"name"`
	Count int    `json:"fileCount"`
}

func Scan(root string, options Options) (Snapshot, error) {
	cleanRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return Snapshot{}, err
	}
	cleanRoot = filepath.Clean(cleanRoot)

	maxFiles := options.MaxFiles
	if maxFiles <= 0 {
		maxFiles = DefaultMaxFiles
	}
	maxDepth := options.MaxDepth
	if maxDepth < 0 {
		maxDepth = DefaultMaxDepth
	}

	var files []File
	dirs := map[string]struct{}{}
	truncated := false
	walkErr := filepath.WalkDir(cleanRoot, func(current string, entry fs.DirEntry, walkErr error) error {
		if handled, err := handleWalkError(cleanRoot, current, entry, walkErr, &truncated); handled {
			return err
		}
		if current == cleanRoot {
			return nil
		}

		rel, relErr := filepath.Rel(cleanRoot, current)
		if relErr != nil {
			truncated = true
			return relErr
		}
		rel = filepath.ToSlash(rel)

		if entry.IsDir() {
			if shouldSkipDir(entry.Name()) {
				return filepath.SkipDir
			}
			if isSymlink(entry) {
				return filepath.SkipDir
			}
			if pathDepth(rel) > maxDepth {
				truncated = true
				return filepath.SkipDir
			}
			dirs[rel] = struct{}{}
			return nil
		}

		if isSymlink(entry) {
			return nil
		}
		if shouldSkipFile(rel) {
			return nil
		}
		depth := fileDepth(rel)
		if depth > maxDepth {
			truncated = true
			return nil
		}
		if options.MaxBytesPerFileName > 0 && len(rel) > options.MaxBytesPerFileName {
			truncated = true
			return nil
		}
		if len(files) >= maxFiles {
			truncated = true
			return errStopWalk
		}

		ext := strings.ToLower(path.Ext(rel))
		files = append(files, File{
			Path:      rel,
			Language:  languageForExt(ext),
			Extension: ext,
			Depth:     depth,
		})
		return nil
	})
	sortFiles(files)
	snapshot := Snapshot{
		Root:           cleanRoot,
		Files:          files,
		FileCount:      len(files),
		DirectoryCount: len(dirs),
		MaxDepth:       maxFileDepth(files),
		Truncated:      truncated,
	}
	snapshot.LanguageCounts = countLanguages(files)
	snapshot.ExtensionCounts = countExtensions(files)
	snapshot.ImportantFiles = importantFilePaths(files)
	snapshot.Tree = buildTree(files)
	if walkErr != nil && !errors.Is(walkErr, errStopWalk) {
		return snapshot, walkErr
	}
	return snapshot, nil
}

func handleWalkError(cleanRoot string, current string, entry fs.DirEntry, walkErr error, truncated *bool) (bool, error) {
	if walkErr == nil {
		return false, nil
	}
	*truncated = true
	if current == cleanRoot {
		return true, walkErr
	}
	if entry != nil && entry.IsDir() {
		return true, filepath.SkipDir
	}
	return true, nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".cache", ".git", ".next", ".worktrees", ".zero", "build", "coverage", "dist", "node_modules", "vendor":
		return true
	default:
		return false
	}
}

func shouldSkipFile(rel string) bool {
	base := strings.ToLower(path.Base(rel))
	switch base {
	case ".git", ".ds_store":
		return true
	}
	switch strings.ToLower(path.Ext(base)) {
	case ".a", ".class", ".dll", ".dylib", ".exe", ".gz", ".jar", ".o", ".so", ".tar", ".tgz", ".zip":
		return true
	default:
		return false
	}
}

func isSymlink(entry fs.DirEntry) bool {
	return entry.Type()&fs.ModeSymlink != 0
}

func pathDepth(rel string) int {
	if rel == "" || rel == "." {
		return 0
	}
	return strings.Count(filepath.ToSlash(rel), "/") + 1
}

func fileDepth(rel string) int {
	if rel == "" || !strings.Contains(rel, "/") {
		return 0
	}
	return strings.Count(filepath.ToSlash(rel), "/")
}

func languageForExt(ext string) string {
	switch strings.TrimPrefix(strings.ToLower(ext), ".") {
	case "bash", "sh", "zsh":
		return "Shell"
	case "c":
		return "C"
	case "cc", "cpp", "cxx", "hh", "hpp":
		return "C++"
	case "cs":
		return "C#"
	case "css":
		return "CSS"
	case "dart":
		return "Dart"
	case "ex", "exs":
		return "Elixir"
	case "go":
		return "Go"
	case "html", "htm":
		return "HTML"
	case "java":
		return "Java"
	case "js", "jsx", "mjs", "cjs":
		return "JavaScript"
	case "json":
		return "JSON"
	case "kt", "kts":
		return "Kotlin"
	case "lua":
		return "Lua"
	case "md", "markdown":
		return "Markdown"
	case "php":
		return "PHP"
	case "proto":
		return "Protobuf"
	case "py":
		return "Python"
	case "rb":
		return "Ruby"
	case "rs":
		return "Rust"
	case "sass", "scss":
		return "SCSS"
	case "sql":
		return "SQL"
	case "svelte":
		return "Svelte"
	case "swift":
		return "Swift"
	case "tf":
		return "Terraform"
	case "ts", "tsx":
		return "TypeScript"
	case "vue":
		return "Vue"
	default:
		return ""
	}
}

func maxFileDepth(files []File) int {
	maxDepth := 0
	for _, file := range files {
		if file.Depth > maxDepth {
			maxDepth = file.Depth
		}
	}
	return maxDepth
}

func countLanguages(files []File) []Count {
	counts := map[string]int{}
	for _, file := range files {
		if file.Language != "" {
			counts[file.Language]++
		}
	}
	return sortedCounts(counts)
}

func countExtensions(files []File) []Count {
	counts := map[string]int{}
	for _, file := range files {
		if file.Extension != "" {
			counts[file.Extension]++
		}
	}
	return sortedCounts(counts)
}

func sortedCounts(counts map[string]int) []Count {
	result := make([]Count, 0, len(counts))
	for name, count := range counts {
		if name == "" || count <= 0 {
			continue
		}
		result = append(result, Count{Name: name, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].Name < result[j].Name
	})
	return result
}

func importantFilePaths(files []File) []string {
	type important struct {
		path     string
		priority int
	}
	values := []important{}
	for _, file := range files {
		if priority, ok := importantPriority(file.Path); ok {
			values = append(values, important{path: file.Path, priority: priority})
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].priority != values[j].priority {
			return values[i].priority < values[j].priority
		}
		return values[i].path < values[j].path
	})
	paths := make([]string, 0, len(values))
	for _, value := range values {
		paths = append(paths, value.path)
	}
	return paths
}

func importantPriority(file string) (int, bool) {
	base := strings.ToLower(path.Base(file))
	switch base {
	case "agents.md":
		return 10, true
	case "zero.md":
		return 20, true
	case "readme.md":
		return 30, true
	case "contributing.md":
		return 40, true
	case "go.mod":
		return 50, true
	case "go.sum":
		return 55, true
	case "package.json":
		return 60, true
	case "cargo.toml":
		return 70, true
	case "pyproject.toml":
		return 80, true
	case "requirements.txt":
		return 90, true
	case "makefile":
		return 100, true
	case "dockerfile", "docker-compose.yml", "docker-compose.yaml":
		return 110, true
	default:
		return 0, false
	}
}

func buildTree(files []File) []string {
	root := newTreeNode()
	for _, file := range files {
		insertFile(root, file.Path)
	}
	lines := []string{"."}
	appendTreeLines(&lines, root, 0)
	return lines
}

type treeNode struct {
	dirs  map[string]*treeNode
	files map[string]struct{}
}

func newTreeNode() *treeNode {
	return &treeNode{dirs: map[string]*treeNode{}, files: map[string]struct{}{}}
}

func insertFile(root *treeNode, rel string) {
	parts := strings.Split(rel, "/")
	if len(parts) == 0 {
		return
	}
	node := root
	for _, part := range parts[:len(parts)-1] {
		if part == "" {
			continue
		}
		if node.dirs[part] == nil {
			node.dirs[part] = newTreeNode()
		}
		node = node.dirs[part]
	}
	name := parts[len(parts)-1]
	if name != "" {
		node.files[name] = struct{}{}
	}
}

func appendTreeLines(lines *[]string, node *treeNode, depth int) {
	for _, entry := range sortedTreeEntries(node) {
		prefix := strings.Repeat("  ", depth)
		if entry.dir {
			*lines = append(*lines, prefix+entry.name+"/")
			appendTreeLines(lines, node.dirs[entry.name], depth+1)
			continue
		}
		*lines = append(*lines, prefix+entry.name)
	}
}

type treeEntry struct {
	name string
	dir  bool
}

func sortedTreeEntries(node *treeNode) []treeEntry {
	entries := make([]treeEntry, 0, len(node.dirs)+len(node.files))
	for name := range node.dirs {
		entries = append(entries, treeEntry{name: name, dir: true})
	}
	for name := range node.files {
		entries = append(entries, treeEntry{name: name})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].name != entries[j].name {
			return entries[i].name < entries[j].name
		}
		return entries[i].dir && !entries[j].dir
	})
	return entries
}

func sortFiles(files []File) {
	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})
}
