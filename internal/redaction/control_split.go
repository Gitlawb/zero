package redaction

import (
	"fmt"
	"regexp"
	"regexp/syntax"
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
// HOW: each shape is rewritten so that a run of separators may sit between any
// two characters it consumes, and matched against the ORIGINAL text. Nothing is
// removed before matching, so there are no offsets to map back, and the leading
// \b of every shape is judged by the regexp engine against the real neighbour:
// a key written after a NUL starts at a boundary, exactly as the unsplit key
// does, and a key glued to the end of a word does not.
//
// An earlier version removed the separators first and matched the compacted
// copy. That lost the difference between a separator inside a credential and one
// next to it, and the fixes for that (check the boundary against the source
// afterwards, map offsets back) left a hole: in "xAKIA<NUL>AKIAIOSF<SOH>ODNN7..."
// the leftmost compacted match began at the decoy AKIA, was then rejected for
// the "x" in front of it, and had already consumed the start of the real key, so
// the real key was never considered. Resuming the scan one byte after each
// rejection would close that, but it is quadratic for the greedy shapes on text
// like "ask-ask-ask-..." with a single NUL in it. Keeping the boundary inside
// the pattern lets RE2 do it in one linear pass.
//
// This pass runs AFTER the contiguous matchers, over their output, and never
// across a replacement. A credential that is whole is claimed by its own strict
// match, so two whole credentials with a separator between them stay two
// replacements with the separator still between them. The shapes run one at a
// time, like the contiguous pass, so a replacement can give the next shape the
// boundary it would otherwise lack.
//
// A match never starts or ends on a separator, so the separators on either side
// of a credential stay where they were: "<NUL>AKIA..." becomes
// "<NUL>[REDACTED]", as it does for the unsplit key.
//
// ONE KNOWN LIMIT, from running the contiguous matchers first. When the part of
// a split key BEFORE its first separator is already a complete credential on
// its own, the contiguous matcher claims that part and stops at the separator,
// so what follows is judged by itself. Usually that is only the tail of the key,
// which is not a credential alone. It matters only when another credential is
// glued straight onto that tail with no boundary between them: the unsplit key
// would have run on and swallowed it, and here it stays. Extending the
// contiguous match across the separator instead would bring back what running
// first exists to prevent, prose after a whole key being eaten and a following
// JWT losing its header.
//
// WHAT THIS DELIBERATELY DOES NOT TREAT AS A SEPARATOR: tab, newline and
// carriage return. Those three are real text structure rather than invisible
// filler, so a credential broken across a line boundary is still two visible
// fragments here and is not covered. Treating them as gaps would also let a
// match span a line break and collapse unrelated lines into one replacement. A
// credential split that way is visible to whoever reads it; the ones handled
// here are not. Invalid UTF-8 is out for the same reason: the engine reads an
// invalid byte as U+FFFD, which is drawn.

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

// containsSplitSeparator is the cheap gate in front of the gap-tolerant pass, so
// text with nothing invisible in it, which is nearly all text, costs one scan and
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

// stripSplitSeparators returns value with every splitSecretSeparator removed.
// Used only on a matched credential, to judge it by its visible characters.
func stripSplitSeparators(value string) string {
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

// splitSeparatorClass is splitSecretSeparator as a regexp character class,
// generated from the same Unicode tables and the same three exclusions so the
// class and the predicate cannot disagree.
var splitSeparatorClass = func() string {
	var class strings.Builder
	class.WriteByte('[')
	emit := func(lo, hi rune) {
		fmt.Fprintf(&class, `\x{%x}-\x{%x}`, lo, hi)
	}
	for _, table := range []*unicode.RangeTable{unicode.Cc, unicode.Cf} {
		visit := func(lo, hi, stride uint32) {
			for r := lo; r <= hi; r += stride {
				start := rune(r)
				if !splitSecretSeparator(start) {
					continue
				}
				end := start
				if stride == 1 {
					for next := rune(r) + 1; uint32(next) <= hi && splitSecretSeparator(next); next++ {
						end = next
					}
					r = uint32(end)
				}
				emit(start, end)
			}
		}
		for _, r := range table.R16 {
			visit(uint32(r.Lo), uint32(r.Hi), uint32(r.Stride))
		}
		for _, r := range table.R32 {
			visit(r.Lo, r.Hi, r.Stride)
		}
	}
	class.WriteByte(']')
	return class.String()
}()

// splitOpenAIPattern and splitTextPatterns are the shape matchers with a run of
// separators allowed between every two characters they consume, derived from
// the originals rather than written out a second time so the two lists cannot
// drift apart.
var (
	splitOpenAIPattern = gapTolerant(openaiKeyPattern)
	splitTextPatterns  = gapTolerantAll(textSecretPatterns)
)

func gapTolerantAll(patterns []*regexp.Regexp) []*regexp.Regexp {
	tolerant := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		tolerant = append(tolerant, gapTolerant(pattern))
	}
	return tolerant
}

// gapTolerant rewrites pattern so that a run of separators may appear before
// every character it consumes except the first. Zero-width assertions, the
// leading \b in particular, are left exactly where they were.
func gapTolerant(pattern *regexp.Regexp) *regexp.Regexp {
	tree, err := syntax.Parse(pattern.String(), syntax.Perl)
	if err != nil {
		panic(fmt.Sprintf("redaction: parse %q: %v", pattern.String(), err))
	}
	separator, err := syntax.Parse(splitSeparatorClass, syntax.Perl)
	if err != nil {
		panic(fmt.Sprintf("redaction: parse separator class: %v", err))
	}
	consumed := false
	return regexp.MustCompile(withGaps(tree, separator, &consumed).String())
}

// withGaps inserts the separator run before each consuming atom. consumed
// tracks whether anything has been consumed yet on this path, because the first
// character of a match must be a real one: a leading gap would let a match start
// on a separator and swallow it.
func withGaps(node, separator *syntax.Regexp, consumed *bool) *syntax.Regexp {
	switch node.Op {
	case syntax.OpLiteral:
		parts := make([]*syntax.Regexp, 0, len(node.Rune))
		for _, r := range node.Rune {
			atom := &syntax.Regexp{Op: syntax.OpLiteral, Rune: []rune{r}, Flags: node.Flags}
			parts = append(parts, gapBefore(atom, separator, consumed))
		}
		if len(parts) == 1 {
			return parts[0]
		}
		return &syntax.Regexp{Op: syntax.OpConcat, Sub: parts}
	case syntax.OpCharClass, syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return gapBefore(node, separator, consumed)
	case syntax.OpConcat, syntax.OpCapture:
		for i := range node.Sub {
			node.Sub[i] = withGaps(node.Sub[i], separator, consumed)
		}
		return node
	case syntax.OpAlternate:
		start, after := *consumed, *consumed
		for i := range node.Sub {
			branch := start
			node.Sub[i] = withGaps(node.Sub[i], separator, &branch)
			after = after || branch
		}
		*consumed = after
		return node
	case syntax.OpStar, syntax.OpPlus, syntax.OpQuest, syntax.OpRepeat:
		// Every shape opens with a literal prefix, so a repetition is never the
		// first thing a match consumes. If one ever is, its first iteration would
		// need no gap and the rest would; fail loudly rather than guess.
		if !*consumed {
			panic("redaction: gap-tolerant rewrite reached a repetition before any literal")
		}
		node.Sub[0] = withGaps(node.Sub[0], separator, consumed)
		return node
	case syntax.OpWordBoundary, syntax.OpNoWordBoundary, syntax.OpBeginLine, syntax.OpEndLine,
		syntax.OpBeginText, syntax.OpEndText, syntax.OpEmptyMatch:
		return node
	default:
		panic(fmt.Sprintf("redaction: gap-tolerant rewrite does not handle %v", node.Op))
	}
}

func gapBefore(atom, separator *syntax.Regexp, consumed *bool) *syntax.Regexp {
	if !*consumed {
		*consumed = true
		return atom
	}
	gap := &syntax.Regexp{Op: syntax.OpStar, Sub: []*syntax.Regexp{separator}}
	return &syntax.Regexp{Op: syntax.OpConcat, Sub: []*syntax.Regexp{gap, atom}}
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
// nearly all text, so the contiguous path keeps its behavior exactly.
//
// It runs over the contiguous matchers' output and stops at every replacement
// they left behind: those mark text already accounted for, and a match reaching
// across one would be reaching out of one credential and into the next.
//
// It runs at most twice. The second pass exists for a credential glued straight
// onto the end of another split one: it has no word boundary of its own until
// the first is replaced, and the replacement then supplies one, the same way the
// contiguous shapes cascade into each other. Two passes and no more keeps the
// cost linear; a longer chain of glued split credentials is left as the
// contiguous matchers leave a chain of glued whole ones.
func redactControlSplitSecrets(value, replacement string) string {
	if !containsSplitSeparator(value) {
		return value
	}
	once := redactSplitRegions(value, replacement, true)
	if once == value {
		return value
	}
	// Ungated: the credential the second pass exists for is often contiguous
	// itself (a whole JWT glued onto a split AWS key), so its region can hold no
	// separator at all once the first pass has cut the text around it.
	return redactSplitRegions(once, replacement, false)
}

func redactSplitRegions(value, replacement string, gated bool) string {
	if replacement == "" {
		return redactSplitRegion(value, replacement, 0, gated)
	}
	regions := strings.Split(value, replacement)
	if len(regions) == 1 {
		return redactSplitRegion(value, replacement, 0, gated)
	}
	previous := byte(0)
	for i, region := range regions {
		regions[i] = redactSplitRegion(region, replacement, previous, gated)
		previous = replacement[len(replacement)-1]
	}
	return strings.Join(regions, replacement)
}

// redactSplitRegion runs every gap-tolerant shape over one stretch of text with
// no replacement inside it and replaces the UNION of what they found.
//
// Independently, not one after another. An unbounded body with gaps allowed will
// run on across a separator into whatever follows, and when that is the start of
// another credential it takes that credential's prefix with it: in
// "sk-ant-…<ESC>build<ESC>eyJ….eyJ….sig" the Anthropic shape stops only at the
// JWT's first dot. Applied in sequence, the JWT shape then finds its header gone
// and the payload and signature are left in the clear. Matched against the same
// text, the JWT shape still finds the whole token, and the union covers both.
//
// previous is the byte immediately before the region in the original, or zero at
// the start of the string. It is lent to the engine in front of the region so a
// leading \b is judged against the real neighbour, and a match is never allowed
// to begin on it.
func redactSplitRegion(value, replacement string, previous byte, gated bool) string {
	if gated && !containsSplitSeparator(value) {
		return value
	}
	text, lead := value, 0
	if previous != 0 {
		text, lead = string([]byte{previous})+value, 1
	}
	var spans [][]int
	for _, span := range splitOpenAIPattern.FindAllStringIndex(text, -1) {
		if span[0] >= lead && openAIShapeIsSecret(stripSplitSeparators(text[span[0]:span[1]])) {
			spans = append(spans, span)
		}
	}
	for _, pattern := range splitTextPatterns {
		for _, span := range pattern.FindAllStringIndex(text, -1) {
			if span[0] >= lead {
				spans = append(spans, span)
			}
		}
	}
	return spliceSpans(text, spans, replacement)[lead:]
}

// spliceSpans replaces every named range of value with replacement, merging
// ranges that overlap so one stretch of text is never replaced twice.
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
			// Overlaps the stretch just replaced: extend it rather than emit a
			// second marker, which would claim two credentials where there was one
			// stretch of text.
			if span[1] > written {
				written = span[1]
			}
			continue
		}
		builder.WriteString(value[written:span[0]])
		builder.WriteString(replacement)
		written = span[1]
	}
	builder.WriteString(value[written:])
	return builder.String()
}
