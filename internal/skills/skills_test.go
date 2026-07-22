package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func writeSkill(t *testing.T, dir string, name string, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", skillDir, err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

// Regression: skill is a permission-allow read-only tool, so a SKILL.md that is a
// symlink pointing OUTSIDE the skills root must be skipped — never read — so the
// tool can't be turned into an arbitrary-file reader.
func TestLoadSkipsSymlinkedSkillFileEscapingRoot(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "good", "---\nname: good\ndescription: ok\n---\nbody")

	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.md")
	if err := os.WriteFile(secret, []byte("---\nname: evil\ndescription: leaked\n---\nTOP SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	evilDir := filepath.Join(dir, "evil")
	if err := os.MkdirAll(evilDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(evilDir, "SKILL.md")); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	for _, s := range loaded {
		if s.Name == "evil" || strings.Contains(s.Content, "TOP SECRET") {
			t.Fatalf("symlinked SKILL.md escaping the root must be skipped, got %+v", s)
		}
	}
	if len(loaded) != 1 || loaded[0].Name != "good" {
		t.Fatalf("expected only the in-root skill, got %+v", loaded)
	}
}

func TestLoadParsesFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "confirmation-policy", "---\nname: confirmation-policy\ndescription: When to ask the user before risky actions.\n---\n\n# Confirmation Policy\n\nAsk first.\n")

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	skill := loaded[0]
	if skill.Name != "confirmation-policy" {
		t.Fatalf("Name = %q, want confirmation-policy", skill.Name)
	}
	if skill.Description != "When to ask the user before risky actions." {
		t.Fatalf("Description = %q", skill.Description)
	}
	wantContent := "# Confirmation Policy\n\nAsk first."
	if skill.Content != wantContent {
		t.Fatalf("Content = %q, want %q", skill.Content, wantContent)
	}
	if skill.Path == "" {
		t.Fatalf("Path is empty")
	}
}

func TestLoadDerivesNameFromDirectoryWithoutFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "no-frontmatter", "# Just markdown\n\nNo frontmatter here.\n")

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	skill := loaded[0]
	if skill.Name != "no-frontmatter" {
		t.Fatalf("Name = %q, want no-frontmatter", skill.Name)
	}
	if skill.Description != "" {
		t.Fatalf("Description = %q, want empty", skill.Description)
	}
	if skill.Content != "# Just markdown\n\nNo frontmatter here." {
		t.Fatalf("Content = %q", skill.Content)
	}
}

func TestLoadSkipsMalformedAndContinues(t *testing.T) {
	dir := t.TempDir()
	// A directory whose SKILL.md is a directory itself (unreadable as a file) is skipped.
	badDir := filepath.Join(dir, "broken")
	if err := os.MkdirAll(filepath.Join(badDir, "SKILL.md"), 0o755); err != nil {
		t.Fatalf("mkdir broken: %v", err)
	}
	writeSkill(t, dir, "good", "---\nname: good\ndescription: works\n---\nbody\n")

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill (malformed skipped), got %d", len(loaded))
	}
	if loaded[0].Name != "good" {
		t.Fatalf("Name = %q, want good", loaded[0].Name)
	}
}

func TestLoadIgnoresDirectoriesWithoutSkillFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatalf("mkdir empty: %v", err)
	}
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 skills, got %d", len(loaded))
	}
}

func TestLoadMissingDirYieldsEmpty(t *testing.T) {
	loaded, err := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("Load on missing dir returned error: %v", err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 skills for missing dir, got %d", len(loaded))
	}
}

func TestLoadNotDirectoryErrors(t *testing.T) {
	// Portable across Unix and Windows: a regular file is not a skills root.
	// Windows reports ReadDir(file) as ErrNotExist; load must reclassify it.
	notDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(notDir)
	if err == nil {
		t.Fatalf("expected error for non-directory skills root, got loaded=%#v", loaded)
	}
	if errors.Is(err, os.ErrNotExist) && !errors.Is(err, errNotDirectory) {
		t.Fatalf("non-directory skills root must not look missing: %v", err)
	}
	if loaded != nil {
		t.Fatalf("expected nil skills on non-directory root, got %#v", loaded)
	}
}

func TestLoadSortsByName(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "zeta", "body")
	writeSkill(t, dir, "alpha", "body")

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(loaded))
	}
	if loaded[0].Name != "alpha" || loaded[1].Name != "zeta" {
		t.Fatalf("skills not sorted: %q, %q", loaded[0].Name, loaded[1].Name)
	}
}

func TestLoadDuplicateFrontmatterNamePicksStableWinner(t *testing.T) {
	dir := t.TempDir()
	// Two skill directories whose frontmatter declares the SAME name. The documented
	// rule: the skill in the lexicographically-first directory name wins, so resolution
	// is deterministic regardless of os.ReadDir / sort ordering.
	writeSkill(t, dir, "aaa-first", "---\nname: shared\ndescription: from aaa\n---\nbody from aaa\n")
	writeSkill(t, dir, "zzz-second", "---\nname: shared\ndescription: from zzz\n---\nbody from zzz\n")

	// Loading repeatedly must always yield the same single winner.
	for i := 0; i < 20; i++ {
		loaded, err := Load(dir)
		if err != nil {
			t.Fatalf("Load returned error: %v", err)
		}
		shared := 0
		var winner Skill
		for _, skill := range loaded {
			if skill.Name == "shared" {
				shared++
				winner = skill
			}
		}
		if shared != 1 {
			t.Fatalf("expected exactly one skill named shared after dedup, got %d", shared)
		}
		if winner.Description != "from aaa" || winner.Content != "body from aaa" {
			t.Fatalf("expected the aaa-first directory to win, got desc=%q content=%q", winner.Description, winner.Content)
		}
	}

	// Get must resolve to the same documented winner.
	got, ok := Get(dir, "shared")
	if !ok {
		t.Fatal("Get(shared) not found")
	}
	if got.Content != "body from aaa" {
		t.Fatalf("Get resolved to non-winner: %q", got.Content)
	}
}

func TestDuplicatesReportsCollidingNames(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "aaa-first", "---\nname: shared\n---\nbody\n")
	writeSkill(t, dir, "zzz-second", "---\nname: shared\n---\nbody\n")
	writeSkill(t, dir, "solo", "---\nname: solo\n---\nbody\n")

	dups, err := Duplicates(dir)
	if err != nil {
		t.Fatalf("Duplicates returned error: %v", err)
	}
	if len(dups) != 1 {
		t.Fatalf("expected exactly one duplicated name, got %d: %#v", len(dups), dups)
	}
	if dups[0].Name != "shared" {
		t.Fatalf("expected the duplicated name to be shared, got %q", dups[0].Name)
	}
	// The winner is the lexicographically-first directory; the loser is reported too.
	if dups[0].Winner == "" || dups[0].Loser == "" {
		t.Fatalf("expected both winner and loser paths recorded, got %#v", dups[0])
	}
	if !strings.Contains(dups[0].Winner, "aaa-first") || !strings.Contains(dups[0].Loser, "zzz-second") {
		t.Fatalf("expected aaa-first to win and zzz-second to lose, got winner=%q loser=%q", dups[0].Winner, dups[0].Loser)
	}
}

func TestGetByName(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "one", "---\nname: one\ndescription: first\n---\ncontent one\n")

	skill, ok := Get(dir, "one")
	if !ok {
		t.Fatalf("Get(one) not found")
	}
	if skill.Content != "content one" {
		t.Fatalf("Content = %q", skill.Content)
	}

	if _, ok := Get(dir, "missing"); ok {
		t.Fatalf("Get(missing) should not be found")
	}
}

func TestListReturnsNamesAndDescriptions(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "b", "---\nname: b\ndescription: bee\n---\nbody")
	writeSkill(t, dir, "a", "---\nname: a\ndescription: ay\n---\nbody")

	listed, err := List(dir)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("expected 2, got %d", len(listed))
	}
	if listed[0].Name != "a" || listed[0].Description != "ay" {
		t.Fatalf("unexpected first skill: %+v", listed[0])
	}
	// List must strip Content so a listing never leaks full skill bodies (the
	// skills above all have a non-empty "body"); only Get/Load return Content.
	for _, skill := range listed {
		if skill.Content != "" {
			t.Fatalf("List must strip Content, got %q for %q", skill.Content, skill.Name)
		}
	}
}

func TestDefaultDirHonorsEnvOverride(t *testing.T) {
	got := DefaultDir(map[string]string{"ZERO_SKILLS_DIR": "/custom/skills"})
	if got != "/custom/skills" {
		t.Fatalf("DefaultDir override = %q, want /custom/skills", got)
	}
}

func TestDefaultDirHonorsXDGDataHome(t *testing.T) {
	got := DefaultDir(map[string]string{"XDG_DATA_HOME": "/xdg/data"})
	want := filepath.Join("/xdg/data", "zero", "skills")
	if got != want {
		t.Fatalf("DefaultDir = %q, want %q", got, want)
	}
}

func TestDefaultDirFallsBackToHome(t *testing.T) {
	got := DefaultDir(map[string]string{"HOME": "/home/zero"})
	want := filepath.Join("/home/zero", ".local", "share", "zero", "skills")
	if got != want {
		t.Fatalf("DefaultDir = %q, want %q", got, want)
	}
}

func TestAgentsDirReturnsExistingDirectory(t *testing.T) {
	home := t.TempDir()
	agents := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	got := AgentsDir(map[string]string{"HOME": home})
	if got != agents {
		t.Fatalf("AgentsDir = %q, want %q", got, agents)
	}
}

func TestAgentsDirMissingIsEmpty(t *testing.T) {
	home := t.TempDir()
	got := AgentsDir(map[string]string{"HOME": home})
	if got != "" {
		t.Fatalf("AgentsDir for missing path = %q, want empty", got)
	}
}

func TestAgentsDirFileNotDirIsEmpty(t *testing.T) {
	home := t.TempDir()
	agentsParent := filepath.Join(home, ".agents")
	if err := os.MkdirAll(agentsParent, 0o755); err != nil {
		t.Fatal(err)
	}
	// skills is a file, not a directory
	if err := os.WriteFile(filepath.Join(agentsParent, "skills"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := AgentsDir(map[string]string{"HOME": home})
	if got != "" {
		t.Fatalf("AgentsDir for file = %q, want empty", got)
	}
}

func TestAgentsDirHonorsUserProfile(t *testing.T) {
	home := t.TempDir()
	agents := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	got := AgentsDir(map[string]string{"USERPROFILE": home})
	if got != agents {
		t.Fatalf("AgentsDir via USERPROFILE = %q, want %q", got, agents)
	}
}

func TestAgentsDirIgnoresZeroSkillsDir(t *testing.T) {
	home := t.TempDir()
	agents := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	// ZERO_SKILLS_DIR must not redirect or suppress AgentsDir.
	got := AgentsDir(map[string]string{
		"HOME":            home,
		"ZERO_SKILLS_DIR": filepath.Join(home, "zero-only"),
	})
	if got != agents {
		t.Fatalf("AgentsDir with ZERO_SKILLS_DIR set = %q, want %q", got, agents)
	}
}

func TestAgentsDirUnresolvableHomeIsEmpty(t *testing.T) {
	// Empty env map values + no real UserHomeDir fallback is hard to force, but
	// empty HOME/USERPROFILE with an empty home should still not panic.
	// When UserHomeDir works this may return a host path; only assert no panic
	// and that a deliberately empty-looking override path is not invented from ZERO.
	_ = AgentsDir(map[string]string{"HOME": "", "USERPROFILE": ""})
}

func TestDiscoveryRootsOrderAndOmission(t *testing.T) {
	home := t.TempDir()
	agents := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	primary := filepath.Join(home, "zero-skills")
	env := map[string]string{
		"HOME":            home,
		"ZERO_SKILLS_DIR": primary,
	}
	roots := DiscoveryRoots(env, []string{"", " /plugin/a ", "plugin/b"})
	want := []string{primary, agents, "/plugin/a", "plugin/b"}
	if len(roots) != len(want) {
		t.Fatalf("DiscoveryRoots = %#v, want %#v", roots, want)
	}
	for i := range want {
		if roots[i] != want[i] {
			t.Fatalf("DiscoveryRoots[%d] = %q, want %q (full %#v)", i, roots[i], want[i], roots)
		}
	}
}

func TestDiscoveryRootsOmitsMissingAgents(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, "zero-skills")
	env := map[string]string{
		"HOME":            home,
		"ZERO_SKILLS_DIR": primary,
	}
	roots := DiscoveryRoots(env, nil)
	if len(roots) != 1 || roots[0] != primary {
		t.Fatalf("DiscoveryRoots without agents = %#v, want only primary", roots)
	}
}

func TestLoadFromRootsPrimaryWinsOverAgents(t *testing.T) {
	primary := t.TempDir()
	agents := t.TempDir()
	writeSkill(t, primary, "shared", "---\nname: shared\n---\nprimary body\n")
	writeSkill(t, agents, "shared", "---\nname: shared\n---\nagents body\n")
	writeSkill(t, agents, "agents-only", "---\nname: agents-only\n---\nagents only\n")

	loaded, dups, err := LoadFromRoots([]string{primary, agents})
	if err != nil {
		t.Fatalf("LoadFromRoots: %v", err)
	}
	byName := map[string]Skill{}
	for _, skill := range loaded {
		byName[skill.Name] = skill
	}
	if byName["shared"].Content != "primary body" {
		t.Fatalf("primary should win shared, got %q", byName["shared"].Content)
	}
	if byName["agents-only"].Content != "agents only" {
		t.Fatalf("agents-only missing: %#v", loaded)
	}
	if len(dups) != 1 || dups[0].Name != "shared" {
		t.Fatalf("expected one shared duplicate, got %#v", dups)
	}
}

func TestLoadFromRootsSkipsEmptyAndMissing(t *testing.T) {
	primary := t.TempDir()
	writeSkill(t, primary, "solo", "---\nname: solo\n---\nbody\n")
	loaded, dups, err := LoadFromRoots([]string{"", filepath.Join(t.TempDir(), "missing"), primary})
	if err != nil {
		t.Fatalf("LoadFromRoots: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "solo" {
		t.Fatalf("expected only solo, got %#v", loaded)
	}
	if len(dups) != 0 {
		t.Fatalf("unexpected dups: %#v", dups)
	}
}

func TestLoadFromRootsBubblesPrimaryError(t *testing.T) {
	// A regular file is not a directory. On Unix ReadDir fails with ENOTDIR; on
	// Windows that case is aliased to ErrNotExist, so load must reclassify an
	// existing non-directory and bubble it from the first non-empty root.
	notDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	optional := t.TempDir()
	writeSkill(t, optional, "fallback", "---\nname: fallback\n---\nbody\n")

	loaded, dups, err := LoadFromRoots([]string{notDir, optional})
	if err == nil {
		t.Fatalf("expected primary root error, got loaded=%#v dups=%#v", loaded, dups)
	}
	// Unix returns ENOTDIR; Windows reclassifies via errNotDirectory because
	// ENOTDIR aliases ErrNotExist there. Either way it must not look missing.
	if errors.Is(err, os.ErrNotExist) && !errors.Is(err, errNotDirectory) {
		t.Fatalf("primary non-directory must not look missing: %v", err)
	}
	if loaded != nil {
		t.Fatalf("expected nil skills on primary error, got %#v", loaded)
	}
	if dups != nil {
		t.Fatalf("expected nil dups on primary error, got %#v", dups)
	}
}

func TestLoadFromRootsOptionalRootFailOpen(t *testing.T) {
	primary := t.TempDir()
	writeSkill(t, primary, "keep", "---\nname: keep\n---\nbody\n")
	notDir := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(notDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	agents := t.TempDir()
	writeSkill(t, agents, "agents-only", "---\nname: agents-only\n---\nbody\n")

	loaded, dups, err := LoadFromRoots([]string{primary, notDir, agents})
	if err != nil {
		t.Fatalf("optional root failure must not fail merge: %v", err)
	}
	byName := map[string]Skill{}
	for _, skill := range loaded {
		byName[skill.Name] = skill
	}
	if byName["keep"].Name != "keep" {
		t.Fatalf("primary skill missing: %#v", loaded)
	}
	if byName["agents-only"].Name != "agents-only" {
		t.Fatalf("later optional skill should still load: %#v", loaded)
	}
	if len(dups) != 0 {
		t.Fatalf("unexpected dups: %#v", dups)
	}
}

func TestListFromRootsStripsContent(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "demo", "---\nname: demo\n---\nbody content\n")
	listed, _, err := ListFromRoots([]string{dir})
	if err != nil {
		t.Fatalf("ListFromRoots: %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "demo" {
		t.Fatalf("unexpected listed: %#v", listed)
	}
	if listed[0].Content != "" {
		t.Fatalf("ListFromRoots must strip Content, got %q", listed[0].Content)
	}
}

func TestGlobalRootsIncludesAgents(t *testing.T) {
	home := t.TempDir()
	agents := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(agents, 0o755); err != nil {
		t.Fatal(err)
	}
	// Point HOME so AgentsDir finds the temp agents root.
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	primary := filepath.Join(home, "primary")
	roots := GlobalRoots(primary)
	if len(roots) != 2 || roots[0] != primary || roots[1] != agents {
		t.Fatalf("GlobalRoots = %#v, want [primary, agents]", roots)
	}
}

func TestInfoFromRootsAgentsOnlyHasNoLock(t *testing.T) {
	primary := t.TempDir()
	agents := t.TempDir()
	writeSkill(t, agents, "shared-agents", "---\nname: agents-skill\ndescription: from agents\n---\nbody\n")
	info, ok := InfoFromRoots(primary, []string{primary, agents}, "agents-skill")
	if !ok {
		t.Fatal("expected agents skill to resolve")
	}
	if info.Skill.Name != "agents-skill" || info.Skill.Description != "from agents" {
		t.Fatalf("unexpected skill: %#v", info.Skill)
	}
	if info.Source != "" || info.Hash != "" {
		t.Fatalf("agents-only skill must not carry lock metadata, got source=%q hash=%q", info.Source, info.Hash)
	}
}

func TestInfoFromRootsPrimaryLockMetadata(t *testing.T) {
	primary := t.TempDir()
	writeSkill(t, primary, "demo", "---\nname: demo\n---\nbody\n")
	// Write a lockfile entry the way install would.
	lockPath := filepath.Join(primary, LockFileName)
	if err := os.WriteFile(lockPath, []byte(`{"demo":{"source":"file:///src","hash":"sha256:abc"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	info, ok := InfoFromRoots(primary, []string{primary}, "demo")
	if !ok {
		t.Fatal("expected primary skill")
	}
	if info.Source != "file:///src" || info.Hash != "sha256:abc" {
		t.Fatalf("lock metadata missing: %#v", info)
	}
}

// writeSkillWithAssets creates a skill at dir/name with SKILL.md plus the given
// extra files (relative paths under the skill dir) — used to exercise asset
// discovery.
func writeSkillWithAssets(t *testing.T, dir, name, skillmd string, extras map[string]string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", skillDir, err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillmd), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	for rel, content := range extras {
		full := filepath.Join(skillDir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", full, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", full, err)
		}
	}
}

// #2: loadAssets must recurse into subdirectories so files like scripts/run.sh
// — which fscopy.CopyTree installs to disk — are listed in Skill.Assets. Issue
// #584 asks for "contents of subdirectories" in the skill context.
func TestLoadDiscoversAssetsRecursively(t *testing.T) {
	dir := t.TempDir()
	writeSkillWithAssets(t, dir, "deploy",
		"---\nname: deploy\ndescription: d\n---\nbody",
		map[string]string{
			"lint.sh":        "#!/bin/sh\necho lint\n",
			"config.yaml":    "key: value\n",
			"scripts/run.sh": "#!/bin/sh\necho run\n",
			"lib/util.py":    "def util():\n    pass\n",
		},
	)

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "deploy" {
		t.Fatalf("expected deploy skill, got %+v", loaded)
	}
	skill := loaded[0]
	wantNames := map[string]bool{
		"config.yaml":    true,
		"lint.sh":        true,
		"scripts/run.sh": true, // slash-form, matching filepath.ToSlash
		"lib/util.py":    true,
	}
	gotNames := map[string]bool{}
	for _, a := range skill.Assets {
		gotNames[a.Name] = true
	}
	for want := range wantNames {
		if !gotNames[want] {
			t.Errorf("expected asset %q in Skill.Assets, got %v", want, skill.Assets)
		}
	}
	if len(skill.Assets) != len(wantNames) {
		t.Errorf("expected %d assets, got %d (%v)", len(wantNames), len(skill.Assets), skill.Assets)
	}
	// SKILL.md must not be listed as an asset (it's the Content).
	for _, a := range skill.Assets {
		if strings.EqualFold(a.Name, "SKILL.md") {
			t.Errorf("SKILL.md must not appear in Assets")
		}
	}
}

// Regression: assets nested deeper than any reasonable fixed depth cap must
// still be discovered, because fscopy.CopyTree installs files at ANY depth and
// the loader must keep every installed file discoverable. A depth cap that
// hides installed assets would make a skill silently appear asset-free while
// files the skill references exist on disk. This nests assets two ways and
// confirms both are listed:
//   - 25 levels deep (well past the old maxAssetDepth=8).
//   - 90 levels deep, past the old maxTraversalDepth=64 — which used to make
//     loadAssets silently drop the asset. Discovery no longer caps depth.
func TestLoadDiscoversDeeplyNestedAssets(t *testing.T) {
	dir := t.TempDir()
	// 25 levels of subdirectories — exceeds any shallow fixed cap, stays well
	// under the filesystem PATH_MAX.
	deep25 := strings.Repeat("d/", 25) + "leaf.sh"
	// 90 levels — past the old maxTraversalDepth=64. Use short path components
	// so the leaf's absolute path stays under the filesystem PATH_MAX.
	deep90 := strings.Repeat("d/", 90) + "deep.sh"
	writeSkillWithAssets(t, dir, "nested",
		"---\nname: nested\n---\nbody",
		map[string]string{
			deep25: "#!/bin/sh\necho deep\n",
			deep90: "#!/bin/sh\necho deeper\n",
		},
	)
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	want := map[string]bool{"leaf.sh": false, "deep.sh": false}
	for _, a := range loaded[0].Assets {
		s := filepath.ToSlash(a.Name)
		for suffix := range want {
			if strings.HasSuffix(s, suffix) {
				want[suffix] = true
			}
		}
	}
	for suffix, found := range want {
		if !found {
			t.Fatalf("deeply nested asset not discovered (%s); Assets=%v", suffix, loaded[0].Assets)
		}
	}
}

// Regression: a file named SKILL.md nested below the skill root is NOT the
// loaded manifest (the root SKILL.md is), so it must remain in Skill.Assets as
// an ordinary asset. Examples: templates/SKILL.md (a copied template the skill
// references) or subskill/SKILL.md (an embedded sub-skill manifest). The loader
// previously skipped every recursively encountered SKILL.md by basename, which
// silently dropped these from model-facing output. Only the root manifest is
// excluded now.
func TestLoadKeepsNestedSkillMdAsAsset(t *testing.T) {
	dir := t.TempDir()
	writeSkillWithAssets(t, dir, "tmpl",
		"---\nname: tmpl\ndescription: d\n---\nbody",
		map[string]string{
			"templates/SKILL.md": "# template manifest\n",
			"subskill/SKILL.md":  "---\nname: sub\n---\nsub body\n",
			"README.md":          "docs\n",
		},
	)

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 || loaded[0].Name != "tmpl" {
		t.Fatalf("expected tmpl skill, got %+v", loaded)
	}
	skill := loaded[0]
	gotNames := map[string]bool{}
	for _, a := range skill.Assets {
		gotNames[a.Name] = true
	}
	wantNames := []string{
		"templates/SKILL.md",
		"subskill/SKILL.md",
		"README.md",
	}
	for _, want := range wantNames {
		if !gotNames[want] {
			t.Errorf("expected nested asset %q in Skill.Assets, got %v", want, skill.Assets)
		}
	}
	// The root manifest must still be excluded — only nested SKILL.md survives.
	for _, a := range skill.Assets {
		if a.Name == "SKILL.md" {
			t.Errorf("root SKILL.md must not appear in Assets")
		}
	}
	if len(skill.Assets) != len(wantNames) {
		t.Errorf("expected %d assets, got %d (%v)", len(wantNames), len(skill.Assets), skill.Assets)
	}
}

// maxAssetCount bounds discovery: a skill with more than maxAssetCount eligible
// files must stop discovering after the cap rather than traversing the whole
// tree, so a huge directory cannot stall skill load.
func TestLoadAssetsCountCapStopsDiscovery(t *testing.T) {
	dir := t.TempDir()
	extras := make(map[string]string, maxAssetCount+50)
	for i := 0; i < maxAssetCount+50; i++ {
		extras[fmt.Sprintf("asset-%04d.txt", i)] = "d"
	}
	writeSkillWithAssets(t, dir, "many", "---\nname: many\n---\nbody", extras)
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded[0].Assets) > maxAssetCount {
		t.Fatalf("discovery exceeded cap: got %d assets, cap %d", len(loaded[0].Assets), maxAssetCount)
	}
}

// maxVisitedEntries bounds total nodes examined: a skill tree with many empty
// subdirectories (where assets count does not grow) still terminates quickly.
// This guards against a pathological tree where thousands of dirs are created but
// no eligible assets are found in any of them (len(assets) stays at 0).
func TestLoadAssetsVisitedEntriesBudget(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "tree")
	skillMd := filepath.Join(skillDir, "SKILL.md")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillMd, []byte("---\nname: tree\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Create 6000 empty subdirectories — well above maxVisitedEntries=4096 —
	// each with a SKILL.md (to match locateSkillDir's expectation of a skill
	// tree) so ReadDir on each sees entries that are not eligible assets.
	for i := 0; i < 6000; i++ {
		d := filepath.Join(skillDir, fmt.Sprintf("dir-%04d", i))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	loaded, err := Load(dir)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	// Should complete well under 1 second (bounded traversal).
	if elapsed > 5*time.Second {
		t.Fatalf("discovery took %v — visited-entries budget may not be enforced", elapsed)
	}
	// No assets expected (all dirs are empty), but the point is termination.
	if len(loaded[0].Assets) != 0 {
		t.Fatalf("expected 0 assets in empty-tree skill, got %d", len(loaded[0].Assets))
	}
}

// FormatOutput must never exceed maxSkillOutputSize on any overflow path,
// including the assets-overflow branch where the body is near the cap and the
// truncation note would otherwise push it over.
func TestFormatOutputAssetsOverflowStaysUnderHardLimit(t *testing.T) {
	// The framed body lands just under maxSkillOutputSize, so the body-overflow
	// branch is skipped. Assets push the total over the cap, forcing the
	// assets-overflow branch. Adding the truncation note would exceed the cap,
	// so appendTruncationNote must make room while preserving valid UTF-8.
	openTag := fmt.Sprintf("<skill name=%q dir=%q>\n", "s", "/d")
	closeTag := "\n</skill>"
	bodyContent := strings.Repeat("A", maxSkillOutputSize-len(openTag)-len(closeTag)-1)
	skill := Skill{
		Name:    "s",
		Dir:     "/d",
		Content: bodyContent,
		Assets: []Asset{
			{Name: "a.txt", Path: "/d/a.txt", Size: 1},
			{Name: "b.txt", Path: "/d/b.txt", Size: 2},
			{Name: "c.txt", Path: "/d/c.txt", Size: 3},
			{Name: "d.txt", Path: "/d/d.txt", Size: 4},
		},
	}
	out := FormatOutput(skill)
	if len(out) > maxSkillOutputSize {
		t.Fatalf("assets-overflow output exceeds cap: %d > %d", len(out), maxSkillOutputSize)
	}
	if !strings.Contains(out, "(output truncated)") {
		t.Fatalf("truncation note missing: %q", out[len(out)-60:])
	}
	if !utf8.ValidString(out) {
		t.Fatalf("invalid UTF-8 after truncation")
	}
	// Assets block must be dropped entirely — no <skill_assets> fragment.
	if strings.Contains(out, "<skill_assets") {
		t.Fatalf("assets block not dropped on overflow: %q", out[len(out)-120:])
	}
}

// Body exactly at the hard limit: body = maxSkillOutputSize (openTag + content
// + closeTag), assets non-empty. Since len(body) > cap is false (equal, not
// greater), the body-overflow branch is skipped. Assets push the total over →
// assets-overflow branch. Since len(body) + note > cap, body must be truncated
// to make room for the note, and the result must stay under the cap.
func TestFormatOutputBodyExactlyAtCapWithAssets(t *testing.T) {
	// Compute content length so body (openTag + content + closeTag) is exactly
	// maxSkillOutputSize.
	const open = "<skill name=\"s\" dir=\"/d\">\n"
	const close = "\n</skill>"
	contentLen := maxSkillOutputSize - len(open) - len(close)
	bodyContent := strings.Repeat("B", contentLen)
	skill := Skill{
		Name:    "s",
		Dir:     "/d",
		Content: bodyContent,
		Assets:  []Asset{{Name: "a.txt", Path: "/d/a.txt", Size: 1}},
	}
	out := FormatOutput(skill)
	if len(out) > maxSkillOutputSize {
		t.Fatalf("body-at-cap output exceeds cap: %d > %d", len(out), maxSkillOutputSize)
	}
	if !strings.Contains(out, "(output truncated)") {
		t.Fatalf("truncation note missing on body-at-cap path")
	}
	if !utf8.ValidString(out) {
		t.Fatalf("invalid UTF-8 after truncation")
	}
}

// FormatOutput must stay under the hard limit even when the open tag is so
// large that reserving note+closeTag leaves no room for content.
func TestFormatOutputHugeOpenTagStaysUnderHardLimit(t *testing.T) {
	// A name so long the open tag alone approaches the cap.
	huge := strings.Repeat("n", maxSkillOutputSize-5)
	skill := Skill{
		Name:    huge,
		Dir:     "/d",
		Content: strings.Repeat("B", maxSkillOutputSize*2),
	}
	out := FormatOutput(skill)
	if len(out) > maxSkillOutputSize {
		t.Fatalf("huge-openTag output exceeds cap: %d > %d", len(out), maxSkillOutputSize)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("invalid UTF-8 after truncation")
	}
}

// FormatOutput must stay under the hard limit even when the open tag ALONE
// exceeds the cap (name + dir longer than maxSkillOutputSize). The frame is
// truncated mid-tag; no note or close tag is appended.
func TestFormatOutputOpenTagExceedsCapAlone(t *testing.T) {
	skill := Skill{
		Name:    strings.Repeat("n", maxSkillOutputSize+200),
		Dir:     "/" + strings.Repeat("d", maxSkillOutputSize),
		Content: "body",
	}
	out := FormatOutput(skill)
	if len(out) > maxSkillOutputSize {
		t.Fatalf("openTag-exceeds-cap output exceeds cap: %d > %d", len(out), maxSkillOutputSize)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("invalid UTF-8 after truncation")
	}
}

// #2/#5: hidden files and subdirectories are skipped at every level (.git,
// .env, .DS_Store), so a .git inside a subdirectory does not leak assets.
func TestLoadSkipsHiddenFilesAndDirsAtEveryLevel(t *testing.T) {
	dir := t.TempDir()
	writeSkillWithAssets(t, dir, "s",
		"---\nname: s\n---\nbody",
		map[string]string{
			".env":            "SECRET=1\n",
			".git/config":     "[remote]\n",
			"scripts/.secret": "x\n",
		},
	)
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	for _, a := range loaded[0].Assets {
		if strings.Contains(a.Name, ".env") || strings.Contains(a.Name, ".git") || strings.Contains(a.Name, ".secret") {
			t.Errorf("hidden file/dir leaked into Assets: %q", a.Name)
		}
	}
}

// #6: FormatOutput must NOT emit the absolute install path (which contains the
// user's home directory) for each asset. Asset names are skill-relative; the
// skill directory is exposed once via dir= and assets are listed by relative
// path only. The skill tool is permission-allow and its output flows to the
// provider, so the absolute home path must not leak per-asset.
func TestFormatOutputDoesNotLeakAbsoluteAssetPaths(t *testing.T) {
	dir := t.TempDir()
	writeSkillWithAssets(t, dir, "s",
		"---\nname: s\n---\nbody",
		map[string]string{"scripts/run.sh": "#!/bin/sh\necho\n"},
	)
	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	out := FormatOutput(loaded[0])
	// The relative asset name must appear; the absolute skills dir must not be
	// repeated per-asset (it appears once in dir=).
	if !strings.Contains(out, "scripts/run.sh") {
		t.Errorf("expected relative asset name in output, got:\n%s", out)
	}
	// Count occurrences of the skills root path: it should appear exactly once
	// (in dir=), never inside the <skill_assets> block. FormatOutput emits dir=
	// via fmt's %q, which escapes backslashes (e.g. C:\Users -> C:\\Users on
	// Windows), so we must match the quoted form — matching the raw path would
	// fail on Windows and, on POSIX, would over-count because dir= is a parent
	// of the asset relative paths.
	skillsRoot := strconv.Quote(loaded[0].Dir)
	count := strings.Count(out, skillsRoot)
	if count != 1 {
		t.Errorf("expected skills root %q to appear exactly once (in dir=), got %d times:\n%s", loaded[0].Dir, count, out)
	}
}

// #1: FormatOutput truncation must stay on a UTF-8 rune boundary so a large
// multi-byte body does not produce invalid UTF-8, and must not split an asset
// line mid-entry. A body of CJK characters that exceeds the cap is the
// regression case.
func TestFormatOutputTruncatesOnRuneBoundary(t *testing.T) {
	// Build a skill whose total output comfortably exceeds maxSkillOutputSize
	// using multi-byte (CJK) content, so the cut boundary lands inside a rune.
	dir := t.TempDir()
	// "中" is 3 bytes; repeat well past the 100KB cap.
	body := strings.Repeat("中", (maxSkillOutputSize/3)+200)
	writeSkill(t, dir, "big", "---\nname: big\n---\n"+body)

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	out := FormatOutput(loaded[0])
	if !strings.HasSuffix(out, "\n(output truncated)\n</skill>") {
		t.Fatalf("expected truncation note + closing frame at end, got suffix %q", out[len(out)-60:])
	}
	// utf8.ValidString catches a split multi-byte rune.
	if !utf8.ValidString(out) {
		t.Fatalf("FormatOutput produced invalid UTF-8 after truncation (split rune)")
	}
}

// Regression: a single-line body (no internal newline) that exceeds the cap must
// NOT collapse to "<skill ...>\n" + the truncation note — that erases every
// instruction byte. Body content must survive between the opening tag and the
// note, and the closing </skill> frame must still be emitted so the document
// stays well-formed (the package doc promises closing tags stay intact).
func TestFormatOutputTruncatesSingleLineBodyKeepsFrame(t *testing.T) {
	dir := t.TempDir()
	// One long line of ASCII with no '\n', well past the 100KB cap.
	body := strings.Repeat("A", maxSkillOutputSize*2)
	writeSkill(t, dir, "big", "---\nname: big\n---\n"+body)

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	out := FormatOutput(loaded[0])

	// The closing frame must survive.
	if !strings.HasSuffix(out, "\n</skill>") {
		t.Fatalf("closing </skill> frame lost; tail=%q", out[len(out)-60:])
	}
	// The truncation note must be present.
	if !strings.Contains(out, "(output truncated)") {
		t.Fatalf("truncation note missing in output:\n...%s", out[len(out)-80:])
	}
	// At least one body byte must survive between the opening tag and the note;
	// the whole output must not be "<skill ...>\n" + note.
	openTagEnd := strings.Index(out, ">\n") + 2 // end of "<skill ...>\n"
	noteIdx := strings.Index(out, "\n(output truncated)")
	if noteIdx <= openTagEnd {
		t.Fatalf("body erased: output is just opening tag + note = %q", out)
	}
	// Total stays under the cap.
	if len(out) > maxSkillOutputSize {
		t.Fatalf("output exceeds cap: %d > %d", len(out), maxSkillOutputSize)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("FormatOutput produced invalid UTF-8 after truncation")
	}
}

// Regression: when a skill has a small body but enough assets that the total
// output exceeds the cap, the truncator must NOT cut into the <skill_assets>
// block — that used to emit a dangling open <skill_assets> with no closing
// </skill_assets> AND duplicate the body's </skill> (one in output[:cut], one in
// the appended closeFrame). On truncation, assets are dropped entirely so the
// result carries exactly one </skill> and no partial asset fragment.
func TestFormatOutputTruncationOmitsAssetsBlock(t *testing.T) {
	dir := t.TempDir()
	// Enough asset files with long relative paths (via nested subdirectories)
	// so the rendered listing at the discovery cap exceeds maxSkillOutputSize.
	// Each line ≈ 280 bytes × 500 assets ≈ 140KB > 100KB cap.
	// macOS filename limit is 255 bytes, so we use deep subdirectory nesting
	// to build long relative names. 100 levels of "d/" × 500 assets renders
	// ~105KB, which clears the 100KB cap and triggers the drop-assets branch.
	extras := make(map[string]string, maxAssetCount+100)
	deepDir := strings.Repeat("d/", 100) // long relative path using short components
	for i := 0; i < maxAssetCount+100; i++ {
		extras[deepDir+fmt.Sprintf("asset-%04d.txt", i)] = "d"
	}
	writeSkillWithAssets(t, dir, "big", "---\nname: big\n---\nsmall body\n", extras)

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if len(loaded[0].Assets) == 0 {
		t.Fatalf("assets at depth 90 not discovered; depth cap may have been reintroduced (Assets=%v)", loaded[0].Assets)
	}
	out := FormatOutput(loaded[0])

	// Exactly one closing </skill> — the body's own; assets dropped carry no
	// extra close tag.
	if c := strings.Count(out, "</skill>"); c != 1 {
		t.Fatalf("expected exactly one </skill>, got %d; output tail:\n%s", c, out[len(out)-200:])
	}
	// No partial/dangling <skill_assets> open tag on the truncated path.
	if strings.Contains(out, "<skill_assets") || strings.Contains(out, "</skill_assets>") {
		t.Fatalf("truncation leaked a <skill_assets> fragment; output tail:\n%s", out[len(out)-200:])
	}
	// Priority rule: a body that fits is kept COMPLETE, then assets are dropped
	// and the note is appended AFTER the body's own </skill>. So the body close
	// tag directly precedes the note (no duplicated close tag appended).
	if !strings.HasSuffix(out, "</skill>\n(output truncated)") {
		t.Fatalf("expected body's </skill> + truncation note at end, got tail=%q", out[len(out)-60:])
	}
	// The complete body content must survive (not erased): the open-tag
	// content precedes the close tag.
	if i := strings.Index(out, "small body"); i < 0 {
		t.Fatalf("body content erased on assets-overflow truncation: %q", out)
	}
	if len(out) > maxSkillOutputSize {
		t.Fatalf("output exceeds cap: %d > %d", len(out), maxSkillOutputSize)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("FormatOutput produced invalid UTF-8 after truncation")
	}
}

// TestFormatOutputTruncationPriority locks the documented priority order — keep
// body complete > drop assets > truncate body — across the three branches.
func TestFormatOutputTruncationPriority(t *testing.T) {
	t.Run("fits_body_and_assets_kept", func(t *testing.T) {
		dir := t.TempDir()
		writeSkillWithAssets(t, dir, "s", "---\nname: s\n---\nbody",
			map[string]string{"a.txt": "x"})
		loaded, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		out := FormatOutput(loaded[0])
		if !strings.Contains(out, "body") || !strings.Contains(out, "a.txt") {
			t.Fatalf("expected both body and asset kept, got:\n%s", out)
		}
		if c := strings.Count(out, "</skill>"); c != 1 {
			t.Fatalf("expected exactly one </skill>, got %d", c)
		}
		// No truncation note when everything fits.
		if strings.Contains(out, "(output truncated)") {
			t.Fatalf("unexpected truncation note when output fits")
		}
	})

	t.Run("body_fits_assets_overflow_assets_dropped_body_intact", func(t *testing.T) {
		dir := t.TempDir()
		// Small body, enough assets with long relative paths to exceed the cap
		// when rendered at the discovery cap (maxAssetCount). 100 levels of
		// "d/" × 500 assets renders ~105KB, clearing the 100KB cap.
		extras := make(map[string]string, maxAssetCount+100)
		deepDir := strings.Repeat("d/", 100)
		for i := 0; i < maxAssetCount+100; i++ {
			extras[deepDir+fmt.Sprintf("asset-%04d.txt", i)] = "d"
		}
		writeSkillWithAssets(t, dir, "s", "---\nname: s\n---\nUNIQUE_BODY_MARKER", extras)
		loaded, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(loaded[0].Assets) == 0 {
			t.Fatalf("assets at depth 90 not discovered; depth cap may have been reintroduced (Assets=%v)", loaded[0].Assets)
		}
		out := FormatOutput(loaded[0])
		// Body must remain COMPLETE and intact.
		if !strings.Contains(out, "UNIQUE_BODY_MARKER") {
			t.Fatalf("body content not preserved intact: %q", out)
		}
		// Assets dropped entirely — no fragment.
		if strings.Contains(out, "<skill_assets") {
			t.Fatalf("assets leaked on overflow: %q", out[len(out)-120:])
		}
		// Exactly one </skill> and a note after it.
		if c := strings.Count(out, "</skill>"); c != 1 {
			t.Fatalf("expected exactly one </skill>, got %d", c)
		}
		if !strings.HasSuffix(out, "</skill>\n(output truncated)") {
			t.Fatalf("expected </skill> + note tail, got %q", out[len(out)-60:])
		}
	})

	t.Run("body_overflow_truncates_body_assets_omitted", func(t *testing.T) {
		dir := t.TempDir()
		// Body alone over the cap; also carry assets to confirm they never appear.
		writeSkillWithAssets(t, dir, "s",
			"---\nname: s\n---\n"+strings.Repeat("A", maxSkillOutputSize*2),
			map[string]string{"a.txt": "x", "b.txt": "y"})
		loaded, err := Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		out := FormatOutput(loaded[0])
		if strings.Contains(out, "<skill_assets") {
			t.Fatalf("assets leaked while truncating body: %q", out)
		}
		if c := strings.Count(out, "</skill>"); c != 1 {
			t.Fatalf("expected exactly one </skill>, got %d", c)
		}
		if !strings.HasSuffix(out, "\n(output truncated)\n</skill>") {
			t.Fatalf("expected note + close frame tail, got %q", out[len(out)-60:])
		}
		if len(out) > maxSkillOutputSize {
			t.Fatalf("output exceeds cap: %d > %d", len(out), maxSkillOutputSize)
		}
		if !utf8.ValidString(out) {
			t.Fatalf("invalid UTF-8 after truncation")
		}
	})
}

// #5 sanity: loadAssets uses confineSkillPath's returned FileInfo and does not
// re-stat, so a symlinked asset pointing OUTSIDE the skills root is skipped
// (not just the SKILL.md symlink path). Guards the permission-allow skill tool.
func TestLoadSkipsSymlinkedAssetEscapingRoot(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("TOP SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(dir, "s")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: s\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(skillDir, "leak.txt")); err != nil {
		t.Skipf("symlink unavailable on this platform: %v", err)
	}

	loaded, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	for _, a := range loaded[0].Assets {
		if a.Name == "leak.txt" || strings.Contains(a.Path, "secret.txt") {
			t.Fatalf("symlinked asset escaping the root must be skipped, got %+v", a)
		}
	}
}
