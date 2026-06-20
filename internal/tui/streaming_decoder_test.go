package tui

import (
	"reflect"
	"strings"
	"testing"
)

func feedChunks(d *streamingDecoder, chunks ...string) {
	for _, c := range chunks {
		d.feed(c)
	}
}

func TestStreamingDecoderPathTailAndCount(t *testing.T) {
	d := newStreamingDecoder()
	// kimi-style spacing; content fed in awkward chunks.
	feedChunks(d, `{"path": "a/b.go",`, ` "content": "package main\n`, `\nfunc main() {`, `\n\t}`, `"}`)
	if d.path != "a/b.go" {
		t.Errorf("path = %q, want a/b.go", d.path)
	}
	if !d.hasContent() {
		t.Fatal("expected content")
	}
	want := []string{"package main", "", "func main() {", "\t}"}
	if got := d.tailLines(); !reflect.DeepEqual(got, want) {
		t.Errorf("tail = %#v, want %#v", got, want)
	}
	if got := d.lineTotal(); got != 4 {
		t.Errorf("lineTotal = %d, want 4", got)
	}
}

func TestStreamingDecoderSplitEscape(t *testing.T) {
	d := newStreamingDecoder()
	// The \n and \t escapes are split across feed boundaries (backslash, then n/t).
	feedChunks(d, `{"path":"x","content":"line1\`, `nline2\`, `tend"}`)
	want := []string{"line1", "line2\tend"}
	if got := d.tailLines(); !reflect.DeepEqual(got, want) {
		t.Errorf("split-escape tail = %#v, want %#v", got, want)
	}
}

func TestStreamingDecoderEditFileNewString(t *testing.T) {
	// edit_file's canonical content key is new_string (M13 preserved in the decoder).
	d := newStreamingDecoder()
	d.feed(`{"path":"a.go","old_string":"x","new_string":"replaced\nbody"}`)
	if !d.hasContent() {
		t.Fatal("expected content from new_string")
	}
	if got := d.tailLines(); !reflect.DeepEqual(got, []string{"replaced", "body"}) {
		t.Errorf("new_string tail = %#v", got)
	}
}

func TestStreamingDecoderProgressBeforeContent(t *testing.T) {
	d := newStreamingDecoder()
	d.feed(`{"path":"website/css/styles.css"`)
	if d.hasContent() {
		t.Error("content not started yet")
	}
	if d.rawLen == 0 {
		t.Error("rawLen should track bytes for the KB indicator")
	}
	if d.path != "website/css/styles.css" {
		t.Errorf("path should extract before content: %q", d.path)
	}
}

func TestStreamingDecoderTailIsBounded(t *testing.T) {
	d := newStreamingDecoder()
	var b strings.Builder
	b.WriteString(`{"path":"x","content":"`)
	for i := 0; i < 100; i++ {
		b.WriteString("line\\n") // 100 escaped newlines
	}
	b.WriteString(`"}`)
	d.feed(b.String())
	if got := len(d.tailLines()); got > streamingTailLines {
		t.Errorf("tail not bounded: %d > %d", got, streamingTailLines)
	}
	if d.lineTotal() < 100 {
		t.Errorf("lineTotal should count all lines: %d", d.lineTotal())
	}
}

func TestStreamingDecoderChunkingIsConsistent(t *testing.T) {
	full := `{"path":"a.go","content":"alpha\nbeta\ngamma\ndelta"}`
	whole := newStreamingDecoder()
	whole.feed(full)
	byByte := newStreamingDecoder()
	for _, r := range full {
		byByte.feed(string(r))
	}
	if !reflect.DeepEqual(whole.tailLines(), byByte.tailLines()) || whole.lineTotal() != byByte.lineTotal() {
		t.Errorf("byte-by-byte (%#v) != whole (%#v)", byByte.tailLines(), whole.tailLines())
	}
}
