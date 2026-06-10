package repoinfo

import (
	"reflect"
	"testing"
)

func TestLanguageForExt(t *testing.T) {
	cases := map[string]string{"go": "Go", "ts": "TypeScript", "tsx": "TypeScript", "py": "Python", "rs": "Rust", "cpp": "C++"}
	for ext, want := range cases {
		if got, ok := languageForExt(ext); !ok || got != want {
			t.Fatalf("languageForExt(%q)=%q,%v want %q", ext, got, ok, want)
		}
	}
	if _, ok := languageForExt("unknownext"); ok {
		t.Fatal("unknown ext must not resolve")
	}
}

func TestCicdForPath(t *testing.T) {
	cases := map[string]string{
		".github/workflows/ci.yml": "GitHub Actions",
		".gitlab-ci.yml":           "GitLab CI",
		"Jenkinsfile":              "Jenkins",
		"src/main.go":              "",
	}
	for p, want := range cases {
		if got := cicdForPath(p); got != want {
			t.Fatalf("cicdForPath(%q)=%q want %q", p, got, want)
		}
	}
}

func TestDetectionTables(t *testing.T) {
	if !buildToolFiles["go.mod"] || !buildToolFiles["package.json"] {
		t.Fatal("expected build tool files")
	}
	if !testToolFiles["pytest.ini"] {
		t.Fatal("expected test tool file")
	}
	if workspaceMarkers["pnpm-workspace.yaml"] != "pnpm" || workspaceMarkers["go.work"] != "go-work" {
		t.Fatal("expected workspace markers")
	}
}

func TestSortedUnique(t *testing.T) {
	got := sortedUnique(map[string]bool{"b": true, "a": true})
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("sortedUnique=%v", got)
	}
	if got := sortedUnique(map[string]bool{}); len(got) != 0 {
		t.Fatalf("empty set should give empty slice, got %v", got)
	}
}
