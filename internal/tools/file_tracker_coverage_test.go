package tools

import (
	"path/filepath"
	"testing"
)

// SeenWhole used to be a flag set only by a SINGLE read that covered the file,
// while SeenRange judged coverage from the accumulated ranges. The two then
// disagreed about the same reads, and write_file — which gates on SeenWhole —
// refused overwrites it should have allowed.
func TestSeenWholeIsDerivedFromAccumulatedRanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.go")

	for name, testCase := range map[string]struct {
		reads [][3]int // start, end, total
		want  bool
	}{
		"one read covering the file": {
			reads: [][3]int{{1, 100, 100}},
			want:  true,
		},
		"two adjacent reads covering it together": {
			reads: [][3]int{{1, 50, 100}, {51, 100, 100}},
			want:  true,
		},
		"overlapping reads covering it together": {
			reads: [][3]int{{1, 60, 100}, {40, 100, 100}},
			want:  true,
		},
		"out-of-order reads covering it together": {
			reads: [][3]int{{51, 100, 100}, {1, 50, 100}},
			want:  true,
		},
		"a gap in the middle": {
			reads: [][3]int{{1, 40, 100}, {60, 100, 100}},
			want:  false,
		},
		"missing the first line": {
			reads: [][3]int{{2, 100, 100}},
			want:  false,
		},
		"missing the last line": {
			reads: [][3]int{{1, 99, 100}},
			want:  false,
		},
		"nothing read at all": {
			reads: nil,
			want:  false,
		},
	} {
		t.Run(name, func(t *testing.T) {
			tracker := NewFileTracker()
			for _, read := range testCase.reads {
				tracker.RecordSeenRange(path, read[0], read[1], read[2])
			}
			if got := tracker.SeenWhole(path); got != testCase.want {
				t.Errorf("SeenWhole = %v, want %v after reads %v", got, testCase.want, testCase.reads)
			}
		})
	}
}

// SeenWhole and SeenRange must not disagree about the same reads.
func TestSeenWholeAgreesWithSeenRange(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.go")
	tracker := NewFileTracker()
	tracker.RecordSeenRange(path, 1, 50, 100)
	tracker.RecordSeenRange(path, 51, 100, 100)

	if !tracker.SeenRange(path, 1, 100) {
		t.Fatal("SeenRange says the whole file was not seen")
	}
	if !tracker.SeenWhole(path) {
		t.Error("SeenRange covers every line but SeenWhole disagrees")
	}
}

// A file that changed length since the recorded reads must not stay "seen":
// the old ranges describe a shape the file no longer has.
func TestSeenWholeResetsWhenTheFileChangesLength(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.go")
	tracker := NewFileTracker()
	tracker.RecordSeenRange(path, 1, 100, 100)
	if !tracker.SeenWhole(path) {
		t.Fatal("precondition: the whole file was read")
	}
	// The file grew; only the first 100 lines of 200 have been seen.
	tracker.RecordSeenRange(path, 1, 100, 200)
	if tracker.SeenWhole(path) {
		t.Error("file grew to 200 lines but is still reported as seen whole")
	}
	if !tracker.SeenRange(path, 1, 100) {
		t.Error("the lines that were read should still count as seen")
	}
}
