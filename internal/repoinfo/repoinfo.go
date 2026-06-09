// Package repoinfo characterizes a repository from local git commands only
// (no network). It powers the `zero repo-info` command.
package repoinfo

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	bytesPerLine   = 50    // LOC estimate heuristic (matches the source module)
	maxCommitsWalk = 10000 // bound history walking on very active repos
)

// ErrNotGitRepo is returned when the directory is not a git repository or HEAD
// has no commits (git ls-tree against HEAD fails).
var ErrNotGitRepo = errors.New("not a git repository, or it has no commits yet")

// LangStat is a per-language rollup.
type LangStat struct {
	Name        string `json:"name"`
	LOCEstimate int    `json:"locEstimate"`
	FileCount   int    `json:"fileCount"`
}

// Info is the collected repository characterization.
type Info struct {
	FileCount             int        `json:"fileCount"`
	DirectoryCount        int        `json:"directoryCount"`
	MaxDepth              int        `json:"maxDepth"`
	LOCEstimate           int        `json:"locEstimate"`
	Languages             []LangStat `json:"languages"`
	PrimaryLanguage       string     `json:"primaryLanguage,omitempty"`
	LanguageCount         int        `json:"languageCount"`
	WorkspaceType         string     `json:"workspaceType"`
	WorkspacePackageCount int        `json:"workspacePackageCount"`
	BuildTools            []string   `json:"buildTools"`
	TestTools             []string   `json:"testTools"`
	CICD                  []string   `json:"cicd"`
	HasGit                bool       `json:"hasGit"`
	Branch                string     `json:"branch,omitempty"`
	RemoteURL             string     `json:"remoteURL,omitempty"`
	AgeDays               *int       `json:"ageDays,omitempty"`
	Contributors90d       *int       `json:"contributors90d,omitempty"`
	CommitVelocity30d     *int       `json:"commitVelocity30d,omitempty"`
}

// RunGit runs a git subcommand in dir and returns raw stdout. A non-zero exit
// (or spawn failure) MUST return a non-nil error.
type RunGit func(ctx context.Context, dir string, args ...string) (string, error)

// Options configures Collect. Now and RunGit are injectable for tests.
type Options struct {
	Cwd    string
	Now    time.Time
	RunGit RunGit
}

// Collect gathers repository metadata from local git only. It never performs a
// network operation. Returns ErrNotGitRepo when the directory has no readable
// HEAD tree; individual history metrics fail soft (omitted, not fatal).
func Collect(ctx context.Context, opts Options) (Info, error) {
	run := opts.RunGit
	if run == nil {
		run = defaultRunGit
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	dir := opts.Cwd

	tree, err := run(ctx, dir, "ls-tree", "-r", "-l", "HEAD")
	if err != nil {
		return Info{}, ErrNotGitRepo
	}

	info := Info{HasGit: true, WorkspaceType: "none", Languages: []LangStat{}}
	langBytes := map[string]int{}
	langFiles := map[string]int{}
	buildSet := map[string]bool{}
	testSet := map[string]bool{}
	cicdSet := map[string]bool{}
	pkgDirs := map[string]bool{}
	appDirs := map[string]bool{}
	lastDir := ""
	hasPackageJSON := false
	hasCargoToml := false

	scanner := bufio.NewScanner(strings.NewReader(tree))
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		tab := strings.IndexByte(line, '\t')
		if tab < 0 {
			continue
		}
		fields := strings.Fields(line[:tab])
		if len(fields) < 4 {
			continue
		}
		size, convErr := strconv.Atoi(fields[3]) // "-" for gitlinks -> skip
		if convErr != nil {
			continue
		}
		filePath := line[tab+1:]
		info.FileCount++

		dirName := path.Dir(filePath)
		if dirName != lastDir && dirName != "." {
			info.DirectoryCount++
			lastDir = dirName
			if depth := strings.Count(filePath, "/"); depth > info.MaxDepth {
				info.MaxDepth = depth
			}
		}

		base := path.Base(filePath)
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(base), "."))
		if lang, ok := languageForExt(ext); ok {
			langBytes[lang] += size
			langFiles[lang]++
		}
		if buildToolFiles[base] {
			buildSet[base] = true
		}
		if testToolFiles[base] {
			testSet[base] = true
		}
		if ci := cicdForPath(filePath); ci != "" {
			cicdSet[ci] = true
		}
		if ws, ok := workspaceMarkers[base]; ok && info.WorkspaceType == "none" {
			info.WorkspaceType = ws
		}
		if filePath == "package.json" {
			hasPackageJSON = true
		}
		if filePath == "Cargo.toml" {
			hasCargoToml = true
		}
		if sub, ok := topSubdir(filePath, "packages/"); ok {
			pkgDirs[sub] = true
		}
		if sub, ok := topSubdir(filePath, "apps/"); ok {
			appDirs[sub] = true
		}
	}

	// Total LOCEstimate is the SUM of the per-language estimates so the parts
	// always add up to the whole (a single per-file truncation would not).
	for name, b := range langBytes {
		loc := b / bytesPerLine
		info.Languages = append(info.Languages, LangStat{Name: name, LOCEstimate: loc, FileCount: langFiles[name]})
		info.LOCEstimate += loc
	}
	sort.Slice(info.Languages, func(i, j int) bool {
		if info.Languages[i].LOCEstimate != info.Languages[j].LOCEstimate {
			return info.Languages[i].LOCEstimate > info.Languages[j].LOCEstimate
		}
		return info.Languages[i].Name < info.Languages[j].Name
	})
	info.LanguageCount = len(info.Languages)
	if len(info.Languages) > 0 {
		info.PrimaryLanguage = info.Languages[0].Name
	}
	info.BuildTools = sortedUnique(buildSet)
	info.TestTools = sortedUnique(testSet)
	info.CICD = sortedUnique(cicdSet)
	info.WorkspacePackageCount = len(pkgDirs) + len(appDirs)

	if info.WorkspaceType == "none" && hasPackageJSON && hasNpmWorkspaces(dir) {
		info.WorkspaceType = "npm-workspaces"
	}
	if info.WorkspaceType == "none" && hasCargoToml && hasCargoWorkspace(dir) {
		info.WorkspaceType = "cargo-workspace"
	}

	if branch, err := run(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(branch)
	}
	if remote, err := run(ctx, dir, "remote", "get-url", "origin"); err == nil {
		info.RemoteURL = strings.TrimSpace(remote)
	}
	if first, err := run(ctx, dir, "log", "--reverse", "-1", "--format=%ct"); err == nil {
		if ts, perr := strconv.ParseInt(strings.TrimSpace(first), 10, 64); perr == nil {
			days := int((now.Unix() - ts) / 86400)
			if days < 0 {
				days = 0
			}
			info.AgeDays = &days
		}
	}
	if authors, err := run(ctx, dir, "log", "--since=90 days ago", "--max-count="+strconv.Itoa(maxCommitsWalk), "--format=%aN"); err == nil {
		info.Contributors90d = countUnique(authors)
	}
	if count, err := run(ctx, dir, "rev-list", "--count", "--since=30 days ago", "--max-count="+strconv.Itoa(maxCommitsWalk), "HEAD"); err == nil {
		if n, perr := strconv.Atoi(strings.TrimSpace(count)); perr == nil {
			info.CommitVelocity30d = &n
		}
	}

	return info, nil
}

// topSubdir returns the first path segment under prefix (e.g. "packages/a/x" ->
// "a"), and false when filePath is not under prefix or has no subdir.
func topSubdir(filePath, prefix string) (string, bool) {
	if !strings.HasPrefix(filePath, prefix) {
		return "", false
	}
	rest := filePath[len(prefix):]
	slash := strings.IndexByte(rest, '/')
	if slash <= 0 {
		return "", false
	}
	return rest[:slash], true
}

// countUnique counts distinct non-empty trimmed lines.
func countUnique(out string) *int {
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			set[line] = true
		}
	}
	n := len(set)
	return &n
}

// hasNpmWorkspaces reports whether the root package.json declares workspaces
// (array or {packages:[]} object). Local file read only.
func hasNpmWorkspaces(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var pkg struct {
		Workspaces any `json:"workspaces"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return false
	}
	switch ws := pkg.Workspaces.(type) {
	case []any:
		return len(ws) > 0
	case map[string]any:
		return len(ws) > 0
	}
	return false
}

// hasCargoWorkspace reports whether the root Cargo.toml has a [workspace] table.
// Local file read only.
func hasCargoWorkspace(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "[workspace]")
}

// defaultRunGit shells the local `git` binary. It never contacts the network for
// the read-only subcommands Collect uses (ls-tree, rev-parse, remote get-url,
// log, rev-list).
func defaultRunGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}
