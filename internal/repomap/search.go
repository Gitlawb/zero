package repomap

import (
	"path"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	MatchReasonExactBasename  = "exact-basename"
	MatchReasonPrefixBasename = "prefix-basename"
	MatchReasonPathSegment    = "path-segment"
	MatchReasonSubstring      = "substring"
	MatchReasonFuzzy          = "fuzzy"
	MatchReasonMultiTerm      = "multi-term"
)

const (
	scoreExactBasename  = 1000
	scorePrefixBasename = 800
	scorePathSegment    = 600
	scoreSubstring      = 400
	scoreFuzzy          = 200
	scoreAllTermsBonus  = 300
)

type SearchResult struct {
	Path   string
	Score  int
	Reason string
}

func Search(snapshot Snapshot, query string, limit int) []SearchResult {
	normalizedQuery := normalizeSearchQuery(query)
	if normalizedQuery == "" || limit <= 0 {
		return []SearchResult{}
	}
	terms := searchTerms(normalizedQuery)
	if len(terms) == 0 {
		return []SearchResult{}
	}

	results := []SearchResult{}
	for _, file := range snapshot.Files {
		displayPath := strings.TrimSpace(file.Path)
		normalizedPath := normalizeSearchPath(displayPath)
		if normalizedPath == "" {
			continue
		}
		score, reason, ok := scoreSearchPath(normalizedPath, normalizedQuery, terms)
		if !ok {
			continue
		}
		results = append(results, SearchResult{
			Path:   displayPath,
			Score:  score,
			Reason: reason,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Path < results[j].Path
	})
	if len(results) > limit {
		return results[:limit]
	}
	return results
}

func normalizeSearchQuery(query string) string {
	normalized := strings.TrimSpace(query)
	normalized = strings.TrimPrefix(normalized, "@")
	normalized = strings.TrimSpace(normalized)
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	normalized = strings.Trim(normalized, "/")
	return strings.ToLower(normalized)
}

func searchTerms(query string) []string {
	fields := strings.FieldsFunc(query, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	terms := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, "/")
		if field != "" {
			terms = append(terms, field)
		}
	}
	return terms
}

func normalizeSearchPath(filePath string) string {
	normalized := strings.TrimSpace(filePath)
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	for strings.HasPrefix(normalized, "./") {
		normalized = strings.TrimPrefix(normalized, "./")
	}
	return strings.ToLower(normalized)
}

func scoreSearchPath(filePath string, query string, terms []string) (int, string, bool) {
	if len(terms) == 1 {
		return scorePath(filePath, query)
	}
	score := 0
	matched := 0
	bestReason := ""
	for _, term := range terms {
		_, reason, ok := scorePath(filePath, term)
		if ok {
			termScore := scoreForMultiTermReason(reason)
			score += termScore
			matched++
			if bestReason == "" || termScore > scoreForMultiTermReason(bestReason) {
				bestReason = reason
			}
		}
	}
	if matched == 0 {
		return 0, "", false
	}
	if matched == len(terms) {
		score += scoreAllTermsBonus
		score += scoreSubstring
		return score, MatchReasonMultiTerm, true
	}
	return score, bestReason, true
}

func scorePath(filePath string, query string) (int, string, bool) {
	basename := path.Base(filePath)
	switch {
	case basename == query:
		return scoreExactBasename, MatchReasonExactBasename, true
	case strings.HasPrefix(basename, query):
		return scorePrefixBasename, MatchReasonPrefixBasename, true
	case hasPathSegment(filePath, query):
		return scorePathSegment, MatchReasonPathSegment, true
	case strings.Contains(filePath, query):
		return scoreSubstring, MatchReasonSubstring, true
	case fuzzyMatch(filePath, query):
		return scoreFuzzy, MatchReasonFuzzy, true
	default:
		return 0, "", false
	}
}

func scoreForMultiTermReason(reason string) int {
	switch reason {
	case MatchReasonExactBasename:
		return 700
	case MatchReasonPathSegment:
		return 650
	case MatchReasonPrefixBasename:
		return 500
	case MatchReasonSubstring:
		return 350
	case MatchReasonFuzzy:
		return 100
	default:
		return 0
	}
}

func hasPathSegment(filePath string, query string) bool {
	for _, segment := range strings.Split(filePath, "/") {
		if segment == query {
			return true
		}
	}
	return false
}

func fuzzyMatch(filePath string, query string) bool {
	position := 0
	for _, char := range query {
		index := strings.IndexRune(filePath[position:], char)
		if index < 0 {
			return false
		}
		position += index + utf8.RuneLen(char)
	}
	return true
}
