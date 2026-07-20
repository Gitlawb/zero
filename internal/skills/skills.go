// Package skills discovers reusable instruction "skills" stored on disk as
// */SKILL.md files. Each skill is a directory containing a SKILL.md whose
// optional YAML-ish frontmatter carries a name/description and whose markdown
// body is the skill content the model can pull in on demand (PRD F15).
//
// The loader is deliberately dependency-free: frontmatter is hand-parsed (no
// YAML library) and malformed files are skipped rather than failing the whole
// load, so a single bad skill never hides the good ones.
package skills

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill is a single discovered skill. Name and Description come from the
// SKILL.md frontmatter (Name falls back to the directory name); Content is the
// markdown body; Path is the absolute path to the SKILL.md file.
type Skill struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Content     string `json:"content,omitempty"`
	Path        string `json:"path"`
}

const skillFileName = "SKILL.md"

// errNotDirectory is returned when a skills root path exists but is not a
// directory. On Windows, os.ReadDir reports that case as ErrNotExist
// (ENOTDIR aliases ERROR_PATH_NOT_FOUND), so load reclassifies it explicitly.
var errNotDirectory = errors.New("not a directory")

// DefaultDir resolves the skills directory, mirroring sessions.DefaultRoot. An
// explicit ZERO_SKILLS_DIR override wins; otherwise it is
// $XDG_DATA_HOME/zero/skills or ~/.local/share/zero/skills. The directory is
// NOT created — a missing directory simply yields no skills.
//
// DefaultDir is the primary write root for install/remove/lock. Runtime discovery
// also considers AgentsDir and plugin skill roots via DiscoveryRoots / LoadFromRoots.
func DefaultDir(env map[string]string) string {
	if override := strings.TrimSpace(envValue(env, "ZERO_SKILLS_DIR")); override != "" {
		return override
	}
	dataHome := strings.TrimSpace(envValue(env, "XDG_DATA_HOME"))
	home := strings.TrimSpace(envValue(env, "HOME"))
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = userHome
		}
	}
	base := dataHome
	if base == "" {
		if home == "" {
			// No XDG_DATA_HOME and no resolvable home: returning a relative path
			// here (".local/share/zero/skills") would bind skills to the process
			// CWD, so signal "no skills dir" and let the caller handle it.
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "zero", "skills")
}

// AgentsDir returns ~/.agents/skills when that path exists and is a directory.
// It is a shared, read-only multi-agent skills root (Zero, Hermes, Claude Code,
// etc.) and is never the target of install/remove/lock. Missing, non-directory,
// or unresolvable home yields "" with no error and no directory creation.
//
// Home resolution matches other packages: HOME, then USERPROFILE, then
// os.UserHomeDir(). ZERO_SKILLS_DIR is intentionally ignored — agents is a
// pure convention path, not a Zero-specific override.
func AgentsDir(env map[string]string) string {
	home := strings.TrimSpace(firstNonEmpty(
		envValue(env, "HOME"),
		envValue(env, "USERPROFILE"),
	))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(userHome) == "" {
			return ""
		}
		home = userHome
	}
	dir := filepath.Join(home, ".agents", "skills")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}
	return dir
}

// DiscoveryRoots returns ordered skill roots for runtime discovery: primary
// DefaultDir, optional AgentsDir when present, then pluginRoots. Empty strings
// are omitted. Earlier entries win on name clashes.
func DiscoveryRoots(env map[string]string, pluginRoots []string) []string {
	return collectRoots(DefaultDir(env), AgentsDir(env), pluginRoots)
}

// collectRoots assembles ordered non-empty skill roots. primary is typically
// DefaultDir (or an injected test dir); agents is typically AgentsDir's result.
func collectRoots(primary string, agents string, pluginRoots []string) []string {
	roots := make([]string, 0, 2+len(pluginRoots))
	if primary = strings.TrimSpace(primary); primary != "" {
		roots = append(roots, primary)
	}
	if agents = strings.TrimSpace(agents); agents != "" {
		roots = append(roots, agents)
	}
	for _, root := range pluginRoots {
		if root = strings.TrimSpace(root); root != "" {
			roots = append(roots, root)
		}
	}
	return roots
}

// GlobalRoots returns discovery roots for management CLI list/info: an explicit
// primary write/root dir (usually skillsDir / DefaultDir) plus AgentsDir when
// present. Plugin roots are excluded from management UX.
func GlobalRoots(primary string) []string {
	return collectRoots(primary, AgentsDir(nil), nil)
}

// DuplicateName records two skills that resolved to the same frontmatter name.
// Winner is the SKILL.md path of the skill that was kept (the one in the
// lexicographically-first directory); Loser is the path that was dropped.
type DuplicateName struct {
	Name   string
	Winner string
	Loser  string
}

// Load scans dir for */SKILL.md files and returns the parsed skills sorted by
// name. A missing directory yields an empty slice with no error; individual
// malformed skill files are skipped rather than failing the whole load.
//
// When two skills declare the SAME frontmatter name, resolution is made
// DETERMINISTIC by a documented rule: the skill in the lexicographically-first
// directory name wins (os.ReadDir returns entries sorted by filename, so the
// first one encountered is kept and later same-name duplicates are dropped).
// This guarantees Load/List/Get always resolve a duplicated name to the same
// winner regardless of sort stability. Use Duplicates to surface a warning about
// any such collisions.
//
// NOTE: Load scans one root. Runtime discovery uses LoadFromRoots /
// DiscoveryRoots (primary DefaultDir, optional ~/.agents/skills, then plugin
// skill roots). Prefer those multi-root helpers for agent/CLI discovery; keep
// Load for single-dir install/write call sites.
func Load(dir string) ([]Skill, error) {
	skills, _, err := load(dir)
	return skills, err
}

// LoadFromRoots loads and merges skills from the provided directories (earlier
// entries win on name clashes). Empty roots are skipped. Missing directories are
// treated as empty (same as Load). Intra-root and cross-root collisions are
// reported as DuplicateName.
//
// The first non-empty root is the required primary: non-missing load failures
// (permission, I/O, not a directory, etc.) are returned so callers do not
// confuse a broken primary skills dir with "no skills". Later optional roots
// (e.g. ~/.agents/skills, plugin roots) fail open so one bad optional directory
// does not hide the rest.
func LoadFromRoots(dirs []string) ([]Skill, []DuplicateName, error) {
	merged := make([]Skill, 0)
	duplicates := []DuplicateName{}
	byName := map[string]int{}
	primary := true

	for _, dir := range dirs {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		loaded, rootDups, err := load(dir)
		if err != nil {
			if primary {
				// Required primary root: surface real I/O failures instead of
				// collapsing to an empty skill list.
				return nil, nil, err
			}
			// Optional roots fail open: one bad directory must not hide the rest.
			continue
		}
		primary = false
		duplicates = append(duplicates, rootDups...)
		for _, skill := range loaded {
			if winnerIdx, clash := byName[skill.Name]; clash {
				duplicates = append(duplicates, DuplicateName{
					Name:   skill.Name,
					Winner: merged[winnerIdx].Path,
					Loser:  skill.Path,
				})
				continue
			}
			byName[skill.Name] = len(merged)
			merged = append(merged, skill)
		}
	}

	sort.Slice(merged, func(left int, right int) bool {
		return merged[left].Name < merged[right].Name
	})
	return merged, duplicates, nil
}

// ListFromRoots is like LoadFromRoots but strips Content (like List).
func ListFromRoots(dirs []string) ([]Skill, []DuplicateName, error) {
	loaded, dups, err := LoadFromRoots(dirs)
	if err != nil {
		return nil, dups, err
	}
	listed := make([]Skill, 0, len(loaded))
	for _, skill := range loaded {
		skill.Content = ""
		listed = append(listed, skill)
	}
	return listed, dups, nil
}

// Duplicates returns the duplicate-name collisions Load resolved by the
// first-directory-wins rule, so a caller can warn the user that a shadowed skill
// was dropped. A missing directory yields no duplicates and no error.
func Duplicates(dir string) ([]DuplicateName, error) {
	_, dups, err := load(dir)
	return dups, err
}

// confineSkillPath resolves manifestPath through symlinks and returns the real
// path only if it stays within rootReal (the already-symlink-resolved skills
// root). This stops a symlinked SKILL.md — or a symlinked skill directory — from
// making the permission-allow skill tool read files outside the skills root.
// ok=false also covers a missing path or one that is a directory.
func confineSkillPath(rootReal string, manifestPath string) (string, bool) {
	real, err := filepath.EvalSymlinks(manifestPath)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(rootReal, real)
	if err != nil {
		return "", false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	// Only read regular files. A non-regular in-root target (directory, FIFO,
	// device, socket) named SKILL.md would otherwise make os.ReadFile block
	// indefinitely — skill is a permission-allow tool over a user-controlled dir.
	info, err := os.Lstat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return real, true
}

// load is the shared scanner behind Load and Duplicates: it parses every
// SKILL.md, deduplicates by frontmatter name (first directory wins) and reports
// the dropped collisions.
func load(dir string) ([]Skill, []DuplicateName, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return []Skill{}, nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Windows maps ENOTDIR to ERROR_PATH_NOT_FOUND, which is also
			// ErrNotExist. A primary skills path that exists but is not a
			// directory must still surface as a load error, not "no skills".
			if info, statErr := os.Stat(dir); statErr == nil && !info.IsDir() {
				return nil, nil, &os.PathError{
					Op:   "readdir",
					Path: dir,
					Err:  errNotDirectory,
				}
			}
			return []Skill{}, nil, nil
		}
		return nil, nil, err
	}

	// Resolve the skills root through symlinks so each SKILL.md can be confined to
	// it. skill is a permission-allow read-only core/MCP tool, so the loader must
	// never follow a symlinked SKILL.md (or skill dir) out of the root and become
	// an arbitrary-file reader. Fall back to an absolute dir if EvalSymlinks fails
	// so confinement still has a stable root.
	rootReal, rootErr := filepath.EvalSymlinks(dir)
	if rootErr != nil {
		if abs, absErr := filepath.Abs(dir); absErr == nil {
			rootReal = abs
		} else {
			rootReal = dir
		}
	}

	skills := make([]Skill, 0, len(entries))
	// byName maps a frontmatter name to the index of the winning skill in skills,
	// so a later same-name duplicate can be recognized and dropped deterministically.
	byName := make(map[string]int, len(entries))
	duplicates := []DuplicateName{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(dir, entry.Name(), skillFileName)
		realPath, ok := confineSkillPath(rootReal, manifestPath)
		if !ok {
			// Missing/unreadable SKILL.md, a directory, or a symlink escaping the
			// skills root: skip it rather than read a file outside the root. One bad
			// or hostile skill must not hide the rest or leak external files.
			continue
		}
		data, err := os.ReadFile(realPath)
		if err != nil {
			continue
		}
		absPath := manifestPath
		if resolved, absErr := filepath.Abs(manifestPath); absErr == nil {
			absPath = resolved
		}
		skill := parseSkill(entry.Name(), absPath, string(data))
		if winnerIdx, clash := byName[skill.Name]; clash {
			// os.ReadDir yields entries sorted by directory name, so the skill already
			// recorded came from the lexicographically-first directory and wins; this
			// later one is dropped (but reported as a duplicate).
			duplicates = append(duplicates, DuplicateName{
				Name:   skill.Name,
				Winner: skills[winnerIdx].Path,
				Loser:  skill.Path,
			})
			continue
		}
		byName[skill.Name] = len(skills)
		skills = append(skills, skill)
	}

	// Names are unique after dedup, so this sort is fully deterministic.
	sort.Slice(skills, func(left int, right int) bool {
		return skills[left].Name < skills[right].Name
	})
	return skills, duplicates, nil
}

// List loads the skills directory and returns each skill without its (possibly
// large) Content body — handy for `zero skills` listings.
func List(dir string) ([]Skill, error) {
	loaded, err := Load(dir)
	if err != nil {
		return nil, err
	}
	listed := make([]Skill, 0, len(loaded))
	for _, skill := range loaded {
		skill.Content = ""
		listed = append(listed, skill)
	}
	return listed, nil
}

// Get loads the named skill from dir, returning false if it is not found.
func Get(dir string, name string) (Skill, bool) {
	loaded, err := Load(dir)
	if err != nil {
		return Skill{}, false
	}
	target := strings.TrimSpace(name)
	for _, skill := range loaded {
		if skill.Name == target {
			return skill, true
		}
	}
	return Skill{}, false
}

// parseSkill splits optional `---`-delimited frontmatter from the markdown body.
// Frontmatter is a simple line parser for `name:`/`description:` keys (no YAML
// dependency). Without frontmatter, Name defaults to the directory name and
// Description is empty.
func parseSkill(dirName string, path string, raw string) Skill {
	body := raw
	name := dirName
	description := ""

	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	if frontmatter, remainder, ok := splitFrontmatter(normalized); ok {
		body = remainder
		if parsedName := frontmatterValue(frontmatter, "name"); parsedName != "" {
			name = parsedName
		}
		description = frontmatterValue(frontmatter, "description")
	}

	return Skill{
		Name:        name,
		Description: description,
		Content:     strings.TrimSpace(body),
		Path:        path,
	}
}

// splitFrontmatter detects a leading `---` line, captures lines up to the
// closing `---`, and returns the frontmatter block plus the remaining body. It
// reports ok=false when there is no opening delimiter or no closing delimiter
// (in which case the whole input is treated as body).
func splitFrontmatter(normalized string) (string, string, bool) {
	if !strings.HasPrefix(normalized, "---\n") && normalized != "---" {
		return "", "", false
	}
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", "", false
	}
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			frontmatter := strings.Join(lines[1:index], "\n")
			body := strings.Join(lines[index+1:], "\n")
			return frontmatter, body, true
		}
	}
	// No closing delimiter — not valid frontmatter; treat the whole file as body.
	return "", "", false
}

// frontmatterValue reads a single `key: value` pair from the frontmatter block.
// Matching is case-insensitive on the key; the first occurrence wins.
func frontmatterValue(frontmatter string, key string) string {
	prefix := strings.ToLower(key) + ":"
	for line := range strings.SplitSeq(frontmatter, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToLower(trimmed), prefix) {
			value := strings.TrimSpace(trimmed[len(prefix):])
			return strings.Trim(value, `"'`)
		}
	}
	return ""
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
