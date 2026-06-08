// Package secrets provides a lightweight, dependency-free scanner for
// high-confidence secret patterns (cloud keys, provider tokens, private keys,
// JWTs). It complements the boundary scrubbing of the configured API key by
// catching OTHER secrets that a command or diff happens to print, so they are
// not echoed back into the model context. It deliberately favors precision over
// recall: only well-shaped, distinctive patterns match, to avoid false positives
// on ordinary output.
package secrets

import (
	"regexp"
	"sort"
	"strings"
)

// Finding is one detected secret occurrence.
type Finding struct {
	Type  string // category, e.g. "aws_access_key_id"
	Match string // the exact matched text (used for redaction)
}

type pattern struct {
	typ string
	re  *regexp.Regexp
}

// patterns are intentionally specific (fixed prefixes / structural shapes) so
// they don't fire on arbitrary identifiers.
var patterns = []pattern{
	{"aws_access_key_id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"github_token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36}`)},
	{"github_pat", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{22,}`)},
	{"slack_token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"google_api_key", regexp.MustCompile(`AIza[0-9A-Za-z\-_]{35}`)},
	{"openai_key", regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`)},
	// Match the ENTIRE PEM/OpenSSH block (header THROUGH the END marker, body
	// included) so redaction removes the key material, not just the header.
	{"private_key_block", regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----.*?-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----`)},
	{"jwt", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
}

// Scan returns the distinct secrets found in text (deduplicated by match,
// sorted by type then match for deterministic output).
func Scan(text string) []Finding {
	if text == "" {
		return nil
	}
	seen := map[string]Finding{}
	for _, p := range patterns {
		for _, m := range p.re.FindAllString(text, -1) {
			if _, ok := seen[m]; !ok {
				seen[m] = Finding{Type: p.typ, Match: m}
			}
		}
	}
	if len(seen) == 0 {
		return nil
	}
	out := make([]Finding, 0, len(seen))
	for _, f := range seen {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Match < out[j].Match
	})
	return out
}

// Redact replaces every detected secret in text with a typed placeholder and
// returns the redacted text plus the findings. When nothing matches it returns
// the input unchanged and a nil slice.
func Redact(text string) (string, []Finding) {
	findings := Scan(text)
	if len(findings) == 0 {
		return text, nil
	}
	redacted := text
	for _, f := range findings {
		redacted = strings.ReplaceAll(redacted, f.Match, "[REDACTED:"+f.Type+"]")
	}
	return redacted, findings
}
