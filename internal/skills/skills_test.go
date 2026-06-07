package skills

import (
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
