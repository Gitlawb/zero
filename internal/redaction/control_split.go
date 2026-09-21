package redaction

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A CREDENTIAL IS STILL A CREDENTIAL WITH AN INVISIBLE BYTE IN THE MIDDLE.
//
// The shape matchers below (openaiKeyPattern, textSecretPatterns) all describe a
// CONTIGUOUS run of body characters, and none of their character classes admits
// a control or format character. So a single NUL, ESC or zero-width space
// dropped into the body of a key ends the match, and RedactString emits the two
// fragments untouched. Whoever reads that output next — a terminal that eats the
// escape, a log viewer, a JSON consumer that strips control bytes, or a person
// copying the text — sees the original key back, because nothing about those
// characters was ever displayed. Reported in #969 for NUL and ESC; every other
// Cc and Cf character behaves the same way, which is why this is written against
// the classes rather than against the two bytes named there.
//
// The rule is normalize-then-match, applied so that the ORIGINAL bytes are what
// gets replaced: matching on a compacted copy and then redacting that copy would
// hand back a string missing the separators, which is a different string from
// the one the caller passed in. So the spans are mapped back and the original
// slice, separators included, is what the replacement covers.
//
// WHAT THIS DELIBERATELY DOES NOT STRIP: tab, newline and carriage return.
// Those three are real text structure rather than invisible filler, so a
// credential broken across a line boundary is still two visible fragments here
// and is not covered. Stripping them would also let a match span a line break
// and collapse unrelated lines into one replacement. A credential split that way
// is visible to whoever reads it; the ones handled here are not.

// splitSecretSeparator reports a character that can sit inside a credential
// without being seen. Cc covers the C0 and C1 controls (NUL, ESC, DEL, NEL, CSI)
// and Cf the format characters (zero-width space and joiner, word joiner, BOM,
// soft hyphen). Tab, newline and carriage return are excluded: see above.
func splitSecretSeparator(r rune) bool {
	switch r {
	case '\t', '\n', '\r':
		return false
	}
	return unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r)
}

// containsSplitSeparator is the cheap gate in front of the compaction, so text
// with nothing invisible in it — which is nearly all text — costs one scan and
// no allocation. ASCII is settled from the byte alone; only a byte that could
// begin a multi-byte rune needs decoding, and the C1 and Cf blocks all start
// with a lead byte at or above 0xC2.
func containsSplitSeparator(value string) bool {
	for i := 0; i < len(value); i++ {
		b := value[i]
		if b < utf8.RuneSelf {
			if b < 0x20 || b == 0x7f {
				if b == '\t' || b == '\n' || b == '\r' {
					continue
				}
				return true
			}
			continue
		}
		r, width := utf8.DecodeRuneInString(value[i:])
		if splitSecretSeparator(r) {
			return true
		}
		i += width - 1
	}
	return false
}

// compactSplitSeparators returns value with every splitSecretSeparator removed.
//
// Decoded explicitly rather than with range, because an invalid byte must be
// measured as the one byte it occupies. Ranging reports it as U+FFFD, whose
// encoded width is three, and the offsets this feeds have to stay in the
// source's own bytes.
func compactSplitSeparators(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for offset := 0; offset < len(value); {
		r, width := utf8.DecodeRuneInString(value[offset:])
		if !splitSecretSeparator(r) {
			builder.WriteString(value[offset : offset+width])
		}
		offset += width
	}
	return builder.String()
}

// splitSeparatorIndex gives, for each byte of compactSplitSeparators(value), the
// start and end offsets in value of the rune it came from. That is what lets a
// match found in the compacted text name the exact original slice it stands for.
//
// Built only once a match exists. Text carrying control characters and no
// credential is the ordinary case for terminal output, and it should not pay for
// a table nothing will read.
func splitSeparatorIndex(value string) (starts, ends []int) {
	starts = make([]int, 0, len(value))
	ends = make([]int, 0, len(value))
	for offset := 0; offset < len(value); {
		r, width := utf8.DecodeRuneInString(value[offset:])
		if splitSecretSeparator(r) {
			offset += width
			continue
		}
		for i := 0; i < width; i++ {
			starts = append(starts, offset)
			ends = append(ends, offset+width)
		}
		offset += width
	}
	return starts, ends
}

// shapeSecretSpans returns the byte ranges of value claimed by the
// credential-shape matchers, applying the same openai filter RedactString uses
// so the two agree on what a key is.
func shapeSecretSpans(value string) [][]int {
	var spans [][]int
	for _, span := range openaiKeyPattern.FindAllStringIndex(value, -1) {
		if !openAIShapeIsSecret(value[span[0]:span[1]]) {
			continue
		}
		spans = append(spans, span)
	}
	for _, pattern := range textSecretPatterns {
		spans = append(spans, pattern.FindAllStringIndex(value, -1)...)
	}
	return spans
}

// openAIShapeIsSecret is the digit/kebab-case filter RedactString applies to an
// openaiKeyPattern match, named so both callers state the same rule once.
func openAIShapeIsSecret(match string) bool {
	if knownOpenAIKeyPrefix(match) || secretMatchHasDigit(match) {
		return true
	}
	return !strings.Contains(strings.TrimPrefix(match, "sk-"), "-")
}

// redactControlSplitSecrets replaces credentials whose body is interrupted by
// invisible characters. It is a no-op for text containing none of them, which is
// nearly all text, so the contiguous path below keeps its behavior exactly.
func redactControlSplitSecrets(value, replacement string) string {
	if !containsSplitSeparator(value) {
		return value
	}
	compact := compactSplitSeparators(value)
	if len(compact) == len(value) {
		return value
	}
	spans := shapeSecretSpans(compact)
	if len(spans) == 0 {
		return value
	}
	starts, ends := splitSeparatorIndex(value)
	mapped := make([][]int, 0, len(spans))
	for _, span := range spans {
		if span[0] >= len(starts) || span[1] <= span[0] || span[1] > len(ends) {
			continue
		}
		mapped = append(mapped, []int{starts[span[0]], ends[span[1]-1]})
	}
	return spliceSpans(value, mapped, replacement)
}

// spliceSpans replaces every named range of value with replacement, merging
// ranges that overlap or touch so one credential cannot be replaced twice.
func spliceSpans(value string, spans [][]int, replacement string) string {
	if len(spans) == 0 {
		return value
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i][0] != spans[j][0] {
			return spans[i][0] < spans[j][0]
		}
		return spans[i][1] > spans[j][1]
	})
	var builder strings.Builder
	builder.Grow(len(value))
	written := 0
	for _, span := range spans {
		if span[0] < written {
			if span[1] <= written {
				continue
			}
			span[0] = written
		}
		builder.WriteString(value[written:span[0]])
		builder.WriteString(replacement)
		written = span[1]
	}
	builder.WriteString(value[written:])
	return builder.String()
}
