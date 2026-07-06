package tui

import (
	"testing"
)

func newFuzzyTestPicker(items []pickerItem) *commandPicker {
	return &commandPicker{
		kind:     pickerModel,
		items:    append([]pickerItem{}, items...),
		allItems: append([]pickerItem{}, items...),
	}
}

// A closer match ranks above a merely-containing one, so the best result lands at
// the top instead of in original order.
func TestPickerRanksBestMatchFirst(t *testing.T) {
	p := newFuzzyTestPicker([]pickerItem{
		{Label: "Claude Opus 4.5", Value: "opus-4.5"},
		{Label: "Claude Sonnet 4.5", Value: "sonnet-4.5"},
		{Label: "Sonnet", Value: "sonnet"}, // exact-ish prefix match should win
	})
	p.query = "sonnet"
	p.applyQuery()

	if len(p.items) != 2 {
		t.Fatalf("expected 2 matches for 'sonnet', got %d: %#v", len(p.items), p.items)
	}
	if p.items[0].Value != "sonnet" {
		t.Fatalf("exact/prefix match should rank first, got %q", p.items[0].Value)
	}
}

// A subsequence query (non-contiguous characters) still matches when no substring does.
func TestPickerFuzzySubsequenceMatches(t *testing.T) {
	p := newFuzzyTestPicker([]pickerItem{
		{Label: "Claude Sonnet 4.5", Value: "sonnet-4.5"},
		{Label: "GPT-5", Value: "gpt-5"},
	})
	p.query = "snt45" // subsequence of "sonnet 4.5", not a substring
	p.applyQuery()

	if len(p.items) != 1 || p.items[0].Value != "sonnet-4.5" {
		t.Fatalf("subsequence query should match sonnet-4.5, got %#v", p.items)
	}
}

// Ranking keeps each group contiguous: a filtered result never splits one group
// into two separate blocks, so group headers still render once per group.
func TestPickerRankingKeepsGroupsContiguous(t *testing.T) {
	p := newFuzzyTestPicker([]pickerItem{
		{Group: "openai", Label: "gpt-5 mini", Value: "a"},
		{Group: "anthropic", Label: "sonnet 4.5", Value: "b"},
		{Group: "openai", Label: "gpt-5 codex", Value: "c"},
		{Group: "anthropic", Label: "opus 4.5", Value: "d"},
	})
	p.query = "5" // matches both openai models and the "4.5" anthropic ones
	p.applyQuery()

	// Every item's group must appear as one contiguous run.
	seen := map[string]bool{}
	last := ""
	for _, item := range p.items {
		if item.Group != last {
			if seen[item.Group] {
				t.Fatalf("group %q split into non-contiguous blocks: %#v", item.Group, p.items)
			}
			seen[item.Group] = true
			last = item.Group
		}
	}
}
