package redaction

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A CREDENTIAL IS STILL A CREDENTIAL WITH AN INVISIBLE BYTE IN THE MIDDLE.
//
// The shape matchers (openaiKeyPattern, textSecretPatterns) all describe a
// CONTIGUOUS run of body characters, and none of their character classes admits
// a control or format character. So a single NUL, ESC or zero-width space
// dropped into the body of a key ends the match, and RedactString emits the two
// fragments untouched. Whoever reads that output next, a terminal that eats the
// escape, a log viewer, a JSON consumer that strips control bytes, or a person
// copying the text, sees the original key back, because nothing about those
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
// THE SAME CHARACTER IS FILLER INSIDE A CREDENTIAL AND A DELIMITER OUTSIDE ONE,
// and the difference cannot be read off the compacted text, so it is taken from
// the two places that do know it:
//
//   - This pass runs AFTER the contiguous matchers, over their output, and never
//     across a replacement. A credential that is whole gets claimed by its own
//     strict match, so two whole credentials with a separator between them stay
//     two replacements with the separator still between them, rather than one
//     match reaching out of the first and into the second.
//   - The leading word boundary is checked against the SOURCE, not against the
//     joined text. A separator in front of a key is a boundary, and a whole key
//     written after a NUL already redacts, so removing that NUL must not turn
//     the key into the tail of the word in front of it. The patterns below drop
//     their leading boundary, and the byte before the match's ORIGINAL start is
//     what has to be a non-word byte instead. So "prefix<NUL>AKIA...<ESC>..."
//     redacts, because unsplit "prefix<NUL>AKIA..." does, and "xAKIA...<ESC>..."
//     does not, because unsplit "xAKIA..." does not either.
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
// with nothing invisible in it, which is nearly all text, costs one scan and no
// allocation. ASCII is settled from the byte alone; only a byte that could begin
// a multi-byte rune needs decoding, and the C1 and Cf blocks all start with a
// lead byte at or above 0xC2.
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

// leadingWordBoundary is the token every shape matcher opens with. The split
// matchers drop it and check the source byte instead; see the header comment.
const leadingWordBoundary = `\b`

// splitOpenAIPattern and splitTextPatterns are the shape matchers with their
// leading word boundary removed, derived from the originals rather than written
// out a second time so the two lists cannot drift apart.
var (
	splitOpenAIPattern = withoutLeadingBoundary(openaiKeyPattern)
	splitTextPatterns  = withoutLeadingBoundaries(textSecretPatterns)
)

func withoutLeadingBoundary(pattern *regexp.Regexp) *regexp.Regexp {
	source := pattern.String()
	relaxed := strings.TrimPrefix(source, leadingWordBoundary)
	if relaxed == source {
		return pattern
	}
	return regexp.MustCompile(relaxed)
}

func withoutLeadingBoundaries(patterns []*regexp.Regexp) []*regexp.Regexp {
	relaxed := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		relaxed = append(relaxed, withoutLeadingBoundary(pattern))
	}
	return relaxed
}

// wordByte is the class Go's regexp word boundary is defined over: ASCII
// letters, digits and underscore, and nothing else. A multi-byte lead or
// continuation byte falls outside it, which is what the boundary already
// assumes.
func wordByte(b byte) bool {
	return b == '_' ||
		('0' <= b && b <= '9') ||
		('A' <= b && b <= 'Z') ||
		('a' <= b && b <= 'z')
}

// shapeSecretSpans returns the byte ranges of value claimed by the
// boundary-relaxed credential shapes, applying the same openai filter
// RedactString uses so the two agree on what a key is.
func shapeSecretSpans(value string) [][]int {
	var spans [][]int
	for _, span := range splitOpenAIPattern.FindAllStringIndex(value, -1) {
		if !openAIShapeIsSecret(value[span[0]:span[1]]) {
			continue
		}
		spans = append(spans, span)
	}
	for _, pattern := range splitTextPatterns {
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

// splitSourceSpans maps match ranges found in compactSplitSeparators(value)
// back onto value, returning the original slice each one stands for.
//
// Only the endpoints of actual matches are resolved. An index over every source
// byte would cost at least sixteen bytes per byte of input on a 64-bit build,
// and RedactString is handed whole command output and notification bodies, so
// that table would be sized by the text rather than by the number of credentials
// in it. The walk below is a single pass whose memory is two ints per match
// endpoint.
func splitSourceSpans(value string, spans [][]int) [][]int {
	if len(spans) == 0 {
		return nil
	}
	wanted := make([]int, 0, 2*len(spans))
	for _, span := range spans {
		if span[1] <= span[0] {
			continue
		}
		wanted = append(wanted, span[0], span[1]-1)
	}
	if len(wanted) == 0 {
		return nil
	}
	sort.Ints(wanted)
	unique := wanted[:1]
	for _, offset := range wanted[1:] {
		if offset != unique[len(unique)-1] {
			unique = append(unique, offset)
		}
	}
	wanted = unique

	starts := make([]int, len(wanted))
	ends := make([]int, len(wanted))
	next := 0
	compact := 0
	for offset := 0; offset < len(value) && next < len(wanted); {
		r, width := utf8.DecodeRuneInString(value[offset:])
		if splitSecretSeparator(r) {
			offset += width
			continue
		}
		for next < len(wanted) && wanted[next] < compact+width {
			starts[next] = offset
			ends[next] = offset + width
			next++
		}
		compact += width
		offset += width
	}
	if next < len(wanted) {
		return nil
	}

	mapped := make([][]int, 0, len(spans))
	for _, span := range spans {
		if span[1] <= span[0] {
			continue
		}
		start := starts[sort.SearchInts(wanted, span[0])]
		end := ends[sort.SearchInts(wanted, span[1]-1)]
		mapped = append(mapped, []int{start, end})
	}
	return mapped
}

// redactControlSplitSecrets replaces credentials whose body is interrupted by
// invisible characters. It is a no-op for text containing none of them, which is
// nearly all text, so the contiguous path keeps its behavior exactly.
//
// It runs over the contiguous matchers' output and stops at every replacement
// they left behind: those mark text already accounted for, and a match reaching
// across one would be reaching out of one credential and into the next.
func redactControlSplitSecrets(value, replacement string) string {
	if !containsSplitSeparator(value) {
		return value
	}
	if replacement == "" {
		return redactSplitRegion(value, replacement, 0)
	}
	regions := strings.Split(value, replacement)
	if len(regions) == 1 {
		return redactSplitRegion(value, replacement, 0)
	}
	previous := byte(0)
	for i, region := range regions {
		regions[i] = redactSplitRegion(region, replacement, previous)
		previous = replacement[len(replacement)-1]
	}
	return strings.Join(regions, replacement)
}

// redactSplitRegion is the matcher for one stretch of text with no replacement
// inside it. previous is the byte immediately before the region in the original,
// or zero at the start of the string, and settles the leading boundary for a
// match that begins at offset zero.
func redactSplitRegion(value, replacement string, previous byte) string {
	if !containsSplitSeparator(value) {
		return value
	}
	compact := compactSplitSeparators(value)
	if len(compact) == len(value) {
		return value
	}
	spans := splitSourceSpans(value, shapeSecretSpans(compact))
	if len(spans) == 0 {
		return value
	}
	bounded := make([][]int, 0, len(spans))
	for _, span := range spans {
		before := previous
		if span[0] > 0 {
			before = value[span[0]-1]
		}
		if wordByte(before) {
			continue
		}
		bounded = append(bounded, span)
	}
	return spliceSpans(value, bounded, replacement)
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
