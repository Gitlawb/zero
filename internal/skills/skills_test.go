package skills

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
