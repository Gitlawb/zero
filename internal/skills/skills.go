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
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

// Skill is a single discovered skill. Name and Description come from the
// SKILL.md frontmatter (Name falls back to the directory name); Content is the
// markdown body; Path is the absolute path to the SKILL.md file; Dir is the
// absolute path to the skill directory; Assets lists additional files (scripts,
// configs, etc.) discovered in the skill directory alongside SKILL.md.
type Skill struct {
	Name        string  `json:"name"`
	Description string  `json:"description,omitempty"`
	Content     string  `json:"content,omitempty"`
	Path        string  `json:"path"`
	Dir         string  `json:"dir,omitempty"`
	Assets      []Asset `json:"assets,omitempty"`
}

// Asset describes a non-SKILL.md file discovered in a skill directory.
type Asset struct {
	Name string `json:"name"` // basename of the file
	Path string `json:"path"` // absolute path to the file
	Size int64  `json:"size"` // file size in bytes
}

// maxAssetSize is the maximum size of an individual asset file that will be
// listed by loadAssets. Files larger than this are silently skipped to prevent a
// malicious skill from including huge binaries.
const maxAssetSize = 1 << 20 // 1 MB

// maxAssetCount caps the number of assets discovered by loadAssets. A skill
// directory with thousands of tiny files would otherwise be traversed fully
// (expensive I/O + sort) only to have the <skill_assets> block dropped by
// FormatOutput when it exceeds maxSkillOutputSize. This cap bounds the
// discovery cost regardless of the output cap. It is the primary bound on
// discovery; there is no depth cap that hides installed files because
// fscopy.CopyTree installs files at any depth and the loader must keep every
// installed file discoverable.
const maxAssetCount = 500

// maxVisitedEntries is the total number of filesystem entries (files and
// directories) that loadAssets will examine. This bounds discovery cost for
// a tree with many empty or non-eligible directories where len(assets) does
// not grow despite heavy traversal. It is deliberately generous (real skill
// trees rarely exceed a few hundred entries) so it never hides a legitimately
// installed file; the primary bound remains maxAssetCount.
const maxVisitedEntries = 4096

// maxSkillOutputSize caps the total output of FormatOutput so a skill with many
// large assets cannot blow out the model's context window.
const maxSkillOutputSize = 100 << 10 // 100 KB

// FormatOutput returns the model-facing output for a skill: the SKILL.md body
// wrapped in <skill> tags, plus a <skill_assets> block listing additional files
// in the skill directory (omitted when there are none). Output is capped at
// maxSkillOutputSize bytes; on overflow the body is kept complete and assets
// are dropped, truncating the body itself only when it alone exceeds the cap.
//
// All code paths guarantee the returned string is at most maxSkillOutputSize bytes.
func FormatOutput(skill Skill) string {
	const truncationNote = "\n(output truncated)"

	openTag := fmt.Sprintf("<skill name=%q dir=%q>\n", skill.Name, skill.Dir)
	var bb strings.Builder
	bb.WriteString(openTag)
	bb.WriteString(skill.Content)
	bb.WriteString("\n</skill>")
	body := bb.String()

	if len(body) > maxSkillOutputSize {
		// Truncate the content only, preserving the open tag and appending the
		// note plus closing </skill>. The cut lands on a UTF-8 rune and line
		// boundary and never retreats into the open tag.
		const closeTag = "\n</skill>"
		ceiling := maxSkillOutputSize - len(truncationNote) - len(closeTag)
		contentStart := len(openTag)
		dropFrame := false
		if ceiling < contentStart {
			// openTag itself exceeds the budget after reserving note+closeTag.
			// Drop both note and closeTag to keep as much content as possible.
			ceiling = maxSkillOutputSize
			// If even the open tag alone exceeds the hard limit, there is no
			// room to preserve it whole — truncate the open tag itself (and any
			// following content) to the hard limit. The result is a partial
			// frame, but it never exceeds maxSkillOutputSize.
			if contentStart > maxSkillOutputSize {
				contentStart = maxSkillOutputSize
				dropFrame = true
			}
		}
		cut := snapToLine(body, contentStart, min(ceiling, len(body)))
		// dropFrame is set only when the open tag alone overflowed: emit the
		// truncated-open-tag prefix with no note/closeTag.
		if dropFrame {
			return body[:cut]
		}
		// When ceiling was raised to maxSkillOutputSize (openTag too long for
		// note+closeTag but still within the hard limit), emit just the
		// truncated body without note/closeTag.
		if ceiling == maxSkillOutputSize {
			return body[:cut]
		}
		return body[:cut] + truncationNote + closeTag
	}

	if len(skill.Assets) == 0 {
		return body
	}
	// Cheap pre-check: if the body alone already leaves no room for even the
	// <skill_assets> open tag, the rendered block can never fit — skip the
	// render entirely (it would build a ~140KB string only to be discarded).
	// Keep the complete body and append the truncation note (subject to the
	// same hard-limit guard as the post-render overflow branch below).
	if len(body)+len(assetBlockOpen(skill.Name))+len(assetBlockClose) > maxSkillOutputSize {
		return appendTruncationNote(body)
	}
	assets := renderAssets(skill)
	if len(body)+len(assets) > maxSkillOutputSize {
		// Keep the complete body, drop the assets block, and note the truncation.
		return appendTruncationNote(body)
	}
	return body + assets
}

// appendTruncationNote returns body with the truncation note appended, never
// exceeding maxSkillOutputSize. When body alone is so close to the limit that
// the note would not fit, body is truncated (on a rune + line boundary) to
// make room for the note. The result is always <= maxSkillOutputSize bytes.
func appendTruncationNote(body string) string {
	const truncationNote = "\n(output truncated)"
	if len(body)+len(truncationNote) <= maxSkillOutputSize {
		return body + truncationNote
	}
	cut := snapToLine(body, 0, maxSkillOutputSize-len(truncationNote))
	return body[:cut] + truncationNote
}

// snapToLine snaps a cut index down to a UTF-8 rune boundary and then back to
// the preceding newline, so a truncation never splits a multi-byte rune or a
// line. floor is the minimum cut (e.g. the end of an open tag that must be
// preserved); ceiling is the maximum cut. The returned cut is in [floor,
// len(body)] and never exceeds ceiling. Both FormatOutput's body-overflow
// branch and appendTruncationNote share this so the two truncation sites cannot
// drift.
func snapToLine(body string, floor, ceiling int) int {
	if floor < 0 {
		floor = 0
	}
	cut := ceiling
	if cut > len(body) {
		cut = len(body)
	}
	for cut > floor && !utf8.RuneStart(body[cut]) {
		cut--
	}
	if cut > floor {
		if nl := strings.LastIndexByte(body[floor:cut], '\n'); nl >= 0 {
			cut = floor + nl + 1
		}
	}
	if cut < floor {
		cut = floor
	}
	if cut > len(body) {
		cut = len(body)
	}
	return cut
}

// assetBlockOpen is the opening tag of the <skill_assets> block; pre-computed
// here so the pre-check in FormatOutput can estimate the minimum overhead of
// rendering assets without actually rendering them.
func assetBlockOpen(name string) string {
	return "\n\n" + fmt.Sprintf("<skill_assets name=%q>\n", name)
}

const assetBlockClose = "</skill_assets>"

// renderAssets builds the <skill_assets> block for a skill, rendering asset
// paths relative to the skill directory.
func renderAssets(skill Skill) string {
	var a strings.Builder
	a.WriteString("\n\n")
	fmt.Fprintf(&a, "<skill_assets name=%q>\n", skill.Name)
	for _, asset := range skill.Assets {
		rel := asset.Name // already skill-relative from loadAssets
		if rel == "" {
			// Surface the basename when no relative name is available; never
			// fall back to the absolute path, which would leak the home dir.
			rel = filepath.Base(asset.Path)
		}
		fmt.Fprintf(&a, "- %s (%s)\n", rel, humanSize(asset.Size))
	}
	a.WriteString("</skill_assets>")
	return a.String()
}

// humanSize formats a byte count as a human-readable string.
func humanSize(bytes int64) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%d B", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
	}
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
// path (and its FileInfo) only if it stays within rootReal (the already-
// symlink-resolved skills root). This stops a symlinked SKILL.md — or a
// symlinked skill directory — from making the permission-allow skill tool read
// files outside the skills root. ok=false also covers a missing path or one
// that is a directory/non-regular. The FileInfo is the Lstat result of the
// resolved real path, so callers do not need to re-stat it.
func confineSkillPath(rootReal string, manifestPath string) (string, os.FileInfo, bool) {
	real, err := filepath.EvalSymlinks(manifestPath)
	if err != nil {
		return "", nil, false
	}
	rel, err := filepath.Rel(rootReal, real)
	if err != nil {
		return "", nil, false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", nil, false
	}
	// Only read regular files. A non-regular in-root target (directory, FIFO,
	// device, socket) named SKILL.md would otherwise make os.ReadFile block
	// indefinitely — skill is a permission-allow tool over a user-controlled dir.
	info, err := os.Lstat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", nil, false
	}
	return real, info, true
}

// loadAssets discovers non-SKILL.md files in a skill directory, RECURSIVELY.
// It returns metadata (name, path, size) for each regular file that is not
// hidden (does not start with "."), not SKILL.md itself, and not larger than
// maxAssetSize. Each file path is confined to the skills root via
// confineSkillPath so symlinked assets pointing outside the root are silently
// skipped. Name is the path relative to the skill directory (so the model never
// sees the user's home directory); Path is the same relative path, kept absolute
// relative to skillDir for callers that need to open the file. Discovery
// recurses into arbitrarily deep subdirectories so every file fscopy.CopyTree
// installs (at any depth) stays discoverable (issue #584 — "contents of
// subdirectories"). The traversal is bounded by maxAssetCount (primary) and
// maxVisitedEntries (total nodes examined); there is no depth cap, so assets
// installed at any depth are kept discoverable.
func loadAssets(rootReal string, skillDir string) []Asset {
	// Resolve the skill dir through symlinks so the relative paths computed
	// below share a base with the EvalSymlinks-resolved real paths returned by
	// confineSkillPath — otherwise a macOS /var → /private/var symlink makes
	// filepath.Rel emit a ../../../../ escape that leaks the absolute path.
	relBase := skillDir
	if resolved, err := filepath.EvalSymlinks(skillDir); err == nil {
		relBase = resolved
	}
	var assets []Asset
	var visited int
	appendAssetsRecursive(rootReal, relBase, relBase, &assets, &visited)
	// Deterministic order: sort by the skill-relative name so the <skill_assets>
	// list is stable across loads regardless of readdir ordering.
	sort.Slice(assets, func(i, j int) bool { return assets[i].Name < assets[j].Name })
	return assets
}

// appendAssetsRecursive walks dir, appending a regular-file Asset for each
// eligible entry. relBase is the skill directory (the root relative paths are
// computed against); dir is the current directory being walked. visited tracks
// the total number of filesystem entries examined and is checked against
// maxVisitedEntries to bound discovery cost even when most examined entries are
// not eligible assets. Discovery is bounded by maxAssetCount (primary) and
// maxVisitedEntries (total nodes), so a huge skill tree with mostly empty
// directories cannot stall skill load. There is no depth cap: directory
// symlinks are never traversed (entry.IsDir() is false for symlinks, and
// confineSkillPath rejects escaped paths via EvalSymlinks + IsRegular), so a
// real filesystem cannot form a traversal cycle, and every file CopyTree
// installs at any depth stays discoverable.
func appendAssetsRecursive(rootReal, relBase, dir string, assets *[]Asset, visited *int) {
	// Stop recursing if we've hit the asset count cap — further entries would
	// be discarded anyway, so skip the I/O.
	if len(*assets) >= maxAssetCount {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		// Re-check on each iteration so a large directory stops early.
		if len(*assets) >= maxAssetCount {
			return
		}
		// Stop if we've examined too many entries total — a huge tree with
		// mostly empty/hidden directories could otherwise stall discovery.
		if *visited >= maxVisitedEntries {
			return
		}
		*visited++
		name := entry.Name()
		// Skip hidden files/dirs (.git, .env, .DS_Store, etc.) at every level.
		if strings.HasPrefix(name, ".") {
			continue
		}
		candidate := filepath.Join(dir, name)
		if entry.IsDir() {
			// Recurse into real subdirectories. A symlink-to-dir is NOT a dir
			// (entry.IsDir() is false for symlinks), so it falls through to
			// confineSkillPath, which rejects it via EvalSymlinks + IsRegular.
			appendAssetsRecursive(rootReal, relBase, candidate, assets, visited)
			continue
		}
		// Skip SKILL.md (already loaded as Content). Case-insensitive so a
		// case-insensitive filesystem (macOS/Windows) can't surface it twice.
		if strings.EqualFold(name, skillFileName) {
			continue
		}
		realPath, info, ok := confineSkillPath(rootReal, candidate)
		if !ok {
			continue
		}
		// confineSkillPath already proved the resolved path is a regular file
		// via os.Lstat and returned that FileInfo — no second stat here.
		if info.Size() > maxAssetSize {
			continue
		}
		rel, err := filepath.Rel(relBase, realPath)
		if err != nil {
			continue
		}
		*assets = append(*assets, Asset{
			Name: filepath.ToSlash(rel),
			Path: realPath,
			Size: info.Size(),
		})
	}
}

// load is the shared scanner behind Load and Duplicates: it parses every
// SKILL.md, deduplicates by frontmatter name (first directory wins) and reports
// the dropped collisions.
func load(dir string) ([]Skill, []DuplicateName, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return []Skill{}, nil, nil
	}
	// Converge any interrupted replace in this root before scanning it. A kill
	// or power loss between the two renames of a swap leaves the canonical
	// install absent and the old tree stranded under a backup name the lockfile
	// no longer matches; RecoverPending restores/commits it so discovery never
	// observes a half-replaced root.
	if err := RecoverPending(dir); err != nil {
		return nil, nil, err
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
		realPath, _, ok := confineSkillPath(rootReal, manifestPath)
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
		skill.Dir = filepath.Dir(absPath)
		skill.Assets = loadAssets(rootReal, filepath.Dir(absPath))
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
		skill.Assets = nil
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
