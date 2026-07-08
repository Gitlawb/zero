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

// DefaultDir resolves the skills directory, mirroring sessions.DefaultRoot. An
// explicit ZERO_SKILLS_DIR override wins; otherwise it is
// $XDG_DATA_HOME/zero/skills or ~/.local/share/zero/skills. The directory is
// NOT created — a missing directory simply yields no skills.
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
// NOTE: Load scans one root. Agent startup merges plugin-declared skill roots
// separately during plugin activation, so the runtime skill surface can include
// both the default directory and skills bundled by active plugins.
func Load(dir string) ([]Skill, error) {
	skills, _, err := load(dir)
	return skills, err
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
	for _, line := range strings.Split(frontmatter, "\n") {
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
