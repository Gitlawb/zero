// Package repoinfo characterizes a repository from local git commands only
// (no network). It powers the `zero repo-info` command.
package repoinfo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
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

	// -z gives NUL-terminated records with UNQUOTED paths; without it git wraps
	// non-ASCII/special filenames in C-quoted "..." (core.quotePath), which would
	// corrupt extension/tooling matching and silently drop those files.
	tree, err := run(ctx, dir, "ls-tree", "-r", "-l", "-z", "HEAD")
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
	dirSet := map[string]bool{}
	hasPackageJSON := false
	hasCargoToml := false

	for _, entry := range strings.Split(tree, "\x00") {
		if entry == "" {
			continue
		}
		tab := strings.IndexByte(entry, '\t')
		if tab < 0 {
			continue
		}
		fields := strings.Fields(entry[:tab])
		if len(fields) < 4 {
			continue
		}
		size, convErr := strconv.Atoi(fields[3]) // "-" for gitlinks -> skip
		if convErr != nil {
			continue
		}
		filePath := entry[tab+1:]
		info.FileCount++

		// Count every directory on the path to this file, including "passthrough"
		// directories that hold only subdirectories: git ls-tree -r lists blobs
		// only, so we expand each file's ancestors rather than count file parents.
		if dirName := path.Dir(filePath); dirName != "." {
			for d := dirName; d != "." && !dirSet[d]; d = path.Dir(d) {
				dirSet[d] = true
			}
		}
		if depth := strings.Count(filePath, "/"); depth > info.MaxDepth {
			info.MaxDepth = depth
		}

		base := path.Base(filePath)
		if lang, ok := languageForPath(filePath); ok {
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

	info.DirectoryCount = len(dirSet)

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
		info.RemoteURL = sanitizeRemoteURL(remote)
	}
	// Age = time since the FIRST commit. Use --max-parents=0 (root commits) — NOT
	// `log --reverse -1`, where git applies the -1 limit BEFORE reversing and so
	// returns the LATEST commit (age would always be ~0). Output is tiny (one root
	// for a normal repo; take the oldest across multiple roots).
	if roots, err := run(ctx, dir, "log", "--max-parents=0", "--format=%ct"); err == nil {
		oldest, found := int64(0), false
		for _, line := range strings.Split(roots, "\n") {
			if ts, perr := strconv.ParseInt(strings.TrimSpace(line), 10, 64); perr == nil {
				if !found || ts < oldest {
					oldest, found = ts, true
				}
			}
		}
		if found {
			days := int((now.Unix() - oldest) / 86400)
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

// sanitizeRemoteURL strips any credentials (userinfo) from a git remote URL so
// they never leak into the report. A remote can embed secrets, e.g.
// "https://x-access-token:ghp_…@github.com/o/r.git". URL forms have their
// user:password@ component removed; scp-like forms ("[user@]host:path") drop a
// leading "user@".
func sanitizeRemoteURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		u.User = nil
		return u.String()
	}
	// scp-like form (no scheme): user@host:path -> host:path.
	if at := strings.IndexByte(raw, '@'); at >= 0 && !strings.Contains(raw, "://") {
		if rest := raw[at+1:]; strings.Contains(rest, ":") {
			return rest
		}
	}
	return raw
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
