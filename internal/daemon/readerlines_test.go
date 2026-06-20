package daemon

import (
	"bufio"
	"strings"
	"testing"
)

func TestReaderLinesHandlesLargeLine(t *testing.T) {
	// A stream-json line far larger than the old 1 MiB scanner cap must be read in
	// full, not dropped, and must not abort the rest of the stream (D1).
	big := strings.Repeat("x", 2<<20) // 2 MiB
	rl := &readerLines{r: bufio.NewReaderSize(strings.NewReader(big+"\nsecond\n"), 64*1024)}
	line, ok, err := rl.Next()
	if err != nil || !ok || len(line) != len(big) {
		t.Fatalf("large line dropped: ok=%v err=%v len=%d want %d", ok, err, len(line), len(big))
	}
	if l2, ok, err := rl.Next(); err != nil || !ok || l2 != "second" {
		t.Fatalf("second line: %q ok=%v err=%v", l2, ok, err)
	}
	if _, ok, err := rl.Next(); ok || err != nil {
		t.Fatalf("expected clean EOF, got ok=%v err=%v", ok, err)
	}
}
