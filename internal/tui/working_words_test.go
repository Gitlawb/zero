package tui

import (
	"strings"
	"testing"
)

func TestWorkingWordsCurrentIsFirstBrand(t *testing.T) {
	w := newWorkingWords()
	if got, want := w.Current(), "gitlawbmaxxing"; got != want {
		t.Fatalf("Current() = %q, want %q (first brand verb)", got, want)
	}
}

func TestWorkingWordsTickAdvances(t *testing.T) {
	w := newWorkingWords()
	first := w.Current()
	w.Tick()
	second := w.Current()
	if first == second {
		t.Fatalf("Tick() did not advance: still %q", second)
	}
	w.Tick()
	third := w.Current()
	if third == second || third == first {
		t.Fatalf("third tick = %q; should differ from %q and %q", third, first, second)
	}
}

func TestWorkingWordsTickWraps(t *testing.T) {
	w := newWorkingWords()
	total := len(w.weighted)
	for i := 0; i < total; i++ {
		w.Tick()
	}
	if got, want := w.Current(), "gitlawbmaxxing"; got != want {
		t.Fatalf("after %d ticks Current() = %q, want %q (full wrap)", total, got, want)
	}
}

func TestWorkingWordsResetRewinds(t *testing.T) {
	w := newWorkingWords()
	for i := 0; i < 17; i++ {
		w.Tick()
	}
	if w.Current() == "gitlawbmaxxing" {
		t.Fatalf("expected to have moved off first verb after 17 ticks, got %q", w.Current())
	}
	w.Reset()
	if got, want := w.Current(), "gitlawbmaxxing"; got != want {
		t.Fatalf("Reset() Current() = %q, want %q", got, want)
	}
}

func TestWorkingWordsBrandWeighted(t *testing.T) {
	// Walk a full ring and count how many positions hold the brand verb.
	// With 8 brand entries duplicated (8 + 8 = 16 brand positions) and 40
	// total, we expect 16/40 = 40% of positions to be brand entries.
	// Assert the floor (>= 30%) so the test isn't brittle to a count
	// adjustment but still catches the brand word being dropped.
	w := newWorkingWords()
	brandCount := 0
	total := len(w.weighted)
	if total == 0 {
		t.Fatal("weighted ring is empty")
	}
	for i := 0; i < total; i++ {
		if v := w.Current(); isBrandVerb(v) {
			brandCount++
		}
		w.Tick()
	}
	ratio := float64(brandCount) / float64(total)
	if ratio < 0.30 {
		t.Fatalf("brand share = %d/%d = %.0f%%, want >= 30%%", brandCount, total, ratio*100)
	}
}

func TestWorkingWordsBaseListIntegrity(t *testing.T) {
	// The base list (pre-weighting) must be non-empty, contain no empty
	// strings, and have no duplicates. The weighted ring is allowed to
	// duplicate by design.
	base := baseVerbs()
	if len(base) < 25 {
		t.Fatalf("base list has %d entries, want >= 25", len(base))
	}
	seen := make(map[string]struct{}, len(base))
	for i, v := range base {
		if strings.TrimSpace(v) == "" {
			t.Fatalf("base[%d] is empty or whitespace", i)
		}
		if _, dup := seen[v]; dup {
			t.Fatalf("base[%d] = %q is a duplicate", i, v)
		}
		seen[v] = struct{}{}
	}
}

func TestWorkingWordsNilSafe(t *testing.T) {
	// A nil receiver must not panic on any of the public methods. The
	// renderer is a hot path; the safe-guard avoids a nil check at every
	// call site.
	var w *workingWords
	w.Tick()
	w.Reset()
	if got := w.Current(); got != "working" {
		t.Fatalf("nil.Current() = %q, want fallback %q", got, "working")
	}
}

func isBrandVerb(v string) bool {
	for _, b := range brandVerbs {
		if b == v {
			return true
		}
	}
	return false
}
