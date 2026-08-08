package agentsessions

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestScanHeadReadsFarLessThanTheWholeFile is the test that keeps `sessions
// discover` usable. The live corpus is 439 MB across 1,266 files with a single
// 73 MB transcript in it; an indexer that reads whole files turns a listing into
// a coffee break.
//
// It asserts on BYTES READ rather than on elapsed time, so it fails for the
// right reason on a slow machine and cannot be silenced by faster hardware.
func TestScanHeadReadsFarLessThanTheWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.jsonl")

	// A transcript whose metadata is where it really is (line 3) followed by a
	// great deal of conversation, mimicking a long session.
	bulk := strings.Repeat("x", 200<<10)
	lines := []string{
		`{"type":"mode","mode":"default"}`,
		`{"type":"queue-operation","operation":"enqueue"}`,
		`{"type":"user","cwd":"/Users/someone/proj","sessionId":"huge","message":{"role":"user","content":"go"}}`,
	}
	for i := 0; i < 200; i++ {
		lines = append(lines, `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"`+bulk+`"}]}}`)
	}
	writeFile(t, path, strings.Join(lines, "\n")+"\n")

	fileSize := fileSizeOf(t, path)
	if fileSize < 32<<20 {
		t.Fatalf("fixture is only %d bytes; it must dwarf the head budget to prove anything", fileSize)
	}

	read, err := scanHead(path, defaultHeadLimit, func([]byte) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if read > defaultHeadLimit.MaxBytes {
		t.Errorf("scanHead read %d bytes, over its own %d-byte budget", read, defaultHeadLimit.MaxBytes)
	}
	if read >= fileSize/8 {
		t.Errorf("scanHead read %d of %d bytes — discovery is reading the transcript, not indexing it", read, fileSize)
	}

	// And the point of the budget: the metadata is still found.
	session, ok := indexFamily1Transcript("claude-code", path)
	if !ok || session.Cwd != "/Users/someone/proj" {
		t.Fatalf("indexing a large transcript failed: ok=%v session=%+v", ok, session)
	}
}

// TestAnOversizedFirstRecordDoesNotStarveTheScan pins the defect the live
// corpus exposed: three real sessions there open with a ~334 KB
// queue-operation record. At the original 256 KiB budget the scan spent
// everything on that one line and never reached the record carrying cwd, so the
// sessions vanished from discovery with no error anywhere.
func TestAnOversizedFirstRecordDoesNotStarveTheScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fat-head.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"queue-operation","operation":"enqueue","content":"` + strings.Repeat("q", 334<<10) + `"}`,
		`{"type":"mode","mode":"default"}`,
		`{"type":"user","cwd":"/Users/someone/proj","sessionId":"fat-head","message":{"role":"user","content":"still here"}}`,
	}, "\n")+"\n")

	session, ok := indexFamily1Transcript("claude-code", path)
	if !ok {
		t.Fatal("a session whose first record is huge was dropped from discovery")
	}
	if session.Cwd != "/Users/someone/proj" {
		t.Errorf("Cwd = %q, want the record after the oversized one to be reached", session.Cwd)
	}
}

// TestALineTooLongToKeepIsSkippedNotFatal covers a single record larger than
// MaxLineBytes. bufio.Scanner would fail the entire scan here; the records
// after it must still be read.
func TestALineTooLongToKeepIsSkippedNotFatal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long-line.jsonl")
	writeFile(t, path, strings.Join([]string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + strings.Repeat("y", 128<<10) + `"}]}}`,
		`{"type":"user","cwd":"/Users/someone/proj","sessionId":"long-line","message":{"role":"user","content":"after the wall"}}`,
	}, "\n")+"\n")

	session, ok := indexFamily1Transcript("claude-code", path)
	if !ok || session.Cwd != "/Users/someone/proj" {
		t.Fatalf("a record past an over-long line was not read: ok=%v session=%+v", ok, session)
	}
}

func TestScanHeadStopsWhenTheVisitorIsDone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stop.jsonl")
	lines := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		lines = append(lines, `{"type":"user","content":"`+strings.Repeat("z", 4096)+`"}`)
	}
	writeFile(t, path, strings.Join(lines, "\n")+"\n")

	seen := 0
	read, err := scanHead(path, defaultHeadLimit, func([]byte) bool {
		seen++
		return seen < 2
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Errorf("visited %d lines, want to stop after 2", seen)
	}
	if read > 64<<10 {
		t.Errorf("read %d bytes after an early stop; the reader buffer should bound this", read)
	}
}

func TestScanHeadHonoursItsLineBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "many.jsonl")
	lines := make([]string, 0, 500)
	for i := 0; i < 500; i++ {
		lines = append(lines, `{"type":"noise"}`)
	}
	writeFile(t, path, strings.Join(lines, "\n")+"\n")

	seen := 0
	if _, err := scanHead(path, defaultHeadLimit, func([]byte) bool { seen++; return true }); err != nil {
		t.Fatal(err)
	}
	if seen != defaultHeadLimit.MaxLines {
		t.Errorf("visited %d lines, want exactly MaxLines=%d", seen, defaultHeadLimit.MaxLines)
	}
}

func TestScanHeadOnAMissingFileIsAnError(t *testing.T) {
	// Unlike globbing, an unreadable file that discovery has already decided
	// exists is worth reporting to the caller, which drops that one entry.
	if _, err := scanHead(filepath.Join(t.TempDir(), "absent.jsonl"), defaultHeadLimit, func([]byte) bool { return true }); err == nil {
		t.Error("scanHead on a missing file returned no error")
	}
}

func TestStreamLinesReadsEverything(t *testing.T) {
	path := filepath.Join(t.TempDir(), "all.jsonl")
	lines := make([]string, 0, 300)
	for i := 0; i < 300; i++ {
		lines = append(lines, `{"type":"message","n":`+itoa(i)+`}`)
	}
	writeFile(t, path, strings.Join(lines, "\n")+"\n")

	seen := 0
	if err := streamLines(path, 64<<10, func([]byte) bool { seen++; return true }); err != nil {
		t.Fatal(err)
	}
	if seen != 300 {
		t.Errorf("streamLines visited %d lines, want all 300 — a full read must not "+
			"inherit the head budget", seen)
	}
}

func TestStreamLinesToleratesAMissingTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-newline.jsonl")
	// A live transcript is appended to constantly; the last record frequently
	// has no terminator yet.
	writeFile(t, path, `{"type":"a"}`+"\n"+`{"type":"b"}`)

	seen := 0
	if err := streamLines(path, 64<<10, func([]byte) bool { seen++; return true }); err != nil {
		t.Fatal(err)
	}
	if seen != 2 {
		t.Errorf("visited %d lines, want 2 — the unterminated final record must not be lost", seen)
	}
}
