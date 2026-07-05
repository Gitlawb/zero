package tools

// Fuzzy fallback matching for edit_file. When the model's old_string fails to
// match byte-for-byte (drifted indentation, collapsed whitespace, escaped
// characters, a slightly-misremembered middle line), these replacers propose
// candidate spans that plausibly correspond to what the model intended. Only a
// candidate that occurs literally in the file is accepted, so the replacement
// itself is always exact even when the match was tolerant.
//
// Strategy cascade ported from opencode's edit tool, whose replacers were in
// turn distilled from Cline's diff-apply evals and gemini-cli's edit corrector.

import (
	"errors"
	"regexp"
	"sort"
	"strings"
)

// editReplacer proposes candidate spans for find inside content. Candidates
// are re-validated by fuzzyEditMatch before use.
type editReplacer func(content, find string) []string

var (
	errEditFuzzyNotFound  = errors.New("no fuzzy match for old_string")
	errEditFuzzyAmbiguous = errors.New("fuzzy match for old_string is ambiguous")
)

// Minimum average middle-line similarity for block-anchor matches.
const editAnchorSimilarityThreshold = 0.65

// fuzzyEditMatch runs the replacer cascade and returns the exact span of
// content to replace. When replaceAll is false the span must be unique in
// content; an ambiguous candidate is skipped in favor of later candidates and
// only reported if nothing unique is found. A span wildly larger than
// old_string is refused outright rather than risking a destructive edit.
func fuzzyEditMatch(content, find string, replaceAll bool) (string, error) {
	replacers := []editReplacer{
		lineTrimmedReplacer,
		blockAnchorReplacer,
		whitespaceNormalizedReplacer,
		indentationFlexibleReplacer,
		escapeNormalizedReplacer,
		trimmedBoundaryReplacer,
		contextAwareReplacer,
	}
	found := false
	for _, replacer := range replacers {
		// Collect the replacer's DISTINCT candidate spans that literally occur in
		// content. Two or more distinct spans from one strategy (e.g. duplicate
		// blocks at different indentation, each occurring once) mean the model's
		// intent is genuinely ambiguous — silently editing the first would be a
		// wrong-span write, so it is rejected instead.
		var candidates []string
		seen := map[string]bool{}
		for _, search := range replacer(content, find) {
			if search == "" || seen[search] {
				continue
			}
			seen[search] = true
			if !strings.Contains(content, search) {
				continue
			}
			candidates = append(candidates, search)
		}
		if len(candidates) == 0 {
			continue
		}
		found = true
		if !replaceAll && len(candidates) > 1 {
			return "", errEditFuzzyAmbiguous
		}
		search := candidates[0]
		if isDisproportionateEditMatch(search, find) {
			return "", errors.New("refusing replacement because the matched span is much larger than old_string; re-read the file and provide the full exact old_string for the intended replacement")
		}
		if replaceAll {
			return search, nil
		}
		if strings.Index(content, search) == strings.LastIndex(content, search) {
			return search, nil
		}
		// The single candidate occurs at multiple positions; a later, stricter
		// strategy may still resolve a unique span, so keep cascading.
	}
	if !found {
		return "", errEditFuzzyNotFound
	}
	return "", errEditFuzzyAmbiguous
}

// isDisproportionateEditMatch guards against anchor-style replacers matching a
// span far larger than the text the model asked to replace (e.g. first/last
// line anchors bridging hundreds of unrelated lines).
func isDisproportionateEditMatch(search, find string) bool {
	findLines := strings.Count(find, "\n") + 1
	searchLines := strings.Count(search, "\n") + 1
	limit := findLines + 3
	if findLines*2 > limit {
		limit = findLines * 2
	}
	if searchLines >= limit {
		return true
	}
	if findLines == 1 {
		return false
	}
	searchTrimmed := len(strings.TrimSpace(search))
	findTrimmed := len(strings.TrimSpace(find))
	byteLimit := findTrimmed + 500
	if findTrimmed*4 > byteLimit {
		byteLimit = findTrimmed * 4
	}
	return searchTrimmed > byteLimit
}

// splitFindLines splits find into lines, dropping the trailing empty line a
// trailing newline produces so windows align with real content lines.
func splitFindLines(find string) []string {
	lines := strings.Split(find, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// lineTrimmedReplacer matches when every line equals the corresponding
// old_string line after trimming surrounding whitespace.
func lineTrimmedReplacer(content, find string) []string {
	contentLines := strings.Split(content, "\n")
	findLines := splitFindLines(find)
	if len(findLines) == 0 {
		return nil
	}
	var candidates []string
	for i := 0; i+len(findLines) <= len(contentLines); i++ {
		matches := true
		for j := range findLines {
			if strings.TrimSpace(contentLines[i+j]) != strings.TrimSpace(findLines[j]) {
				matches = false
				break
			}
		}
		if matches {
			candidates = append(candidates, strings.Join(contentLines[i:i+len(findLines)], "\n"))
		}
	}
	return candidates
}

// blockAnchorReplacer anchors on the first and last lines (trimmed) and
// accepts the block when the middle lines average >= the similarity threshold
// (Levenshtein-based), tolerating a slightly misremembered interior.
func blockAnchorReplacer(content, find string) []string {
	findLines := splitFindLines(find)
	if len(findLines) < 3 {
		return nil
	}
	contentLines := strings.Split(content, "\n")
	firstAnchor := strings.TrimSpace(findLines[0])
	lastAnchor := strings.TrimSpace(findLines[len(findLines)-1])
	searchBlockSize := len(findLines)
	maxLineDelta := searchBlockSize / 4
	if maxLineDelta < 1 {
		maxLineDelta = 1
	}

	type span struct{ start, end int }
	var candidates []span
	for i := 0; i < len(contentLines); i++ {
		if strings.TrimSpace(contentLines[i]) != firstAnchor {
			continue
		}
		// Only the first occurrence of the last anchor after this start counts,
		// mirroring the reference implementation: a farther-away closing line is
		// assumed to close a different block.
		for j := i + 2; j < len(contentLines); j++ {
			if strings.TrimSpace(contentLines[j]) != lastAnchor {
				continue
			}
			actualBlockSize := j - i + 1
			delta := actualBlockSize - searchBlockSize
			if delta < 0 {
				delta = -delta
			}
			if delta <= maxLineDelta {
				candidates = append(candidates, span{start: i, end: j})
			}
			break
		}
	}
	if len(candidates) == 0 {
		return nil
	}

	middleSimilarity := func(candidate span) float64 {
		actualBlockSize := candidate.end - candidate.start + 1
		linesToCheck := searchBlockSize - 2
		if actualBlockSize-2 < linesToCheck {
			linesToCheck = actualBlockSize - 2
		}
		if linesToCheck <= 0 {
			return 1.0
		}
		similarity := 0.0
		for j := 1; j < searchBlockSize-1 && j < actualBlockSize-1; j++ {
			contentLine := strings.TrimSpace(contentLines[candidate.start+j])
			findLine := strings.TrimSpace(findLines[j])
			maxLen := len(contentLine)
			if len(findLine) > maxLen {
				maxLen = len(findLine)
			}
			if maxLen == 0 {
				continue
			}
			distance := levenshtein(contentLine, findLine)
			similarity += 1 - float64(distance)/float64(maxLen)
		}
		return similarity / float64(linesToCheck)
	}

	// Return EVERY candidate that clears the similarity threshold, best first.
	// Picking only the best would hide competing blocks from fuzzyEditMatch's
	// distinct-candidate ambiguity check and could silently edit the wrong
	// block; with all qualifying spans surfaced, one clear winner still
	// resolves (single candidate) while two plausible blocks are rejected as
	// ambiguous. replaceAll consumers take the first (most similar) span.
	type scoredSpan struct {
		span       span
		similarity float64
	}
	var qualifying []scoredSpan
	for _, candidate := range candidates {
		if s := middleSimilarity(candidate); s >= editAnchorSimilarityThreshold {
			qualifying = append(qualifying, scoredSpan{span: candidate, similarity: s})
		}
	}
	if len(qualifying) == 0 {
		return nil
	}
	sort.SliceStable(qualifying, func(i, j int) bool {
		return qualifying[i].similarity > qualifying[j].similarity
	})
	spans := make([]string, 0, len(qualifying))
	for _, scored := range qualifying {
		spans = append(spans, strings.Join(contentLines[scored.span.start:scored.span.end+1], "\n"))
	}
	return spans
}

var editWhitespaceRun = regexp.MustCompile(`\s+`)

func normalizeEditWhitespace(text string) string {
	return strings.TrimSpace(editWhitespaceRun.ReplaceAllString(text, " "))
}

// whitespaceNormalizedReplacer matches after collapsing all whitespace runs to
// a single space: full lines, sub-line spans (via a word-boundary regex), and
// multi-line windows.
func whitespaceNormalizedReplacer(content, find string) []string {
	normalizedFind := normalizeEditWhitespace(find)
	if normalizedFind == "" {
		return nil
	}
	contentLines := strings.Split(content, "\n")
	var candidates []string
	var subLinePattern *regexp.Regexp
	for _, line := range contentLines {
		normalizedLine := normalizeEditWhitespace(line)
		if normalizedLine == normalizedFind {
			candidates = append(candidates, line)
			continue
		}
		if !strings.Contains(normalizedLine, normalizedFind) {
			continue
		}
		if subLinePattern == nil {
			words := strings.Fields(find)
			quoted := make([]string, len(words))
			for i, word := range words {
				quoted[i] = regexp.QuoteMeta(word)
			}
			pattern, err := regexp.Compile(strings.Join(quoted, `\s+`))
			if err != nil {
				continue
			}
			subLinePattern = pattern
		}
		if match := subLinePattern.FindString(line); match != "" {
			candidates = append(candidates, match)
		}
	}

	findLines := strings.Split(find, "\n")
	if len(findLines) > 1 {
		for i := 0; i+len(findLines) <= len(contentLines); i++ {
			block := strings.Join(contentLines[i:i+len(findLines)], "\n")
			if normalizeEditWhitespace(block) == normalizedFind {
				candidates = append(candidates, block)
			}
		}
	}
	return candidates
}

// stripCommonIndentation removes the minimum leading-whitespace width shared
// by all non-empty lines, so blocks match regardless of their nesting depth.
func stripCommonIndentation(text string) string {
	lines := strings.Split(text, "\n")
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " \t"))
		if minIndent < 0 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		return text
	}
	stripped := make([]string, len(lines))
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			stripped[i] = line
			continue
		}
		stripped[i] = line[minIndent:]
	}
	return strings.Join(stripped, "\n")
}

func indentationFlexibleReplacer(content, find string) []string {
	normalizedFind := stripCommonIndentation(find)
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	var candidates []string
	for i := 0; i+len(findLines) <= len(contentLines); i++ {
		block := strings.Join(contentLines[i:i+len(findLines)], "\n")
		if stripCommonIndentation(block) == normalizedFind {
			candidates = append(candidates, block)
		}
	}
	return candidates
}

var editEscapeSequence = regexp.MustCompile("\\\\(n|t|r|'|\"|`|\\\\|\\n|\\$)")

// unescapeEditString undoes one level of string escaping (\n, \t, \", \\, a
// backslash-newline continuation, \$) — the model sometimes reproduces file
// content as it appeared inside a quoted string literal.
func unescapeEditString(text string) string {
	return editEscapeSequence.ReplaceAllStringFunc(text, func(match string) string {
		switch match[1:] {
		case "n":
			return "\n"
		case "t":
			return "\t"
		case "r":
			return "\r"
		case "\n":
			return "\n"
		default:
			// ', ", `, \, $ all unescape to themselves.
			return match[1:]
		}
	})
}

func escapeNormalizedReplacer(content, find string) []string {
	unescapedFind := unescapeEditString(find)
	var candidates []string
	if strings.Contains(content, unescapedFind) {
		candidates = append(candidates, unescapedFind)
	}
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(unescapedFind, "\n")
	for i := 0; i+len(findLines) <= len(contentLines); i++ {
		block := strings.Join(contentLines[i:i+len(findLines)], "\n")
		if unescapeEditString(block) == unescapedFind {
			candidates = append(candidates, block)
		}
	}
	return candidates
}

// trimmedBoundaryReplacer tolerates stray leading/trailing whitespace (often
// blank lines) around an otherwise-exact old_string.
func trimmedBoundaryReplacer(content, find string) []string {
	trimmedFind := strings.TrimSpace(find)
	if trimmedFind == find || trimmedFind == "" {
		return nil
	}
	var candidates []string
	if strings.Contains(content, trimmedFind) {
		candidates = append(candidates, trimmedFind)
	}
	contentLines := strings.Split(content, "\n")
	findLines := strings.Split(find, "\n")
	for i := 0; i+len(findLines) <= len(contentLines); i++ {
		block := strings.Join(contentLines[i:i+len(findLines)], "\n")
		if strings.TrimSpace(block) == trimmedFind {
			candidates = append(candidates, block)
		}
	}
	return candidates
}

// contextAwareReplacer anchors on the first and last lines and accepts an
// equal-length block when at least half of its non-empty middle lines match
// after trimming — a cheaper, stricter cousin of blockAnchorReplacer.
func contextAwareReplacer(content, find string) []string {
	findLines := splitFindLines(find)
	if len(findLines) < 3 {
		return nil
	}
	contentLines := strings.Split(content, "\n")
	firstAnchor := strings.TrimSpace(findLines[0])
	lastAnchor := strings.TrimSpace(findLines[len(findLines)-1])
	var candidates []string
	for i := 0; i < len(contentLines); i++ {
		if strings.TrimSpace(contentLines[i]) != firstAnchor {
			continue
		}
		for j := i + 2; j < len(contentLines); j++ {
			if strings.TrimSpace(contentLines[j]) != lastAnchor {
				continue
			}
			if j-i+1 == len(findLines) {
				matching, nonEmpty := 0, 0
				for k := 1; k < len(findLines)-1; k++ {
					blockLine := strings.TrimSpace(contentLines[i+k])
					findLine := strings.TrimSpace(findLines[k])
					if blockLine == "" && findLine == "" {
						continue
					}
					nonEmpty++
					if blockLine == findLine {
						matching++
					}
				}
				if nonEmpty == 0 || float64(matching)/float64(nonEmpty) >= 0.5 {
					candidates = append(candidates, strings.Join(contentLines[i:j+1], "\n"))
				}
			}
			break
		}
	}
	return candidates
}

// adaptReplacementToSpan re-shapes the model's replacement to the span a
// tolerant matcher resolved. When old_string only matched after normalization,
// new_string was written at old_string's (wrong) shape, so applying it raw
// would strip the file's indentation or drop a trailing CR:
//
//  1. Uniform re-indent: when every span line equals delta + the corresponding
//     find line's indentation (the line-trimmed / indentation-flexible shapes),
//     the same delta is prepended to every non-blank replacement line. Any
//     line that breaks the uniform-delta relationship disables the shift —
//     block-anchor matches with a drifted interior are left untouched.
//  2. Trailing CR: a span from a CRLF file ends mid-line at "\r" (candidates
//     are built by joining lines split on "\n"); the replacement gets the same
//     trailing "\r" so the file's CRLF pairs stay intact.
func adaptReplacementToSpan(span, find, replacement string) string {
	if delta, ok := uniformIndentDelta(span, find); ok && delta != "" {
		lines := strings.Split(replacement, "\n")
		for i, line := range lines {
			if strings.TrimSpace(line) == "" {
				continue
			}
			lines[i] = delta + line
		}
		replacement = strings.Join(lines, "\n")
	}
	if strings.HasSuffix(span, "\r") && !strings.HasSuffix(replacement, "\r") {
		replacement += "\r"
	}
	return replacement
}

// uniformIndentDelta returns the indentation prefix that, prepended to every
// non-blank find line, yields the corresponding span line's indentation. ok is
// false when line counts differ, any line pair disagrees on the delta, or the
// span is not simply a uniformly deeper-indented copy of find.
func uniformIndentDelta(span, find string) (string, bool) {
	spanLines := strings.Split(span, "\n")
	findLines := splitFindLines(find)
	if len(spanLines) != len(findLines) {
		return "", false
	}
	leadingWhitespace := func(line string) string {
		return line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	}
	delta := ""
	haveDelta := false
	for i := range findLines {
		spanLine := strings.TrimSuffix(spanLines[i], "\r")
		findLine := strings.TrimSuffix(findLines[i], "\r")
		if strings.TrimSpace(spanLine) == "" && strings.TrimSpace(findLine) == "" {
			continue
		}
		spanIndent := leadingWhitespace(spanLine)
		findIndent := leadingWhitespace(findLine)
		if !strings.HasSuffix(spanIndent, findIndent) {
			return "", false
		}
		lineDelta := spanIndent[:len(spanIndent)-len(findIndent)]
		if !haveDelta {
			delta = lineDelta
			haveDelta = true
			continue
		}
		if lineDelta != delta {
			return "", false
		}
	}
	return delta, haveDelta
}

// levenshtein computes edit distance with a two-row rolling matrix.
func levenshtein(a, b string) int {
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}
	previous := make([]int, len(b)+1)
	current := make([]int, len(b)+1)
	for j := range previous {
		previous[j] = j
	}
	for i := 1; i <= len(a); i++ {
		current[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			minimum := previous[j] + 1
			if current[j-1]+1 < minimum {
				minimum = current[j-1] + 1
			}
			if previous[j-1]+cost < minimum {
				minimum = previous[j-1] + cost
			}
			current[j] = minimum
		}
		previous, current = current, previous
	}
	return previous[len(b)]
}
