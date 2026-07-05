package tools

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// Small output passes through untouched and records raw==emitted, no truncated flag.
func TestBudgetBashOutputSmallPassesThrough(t *testing.T) {
	meta := map[string]string{}
	out, errStr, truncated := budgetBashOutput("hello\n", "warn\n", meta)
	if out != "hello\n" || errStr != "warn\n" {
		t.Fatalf("small output altered: out=%q err=%q", out, errStr)
	}
	if truncated {
		t.Fatalf("small output must report truncated=false")
	}
	if meta["truncated"] == "true" {
		t.Fatalf("small output must not be flagged truncated: %v", meta)
	}
	if meta["raw_bytes"] != strconv.Itoa(len("hello\n")+len("warn\n")) {
		t.Fatalf("raw_bytes wrong: %v", meta)
	}
}

// Oversized stdout is truncated head+tail: both the first and last lines survive,
// the middle is dropped behind a marker, and meta is flagged.
func TestBudgetBashOutputTruncatesHeadAndTail(t *testing.T) {
	head := "FIRST_LINE_MARKER\n"
	tail := "\nLAST_LINE_MARKER"
	big := head + strings.Repeat("x", bashOutputBudgetBytes) + tail

	meta := map[string]string{}
	out, _, truncated := budgetBashOutput(big, "", meta)

	if !truncated {
		t.Fatalf("oversized output must report truncated=true")
	}
	if !strings.Contains(out, "FIRST_LINE_MARKER") {
		t.Fatalf("head lost after truncation")
	}
	if !strings.Contains(out, "LAST_LINE_MARKER") {
		t.Fatalf("tail lost after truncation (failures live at the tail)")
	}
	if !strings.Contains(out, "output truncated") {
		t.Fatalf("expected a truncation marker, got:\n%s", out[:200])
	}
	if len(out) > bashOutputBudgetBytes {
		t.Fatalf("emitted %d bytes exceeds budget %d", len(out), bashOutputBudgetBytes)
	}
	if meta["truncated"] != "true" {
		t.Fatalf("expected truncated=true, got %v", meta)
	}
	if meta["raw_bytes"] != strconv.Itoa(len(big)) {
		t.Fatalf("raw_bytes = %s, want %d", meta["raw_bytes"], len(big))
	}
	if got, _ := strconv.Atoi(meta["emitted_bytes"]); got != len(out) {
		t.Fatalf("emitted_bytes = %s, want %d", meta["emitted_bytes"], len(out))
	}
}

// boundedBuffer must keep memory bounded to head+tail while counting every byte,
// so a runaway command can't OOM Zero before truncation runs. It keeps the very
// first bytes (head) and the very last bytes (tail), discarding the middle.
func TestBoundedBufferKeepsHeadAndTailBounded(t *testing.T) {
	b := newBoundedBuffer(8, 8)
	total := 0
	for i := 0; i < 1000; i++ {
		chunk := []byte(fmt.Sprintf("%05d.", i)) // 6 bytes each
		n, err := b.Write(chunk)
		if err != nil || n != len(chunk) {
			t.Fatalf("Write = (%d, %v), want (%d, nil)", n, err, len(chunk))
		}
		total += len(chunk)
	}
	if b.total != total {
		t.Fatalf("total = %d, want %d (every byte must be counted)", b.total, total)
	}
	// Retained memory is bounded regardless of how much streamed through.
	if len(b.head) > 8 {
		t.Fatalf("head grew past its cap: %d", len(b.head))
	}
	if len(b.tail) > 2*8 {
		t.Fatalf("tail grew past 2×cap (compaction failed): %d", len(b.tail))
	}
	r := b.retained()
	if len(r) > 8+8 {
		t.Fatalf("retained %d bytes, want <= headCap+tailCap", len(r))
	}
	if !strings.HasPrefix(r, "00000.") {
		t.Fatalf("retained head lost, got prefix %q", r[:min(6, len(r))])
	}
	if !strings.HasSuffix(r, "00999.") {
		t.Fatalf("retained tail lost, got %q", r)
	}
}

// budgetBashCapture reports the TRUE total (not the retained size) in the marker
// and raw_bytes, even though only a bounded head+tail was ever held in memory.
func TestBudgetBashCaptureReportsTrueTotal(t *testing.T) {
	// Retained head+tail as boundedBuffer would hand over; the real command produced
	// far more than was kept.
	retained := "HEAD_START" + strings.Repeat("y", bashOutputBudgetBytes) + "TAIL_END"
	total := 10 * bashOutputBudgetBytes

	meta := map[string]string{}
	out, _, truncated := budgetBashCapture(retained, total, "", 0, meta)

	if !truncated {
		t.Fatal("oversized capture must report truncated=true")
	}
	if !strings.Contains(out, "HEAD_START") || !strings.Contains(out, "TAIL_END") {
		t.Fatalf("head/tail lost after budgeting:\n%s", out[:min(120, len(out))])
	}
	if len(out) > bashOutputBudgetBytes {
		t.Fatalf("emitted %d bytes exceeds budget %d", len(out), bashOutputBudgetBytes)
	}
	if meta["raw_bytes"] != strconv.Itoa(total) {
		t.Fatalf("raw_bytes = %s, want the true total %d", meta["raw_bytes"], total)
	}
	// The marker must cite the true omitted count (total-budget), not retained-budget.
	if !strings.Contains(out, strconv.Itoa(total-bashOutputBudgetBytes)) {
		t.Fatalf("marker should cite the true omitted byte count:\n%s", out[:min(200, len(out))])
	}
}
