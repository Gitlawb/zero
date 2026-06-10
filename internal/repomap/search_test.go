package repomap

import "testing"

func TestSearchRanksPathMatchesByReason(t *testing.T) {
	snapshot := Snapshot{Files: []File{
		{Path: "docs/app-config-guide.md"},
		{Path: "c/o/n/f/i/g.txt"},
		{Path: "cmd/config/main.go"},
		{Path: "internal/config.go"},
	}}

	results := Search(snapshot, "@config", 10)

	want := []struct {
		path   string
		reason string
	}{
		{"internal/config.go", MatchReasonPrefixBasename},
		{"cmd/config/main.go", MatchReasonPathSegment},
		{"docs/app-config-guide.md", MatchReasonSubstring},
		{"c/o/n/f/i/g.txt", MatchReasonFuzzy},
	}
	if len(results) != len(want) {
		t.Fatalf("Search returned %d results, want %d: %#v", len(results), len(want), results)
	}
	for i, expected := range want {
		if results[i].Path != expected.path || results[i].Reason != expected.reason {
			t.Fatalf("result %d = %#v, want path %q reason %q", i, results[i], expected.path, expected.reason)
		}
	}
	for i := 1; i < len(results); i++ {
		if results[i-1].Score <= results[i].Score {
			t.Fatalf("scores should descend by match quality, got %#v", results)
		}
	}
}

func TestSearchOrdersTiedScoresByPath(t *testing.T) {
	snapshot := Snapshot{Files: []File{
		{Path: "internal/search/search.go"},
		{Path: "internal/repomap/search.go"},
	}}

	results := Search(snapshot, "search.go", 10)

	if len(results) != 2 {
		t.Fatalf("Search returned %d results, want 2: %#v", len(results), results)
	}
	if results[0].Path != "internal/repomap/search.go" || results[1].Path != "internal/search/search.go" {
		t.Fatalf("expected tied exact basename matches to sort by path, got %#v", results)
	}
	if results[0].Score != results[1].Score {
		t.Fatalf("expected exact basename matches to tie on score, got %#v", results)
	}
	for _, result := range results {
		if result.Reason != MatchReasonExactBasename {
			t.Fatalf("expected exact basename reason, got %#v", results)
		}
	}
}

func TestSearchRanksCompleteMultiTermMatchesAheadOfPartialMatches(t *testing.T) {
	snapshot := Snapshot{Files: []File{
		{Path: "internal/zeroruntime/runtime.go"},
		{Path: "internal/agent/runtime.go"},
		{Path: "internal/agent/loop.go"},
	}}

	results := Search(snapshot, "agent runtime", 10)

	if len(results) != 3 {
		t.Fatalf("Search returned %d results, want 3: %#v", len(results), results)
	}
	if results[0].Path != "internal/agent/runtime.go" || results[0].Reason != MatchReasonMultiTerm {
		t.Fatalf("expected complete multi-term match first, got %#v", results)
	}
	if results[1].Score <= results[2].Score {
		t.Fatalf("expected stronger partial match before weaker partial match, got %#v", results)
	}
}

func TestSearchAppliesLimitAndIgnoresBlankQueries(t *testing.T) {
	snapshot := Snapshot{Files: []File{
		{Path: "cmd/zero/main.go"},
		{Path: "internal/agent/loop.go"},
		{Path: "internal/cli/serve.go"},
	}}

	results := Search(snapshot, "go", 2)
	if len(results) != 2 {
		t.Fatalf("Search returned %d results, want 2: %#v", len(results), results)
	}

	if results := Search(snapshot, "   @   ", 10); len(results) != 0 {
		t.Fatalf("blank query returned results: %#v", results)
	}
	if results := Search(snapshot, "go", 0); len(results) != 0 {
		t.Fatalf("zero limit returned results: %#v", results)
	}
}
