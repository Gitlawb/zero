package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runEdit(t *testing.T, dir, initial string, args map[string]any) (Result, string) {
	t.Helper()
	path := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	if args["path"] == nil {
		args["path"] = "target.txt"
	}
	result := NewEditFileTool(dir).Run(context.Background(), args)
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return result, string(after)
}

func TestEditExactMatchStillPreferred(t *testing.T) {
	// The fast path must keep byte-exact semantics: an exact match wins even
	// when fuzzy strategies would also find candidates elsewhere.
	result, after := runEdit(t, t.TempDir(), "alpha\nbeta\ngamma\n", map[string]any{
		"old_string": "beta",
		"new_string": "delta",
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %q", result.Output)
	}
	if after != "alpha\ndelta\ngamma\n" {
		t.Fatalf("unexpected content: %q", after)
	}
}

func TestEditFuzzyLineTrimmed(t *testing.T) {
	// Model reproduced the lines without the file's leading indentation; the
	// line-trimmed strategy must find the real indented span and preserve the
	// file's own indentation on the replaced region boundary.
	initial := "func a() {\n\tx := 1\n\treturn x\n}\n"
	result, after := runEdit(t, t.TempDir(), initial, map[string]any{
		"old_string": "x := 1\nreturn x",
		"new_string": "y := 2\nreturn y",
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %q", result.Output)
	}
	// new_string was written at old_string's outdented shape; the span's
	// indentation must be re-applied so the file keeps its tabs.
	if !strings.Contains(after, "\ty := 2\n\treturn y\n}") {
		t.Fatalf("unexpected content: %q", after)
	}
	if strings.Contains(after, "x := 1") {
		t.Fatalf("old span survived: %q", after)
	}
}

func TestEditFuzzyWhitespaceNormalizedSingleLine(t *testing.T) {
	// Whitespace runs collapsed: "x  :=   1" in the file, single spaces from
	// the model.
	initial := "start\nx  :=   1\nend\n"
	result, after := runEdit(t, t.TempDir(), initial, map[string]any{
		"old_string": "x := 1",
		"new_string": "x := 2",
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %q", result.Output)
	}
	if !strings.Contains(after, "x := 2") || strings.Contains(after, ":=   1") {
		t.Fatalf("unexpected content: %q", after)
	}
}

func TestEditFuzzyBlockAnchorToleratesMiddleDrift(t *testing.T) {
	// First and last lines anchor the block; one interior line differs slightly
	// (comment text drifted). Levenshtein similarity keeps it above 0.65.
	initial := strings.Join([]string{
		"func handler(w http.ResponseWriter, r *http.Request) {",
		"\t// write the response body to the client",
		"\tw.WriteHeader(http.StatusOK)",
		"\tfmt.Fprint(w, \"done\")",
		"}",
		"",
	}, "\n")
	find := strings.Join([]string{
		"func handler(w http.ResponseWriter, r *http.Request) {",
		"\t// write the response body to client",
		"\tw.WriteHeader(http.StatusOK)",
		"\tfmt.Fprint(w, \"done\")",
		"}",
	}, "\n")
	result, after := runEdit(t, t.TempDir(), initial, map[string]any{
		"old_string": find,
		"new_string": "func handler(w http.ResponseWriter, r *http.Request) {\n\tw.WriteHeader(http.StatusNoContent)\n}",
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %q", result.Output)
	}
	if !strings.Contains(after, "StatusNoContent") || strings.Contains(after, "StatusOK") {
		t.Fatalf("unexpected content: %q", after)
	}
}

func TestEditFuzzyIndentationFlexible(t *testing.T) {
	// The whole block sits one nesting level deeper in the file than in the
	// model's old_string, with interior relative indentation preserved.
	initial := "if ok {\n\t\tfor i := range xs {\n\t\t\tsum += xs[i]\n\t\t}\n}\n"
	result, after := runEdit(t, t.TempDir(), initial, map[string]any{
		"old_string": "for i := range xs {\n\tsum += xs[i]\n}",
		"new_string": "sum += total(xs)",
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %q", result.Output)
	}
	// The replacement lands at the span's real depth (two tabs), not at
	// old_string's outdented depth.
	if !strings.Contains(after, "\t\tsum += total(xs)") || strings.Contains(after, "range xs") {
		t.Fatalf("unexpected content: %q", after)
	}
}

func TestEditFuzzyCRLFLineTrimmedPreservesCarriageReturns(t *testing.T) {
	// CRLF file + a multi-line outdented old_string: the resolved span carries
	// tabs and trailing CRs; the replacement must inherit both so the file's
	// CRLF pairs and indentation survive the edit.
	initial := "func a() {\r\n\tx := 1\r\n\treturn x\r\n}\r\n"
	result, after := runEdit(t, t.TempDir(), initial, map[string]any{
		"old_string": "x := 1\nreturn x",
		"new_string": "y := 2\nreturn y",
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %q", result.Output)
	}
	if !strings.Contains(after, "\ty := 2\r\n\treturn y\r\n}") {
		t.Fatalf("unexpected content: %q", after)
	}
	if strings.Contains(after, "\n\treturn y\n") {
		t.Fatalf("bare LF introduced into CRLF file: %q", after)
	}
}

func TestUniformIndentDelta(t *testing.T) {
	cases := []struct {
		span, find string
		want       string
		ok         bool
	}{
		{"\tx := 1\n\treturn x", "x := 1\nreturn x", "\t", true},
		{"\t\tfor {\n\t\t\tgo()\n\t\t}", "for {\n\tgo()\n}", "\t\t", true},
		{"x := 1", "x := 1", "", true},                          // no shift needed
		{"\tx := 1\n  return x", "x := 1\nreturn x", "", false}, // mixed delta
		{"\ta\n\tb\n\tc", "a\nb", "", false},                    // line-count mismatch
		{"  a", "\ta", "", false},                               // find indent not a suffix
	}
	for _, c := range cases {
		got, ok := uniformIndentDelta(c.span, c.find)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("uniformIndentDelta(%q, %q) = %q,%v want %q,%v", c.span, c.find, got, ok, c.want, c.ok)
		}
	}
}

func TestAdaptReplacementLeavesNonUniformSpansAlone(t *testing.T) {
	// Block-anchor style match with a drifted interior: no uniform delta, so
	// the replacement must pass through untouched.
	span := "\tfunc h() {\n\t\t// drifted comment\n\t}"
	find := "func h() {\n// different comment\n}"
	if got := adaptReplacementToSpan(span, find, "replacement()"); got != "replacement()" {
		t.Fatalf("non-uniform span must not re-indent: %q", got)
	}
}

func TestEditFuzzyEscapeNormalized(t *testing.T) {
	// Model reproduced the line as it would appear inside a quoted string
	// literal, with escaped quotes.
	initial := "console.log(\"hello world\")\n"
	result, after := runEdit(t, t.TempDir(), initial, map[string]any{
		"old_string": `console.log(\"hello world\")`,
		"new_string": `console.log("goodbye")`,
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %q", result.Output)
	}
	if !strings.Contains(after, `console.log("goodbye")`) {
		t.Fatalf("unexpected content: %q", after)
	}
}

func TestEditFuzzyTrimmedBoundary(t *testing.T) {
	// Stray whitespace around an otherwise-exact old_string; "  two  " is not a
	// substring of the file, so only the fuzzy cascade can resolve it.
	initial := "one\ntwo\nthree\n"
	result, after := runEdit(t, t.TempDir(), initial, map[string]any{
		"old_string": "  two  ",
		"new_string": "2",
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %q", result.Output)
	}
	if after != "one\n2\nthree\n" {
		t.Fatalf("unexpected content: %q", after)
	}
}

func TestTrimmedBoundaryReplacerYieldsTrimmedSpan(t *testing.T) {
	candidates := trimmedBoundaryReplacer("one\ntwo\nthree\n", "\ntwo\n")
	if len(candidates) == 0 || candidates[0] != "two" {
		t.Fatalf("expected trimmed candidate \"two\", got %v", candidates)
	}
	if trimmedBoundaryReplacer("anything", "already-trimmed") != nil {
		t.Fatal("already-trimmed find must yield no candidates")
	}
}

// Two identical blocks that only match old_string after indentation-tolerant
// matching: exact search finds nothing, fuzzy finds both.
const ambiguousBlocks = "\tif x {\n\t\tgo()\n\t}\nmid\n\tif x {\n\t\tgo()\n\t}\n"

func TestEditFuzzyAmbiguousReportsError(t *testing.T) {
	// No strategy can disambiguate identical blocks, so the tool must refuse
	// rather than guess, and the file must be untouched.
	result, after := runEdit(t, t.TempDir(), ambiguousBlocks, map[string]any{
		"old_string": "if x {\n\tgo()\n}",
		"new_string": "stop()",
	})
	if result.Status != StatusError || !strings.Contains(result.Output, "multiple locations") {
		t.Fatalf("expected ambiguity error, got %q", result.Output)
	}
	if after != ambiguousBlocks {
		t.Fatalf("file must be unchanged, got %q", after)
	}
}

func TestEditFuzzyReplaceAll(t *testing.T) {
	// replace_all applies the fuzzy-resolved span at every occurrence.
	result, after := runEdit(t, t.TempDir(), ambiguousBlocks, map[string]any{
		"old_string":  "if x {\n\tgo()\n}",
		"new_string":  "stop()",
		"replace_all": true,
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %q", result.Output)
	}
	if strings.Count(after, "stop()") != 2 || strings.Contains(after, "go()") {
		t.Fatalf("unexpected content: %q", after)
	}
}

func TestEditFuzzyNotFoundKeepsExactError(t *testing.T) {
	result, after := runEdit(t, t.TempDir(), "alpha\n", map[string]any{
		"old_string": "omega",
		"new_string": "x",
	})
	if result.Status != StatusError || !strings.Contains(result.Output, "Could not find the exact string") {
		t.Fatalf("expected not-found error, got %q", result.Output)
	}
	if after != "alpha\n" {
		t.Fatalf("file must be unchanged, got %q", after)
	}
}

func TestEditFuzzyCRLFFile(t *testing.T) {
	// CRLF file + LF old_string with indentation drift: the CRLF translation
	// feeds the cascade and the replacement preserves CRLF endings.
	initial := "func a() {\r\n\tx := 1\r\n}\r\n"
	result, after := runEdit(t, t.TempDir(), initial, map[string]any{
		"old_string": "x := 1",
		"new_string": "x := 2",
	})
	if result.Status != StatusOK {
		t.Fatalf("expected ok, got %q", result.Output)
	}
	if !strings.Contains(after, "\tx := 2\r\n") {
		t.Fatalf("unexpected content: %q", after)
	}
}

func TestIsDisproportionateEditMatch(t *testing.T) {
	// A candidate span that dwarfs old_string must be refused: anchors bridging
	// unrelated code would otherwise delete it all.
	big := strings.Repeat("line\n", 10)
	if !isDisproportionateEditMatch(big, "a\nb\nc") {
		t.Fatal("10-line span for 3-line find must be disproportionate")
	}
	if isDisproportionateEditMatch("a\nb\nc\nd", "a\nb\nc") {
		t.Fatal("one extra line within tolerance must be allowed")
	}
	if isDisproportionateEditMatch("single line", "single") {
		t.Fatal("single-line finds are exempt from the byte-length guard")
	}
	if !isDisproportionateEditMatch("xx\n"+strings.Repeat("y", 900)+"\nzz", "xx\nab\nzz") {
		t.Fatal("byte-length blowup must be disproportionate")
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"", "abc", 3},
		{"kitten", "sitting", 3},
		{"same", "same", 0},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Fatalf("levenshtein(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestEditFuzzyDistinctCandidatesAreAmbiguous(t *testing.T) {
	// Two same-content blocks at DIFFERENT indentation: each resolved span is
	// distinct and occurs exactly once, so the old literal-uniqueness check
	// passed and silently edited the first block. Distinct-shaped candidates
	// from one strategy must instead report ambiguity and leave the file alone.
	initial := "\tif x {\n\t\tgo()\n\t}\nmid\n\t\tif x {\n\t\t\tgo()\n\t\t}\n"
	result, after := runEdit(t, t.TempDir(), initial, map[string]any{
		"old_string": "if x {\n\tgo()\n}",
		"new_string": "stop()",
	})
	if result.Status != StatusError || !strings.Contains(result.Output, "multiple locations") {
		t.Fatalf("distinct fuzzy candidates must be ambiguous, got %q", result.Output)
	}
	if after != initial {
		t.Fatalf("file must be unchanged, got %q", after)
	}
}

func TestEditFuzzyBlockAnchorTwoPlausibleBlocksAmbiguous(t *testing.T) {
	// Two blocks share the same first/last anchor lines and BOTH interiors sit
	// above the similarity threshold. Picking the "best" one would silently
	// edit a block the model may not have meant; both must surface so the
	// cascade reports ambiguity and the file stays untouched.
	initial := strings.Join([]string{
		"func setup(cfg Config) {",
		"\tvalue := compute(alpha, beta, gamma)",
		"}",
		"",
		"func setup(cfg Config) {",
		"\tvalue := compute(alpha, delta, gamma)",
		"}",
		"",
	}, "\n")
	find := strings.Join([]string{
		"func setup(cfg Config) {",
		"\tvalue := compute(alpha, omega, gamma)",
		"}",
	}, "\n")
	result, after := runEdit(t, t.TempDir(), initial, map[string]any{
		"old_string": find,
		"new_string": "func setup(cfg Config) {}",
	})
	if result.Status != StatusError || !strings.Contains(result.Output, "multiple locations") {
		t.Fatalf("two plausible anchor blocks must be ambiguous, got %q", result.Output)
	}
	if after != initial {
		t.Fatalf("file must be unchanged, got %q", after)
	}
}
