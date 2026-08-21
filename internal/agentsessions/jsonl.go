package agentsessions

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// headLimit bounds a discovery-time read of a transcript.
//
// Discovery lists sessions; it must never pay for their contents. A working
// machine here holds 439 MB of Claude Code transcripts across 1,266 files, one
// of them 73 MB on its own, so "just parse it and take the first few fields"
// turns `sessions discover` into something nobody runs twice.
//
// All three bounds are needed, and MaxBytes is the one that actually saves us:
// a single transcript line can be megabytes (one large tool result), so a
// line-count bound alone would still stream the whole file looking for the 64th
// newline. MaxBytes is enforced by an io.LimitReader around the file, which
// caps bytes pulled off disk regardless of where the newlines fall.
type headLimit struct {
	MaxLines     int
	MaxBytes     int64
	MaxLineBytes int
}

// defaultHeadLimit is sized from the real corpus, and MaxBytes in particular was
// set by measurement rather than by taste.
//
// Across sampled Claude Code transcripts the record carrying cwd/gitBranch/
// sessionId is line 3 and the ai-title record line 8, so 64 lines is ample. The
// byte budget is the subtle one: it is a budget for the whole scan, so a single
// outsized record spends it and starves the records after it. Three real
// sessions in a 269-file corpus open with a ~334 KB queue-operation record and
// were dropped entirely at a 256 KiB budget — the scan never reached line 3.
//
// 2 MiB clears that case with room to spare while still bounding a 73 MB
// transcript to a ~36x smaller read. The budget only ever binds on pathological
// files; a normal transcript's first 64 lines are a few KB in total and the line
// count ends the scan long before the bytes do.
// importLineLimit is the per-record cap for a FULL import, which is a
// deliberate one-off read of one file the user named — not the index's sweep of
// every transcript on disk. The discovery cap is 64 KiB because it is paid once
// per file across the whole store; applying it to an import silently deleted
// ordinary long messages from the conversation being restored. Still bounded, so
// a corrupt file cannot exhaust memory.
const importLineLimit = 8 << 20

var defaultHeadLimit = headLimit{
	MaxLines:     64,
	MaxBytes:     2 << 20,
	MaxLineBytes: 64 << 10,
}

// countingReader records how many bytes were actually pulled from the file, so
// tests can assert the bound holds rather than trusting that it does.
type countingReader struct {
	inner io.Reader
	count int64
}

func (reader *countingReader) Read(buffer []byte) (int, error) {
	read, err := reader.inner.Read(buffer)
	reader.count += int64(read)
	return read, err
}

// scanHead calls visit with each of the first few lines of path, stopping early
// when visit returns false. It returns the number of bytes read from disk.
//
// Lines are handed over whole up to MaxLineBytes and truncated beyond it. A
// truncated line will not parse as JSON and is simply skipped by the caller,
// which is the right outcome: a record too large to fit the head budget is a
// giant tool result, never the small metadata record discovery is looking for.
func scanHead(root string, path string, limit headLimit, visit func(line []byte) bool) (int64, error) {
	file, err := openContained(root, path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	counter := &countingReader{inner: io.LimitReader(file, limit.MaxBytes)}
	reader := bufio.NewReaderSize(counter, 64<<10)

	for line := 0; line < limit.MaxLines; line++ {
		content, err := readBoundedLine(reader, limit.MaxLineBytes)
		if len(content) > 0 && !visit(content) {
			break
		}
		if err != nil {
			// EOF IS THE ONLY CLEAN STOP. Every other read error — a truncated
			// file, an I/O failure, a directory replaced mid-scan — was reported
			// as a successful partial scan, so a session indexed off whatever
			// bytes happened to arrive before the failure looked exactly like one
			// indexed off a whole file. The caller cannot decline what it is not
			// told about.
			if err == io.EOF {
				break
			}
			return counter.count, err
		}
	}
	return counter.count, nil
}

// openContained opens path through a handle on root, so the containment checked
// when the path was globbed still holds at the moment of the read.
//
// THE GAP IS BETWEEN THE CHECK AND THE OPEN. globTranscripts already refuses a
// symlink wearing a transcript extension, but that verdict is about the state of
// the tree at glob time; anything can replace an entry before the file is
// actually opened, and os.Open would follow it out of the store. os.Root
// resolves every component itself and refuses to leave, so the window closes.
//
// An empty root opens directly, which is what the unit tests for this file need
// — they build a single transcript in a temp dir with no store around it.
func openContained(root string, path string) (*os.File, error) {
	if strings.TrimSpace(root) == "" {
		return os.Open(path)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("transcript %s is outside the store root %s", path, root)
	}
	handle, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer handle.Close()
	return handle.Open(relative)
}

// streamLines calls visit with every line of path, without bounding the total.
// This is the full-read path used once a specific session has been named, where
// the user has asked for the contents and truncating them silently would be a
// lie. Individual lines are still capped: a record larger than maxLineBytes is
// truncated rather than buffered whole, so one 200 MB tool result cannot
// exhaust memory.
func streamLines(path string, maxLineBytes int, visit func(line []byte, truncated bool) bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := bufio.NewReaderSize(file, 64<<10)
	for {
		content, truncated, err := readBoundedLineTruncated(reader, maxLineBytes)
		if (len(content) > 0 || truncated) && !visit(content, truncated) {
			return nil
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// readBoundedLine consumes through the next newline and returns at most keep
// bytes of it.
//
// bufio.Scanner is deliberately not used: it fails the whole scan on a token
// longer than its buffer, and these transcripts routinely contain lines far
// past any sensible buffer size. Here an overlong line is consumed and
// truncated, so one giant record costs a skip rather than the entire file.
func readBoundedLine(reader *bufio.Reader, keep int) ([]byte, error) {
	kept, _, err := readBoundedLineTruncated(reader, keep)
	return kept, err
}

// readBoundedLineTruncated also reports whether anything was discarded.
//
// THE CALLER HAS TO BE ABLE TO TELL. A truncated record is returned as invalid
// JSON, and every caller reacted by skipping it — which is right for the index,
// where a session is still listed, and wrong for an import, where the skipped
// bytes were the conversation itself. Without this the two cases are
// indistinguishable, so the import path could not report what it had lost.
func readBoundedLineTruncated(reader *bufio.Reader, keep int) ([]byte, bool, error) {
	var kept []byte
	total := 0
	for {
		chunk, err := reader.ReadSlice('\n')
		total += len(chunk)
		if room := keep - len(kept); room > 0 {
			if room > len(chunk) {
				room = len(chunk)
			}
			// ReadSlice returns a view into the reader's buffer, invalidated by
			// the next read, so this must copy.
			kept = append(kept, chunk[:room]...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		// THE LINE TERMINATOR IS NOT CONTENT. Counting it made a record whose
		// content exactly fills keep report as truncated, and a CRLF file was one
		// byte worse — so an import emitted "could not be read" for records that
		// had in fact been read in full, which is a false alarm in the one place
		// this signal exists to be trusted.
		return bytes.TrimRight(kept, "\r\n"), total-terminatorBytes(chunk) > keep, err
	}
}

// terminatorBytes is the length of the trailing newline on a chunk, 0 when the
// final line of a file has none.
func terminatorBytes(chunk []byte) int {
	if len(chunk) == 0 || chunk[len(chunk)-1] != '\n' {
		return 0
	}
	if len(chunk) > 1 && chunk[len(chunk)-2] == '\r' {
		return 2
	}
	return 1
}

// fileModTime is the transcript's last-write time, used as the session's
// last-activity stamp. Reading the final record would be more precise and would
// cost a seek plus a read at the end of a file that may be 73 MB — the mtime is
// the same answer for free.
func fileModTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}
