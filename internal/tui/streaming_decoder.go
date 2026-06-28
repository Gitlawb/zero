package tui

import "strings"

// streamingDecoder incrementally decodes a tool call's streaming JSON arguments
// for the live "writing" preview. Each delta is processed exactly once, so the
// preview costs O(fragment) per delta instead of re-scanning the whole growing
// args buffer on every render (the previous O(n²) behavior — M14/L6/L7).
//
// It buffers raw args until the content value starts (extracting the file path
// from the head), then decodes the content's JSON-string escapes as fragments
// arrive, keeping only a bounded tail of lines plus a running line count.
type streamingDecoder struct {
	pathKeys    []string
	contentKeys []string
	tailCap     int

	rawLen   int    // total raw arg bytes seen (for the "receiving N KB" indicator)
	head     []byte // raw args before the content value; dropped once content starts
	pathScan []byte // bounded post-content args used only to recover a late path
	path     string
	pathDone bool

	inContent bool
	closed    bool // the content value's closing quote was seen

	tail       []string // last tailCap completed content lines
	cur        []byte   // the in-progress (incomplete) last line
	lineCount  int      // completed content lines (decoded newlines)
	pendingEsc bool     // the previous byte was a lone backslash
	uSkip      int      // remaining hex digits to skip after a \uXXXX escape
}

func newStreamingDecoder() *streamingDecoder {
	return &streamingDecoder{
		pathKeys:    []string{"path", "file", "file_path", "filepath", "filename"},
		contentKeys: []string{"content", "new_string", "new_str", "new_text", "new", "replacement", "patch", "input"},
		tailCap:     streamingTailLines,
	}
}

// feed consumes one streamed argument fragment.
func (d *streamingDecoder) feed(fragment string) {
	d.rawLen += len(fragment)
	if d.closed {
		d.scanPathFragment(fragment)
		return
	}
	if d.inContent {
		if remainder := d.consume(fragment); remainder != "" {
			d.scanPathFragment(remainder)
		}
		return
	}
	d.head = append(d.head, fragment...)
	if !d.pathDone {
		// Best effort: the path completes quickly (it precedes the content), and a
		// partial value just updates until it's whole.
		d.setPathFrom(string(d.head))
	}
	start := d.contentValueStart()
	if start < 0 {
		return // content value hasn't begun yet
	}
	d.inContent = true
	rest := string(d.head[start:])
	d.head = nil
	if remainder := d.consume(rest); remainder != "" {
		d.scanPathFragment(remainder)
	}
}

func (d *streamingDecoder) setPathFrom(args string) {
	if d.pathDone {
		return
	}
	if v := streamingFilePath(args); v != "" {
		d.path = v
		d.pathDone = true
	}
}

const maxStreamingPathScanBytes = 8192

func (d *streamingDecoder) scanPathFragment(fragment string) {
	if d.pathDone || fragment == "" {
		return
	}
	d.pathScan = append(d.pathScan, fragment...)
	if len(d.pathScan) > maxStreamingPathScanBytes {
		d.pathScan = d.pathScan[len(d.pathScan)-maxStreamingPathScanBytes:]
	}
	d.setPathFrom(string(d.pathScan))
}

// contentValueStart returns the index in head just past the opening quote of the
// first content value, or -1 if no content value has started yet.
func (d *streamingDecoder) contentValueStart() int {
	s := string(d.head)
	best := -1
	for _, key := range d.contentKeys {
		if idx := jsonStringValueStart(s, key); idx >= 0 && (best < 0 || idx < best) {
			best = idx
		}
	}
	return best
}

// consume decodes content bytes, splitting on decoded newlines into tail lines,
// stopping at the value's closing quote, and returning any bytes after it.
func (d *streamingDecoder) consume(b string) string {
	for i := 0; i < len(b); i++ {
		c := b[i]
		if d.uSkip > 0 {
			d.uSkip-- // skip the 4 hex digits of a \uXXXX escape (not decoded for a preview)
			continue
		}
		if d.pendingEsc {
			d.pendingEsc = false
			switch c {
			case 'n':
				d.newline()
			case 't':
				d.cur = append(d.cur, '\t')
			case 'r':
				// drop carriage returns from the preview
			case 'u':
				d.uSkip = 4
			default: // " \ / and anything else: take the literal char
				d.cur = append(d.cur, c)
			}
			continue
		}
		switch c {
		case '\\':
			d.pendingEsc = true
		case '"':
			d.closed = true // closing quote of the content value
			return b[i+1:]
		default:
			d.cur = append(d.cur, c)
		}
	}
	return ""
}

func (d *streamingDecoder) newline() {
	d.tail = append(d.tail, string(d.cur))
	if len(d.tail) > d.tailCap {
		d.tail = d.tail[len(d.tail)-d.tailCap:]
	}
	d.cur = d.cur[:0]
	d.lineCount++
}

// hasContent reports whether the content value has begun streaming.
func (d *streamingDecoder) hasContent() bool { return d.inContent }

// lineTotal is the line count shown in the header (completed lines plus the
// in-progress one).
func (d *streamingDecoder) lineTotal() int {
	if !d.inContent {
		return 0
	}
	n := d.lineCount
	if len(d.cur) > 0 || !d.closed {
		n++ // the current (possibly empty, still-open) line
	}
	if n < 1 {
		n = 1
	}
	return n
}

// tailLines returns the last tailCap content lines, including the in-progress one.
func (d *streamingDecoder) tailLines() []string {
	out := append([]string(nil), d.tail...)
	if len(d.cur) > 0 {
		out = append(out, string(d.cur))
	}
	if len(out) > d.tailCap {
		out = out[len(out)-d.tailCap:]
	}
	return out
}

// jsonStringValueStart finds `"key"` then, tolerating whitespace around the colon,
// returns the index just past the opening quote of its string value, or -1.
func jsonStringValueStart(s, key string) int {
	marker := `"` + key + `"`
	idx := strings.Index(s, marker)
	if idx < 0 {
		return -1
	}
	i := skipJSONSpace(s, idx+len(marker))
	if i >= len(s) || s[i] != ':' {
		return -1
	}
	i = skipJSONSpace(s, i+1)
	if i >= len(s) || s[i] != '"' {
		return -1
	}
	return i + 1
}
