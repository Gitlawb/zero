package tools

import (
	"strings"
	"testing"
)

func TestBoundedFileDiffRefusesPartialOrBinaryContent(t *testing.T) {
	if diff, ok := boundedFileDiff("a.txt", "old", "new"); !ok || diff.Path != "a.txt" || diff.OldText != "old" || diff.NewText != "new" {
		t.Fatalf("small text diff = %#v, %t", diff, ok)
	}
	for _, tc := range []struct {
		name string
		old  string
		new  string
	}{
		{"unchanged", "same", "same"},
		{"binary old", string([]byte{0xff}), "text"},
		{"binary new", "text", string([]byte{0xff})},
		{"too large", strings.Repeat("a", maxToolPreviewBytes), "b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if diff, ok := boundedFileDiff("a.txt", tc.old, tc.new); ok || diff != (FileDiff{}) {
				t.Fatalf("unexpected diff = %#v, %t", diff, ok)
			}
		})
	}
}
