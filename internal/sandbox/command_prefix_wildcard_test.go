package sandbox

import (
	"path/filepath"
	"testing"
)

func TestCommandPrefixProjectScope(t *testing.T) {
	store, err := NewGrantStore(StoreOptions{FilePath: filepath.Join(t.TempDir(), "grants.json")})
	if err != nil {
		t.Fatalf("new grant store: %v", err)
	}
	if _, err := store.GrantCommandPrefix(CommandPrefixInput{ToolName: "bash", Prefix: []string{"go", "test"}, Project: "/proj/a"}); err != nil {
		t.Fatalf("grant project prefix: %v", err)
	}

	if _, ok, _ := store.LookupCommandPrefix("bash", []string{"go", "test", "./..."}, "/proj/a"); !ok {
		t.Fatal("project grant should match inside its own project")
	}
	if _, ok, _ := store.LookupCommandPrefix("bash", []string{"go", "test", "./..."}, "/proj/b"); ok {
		t.Fatal("project grant must not match a different project")
	}

	// A global grant (empty Project) matches in every project.
	if _, err := store.GrantCommandPrefix(CommandPrefixInput{ToolName: "bash", Prefix: []string{"go", "vet"}}); err != nil {
		t.Fatalf("grant global prefix: %v", err)
	}
	if _, ok, _ := store.LookupCommandPrefix("bash", []string{"go", "vet", "./..."}, "/proj/b"); !ok {
		t.Fatal("global grant should match any project")
	}

	// The same prefix can be held both project-scoped and global without collision.
	if _, err := store.GrantCommandPrefix(CommandPrefixInput{ToolName: "bash", Prefix: []string{"go", "test"}}); err != nil {
		t.Fatalf("grant global duplicate prefix: %v", err)
	}
	if _, ok, _ := store.LookupCommandPrefix("bash", []string{"go", "test", "-run", "X"}, "/proj/b"); !ok {
		t.Fatal("global copy of a project prefix should still match elsewhere")
	}
}

func TestValidCommandPrefixAllowsTrailingWildcardOnLastToken(t *testing.T) {
	// yarn is not a banned launcher, so the wildcard prefix is grantable.
	if !ValidCommandPrefix([]string{"yarn", "test:*"}) {
		t.Fatal("trailing wildcard on the last token should be valid")
	}
}

func TestValidCommandPrefixRejectsUnsafeWildcards(t *testing.T) {
	cases := map[string][]string{
		"lone launcher wildcard":  {"go*"},
		"mid-command wildcard":    {"yarn", "test:*", "unit"},
		"mid-token glob":          {"yarn", "te*st"},
		"plain trailing wildcard": {"yarn", "test*"},
		"bare wildcard":           {"yarn", "*"},
	}
	for name, prefix := range cases {
		if ValidCommandPrefix(prefix) {
			t.Fatalf("%s: %#v should be rejected", name, prefix)
		}
	}
}

func TestCommandPrefixSessionGrantMatchesWildcard(t *testing.T) {
	engine := NewEngine(EngineOptions{Policy: DefaultPolicy()})
	engine.GrantCommandPrefixForSession("bash", []string{"yarn", "test:*"})

	if _, ok := engine.LookupCommandPrefixForSession("bash", []string{"yarn", "test:unit"}); !ok {
		t.Fatal("wildcard grant should match test:unit")
	}
	if _, ok := engine.LookupCommandPrefixForSession("bash", []string{"yarn", "test:e2e", "--watch"}); !ok {
		t.Fatal("wildcard grant should match test:e2e with extra args")
	}
	if _, ok := engine.LookupCommandPrefixForSession("bash", []string{"yarn", "build"}); ok {
		t.Fatal("wildcard grant must not match a non-test: script")
	}
}
