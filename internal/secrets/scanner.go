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
// A leading \b anchors each prefixed pattern to a word boundary so it can't fire
// mid-word — e.g. "sk-" inside "task-management-and-coordination" must NOT match
// as an openai_key. Real secrets are preceded by a delimiter (space, quote, =, :,
// start-of-string), all of which satisfy \b.
var patterns = []pattern{
	{"aws_access_key_id", regexp.MustCompile(`\bAKIA[0-9A-Z]{16}`)},
	{"github_token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36}`)},
	{"github_pat", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}`)},
	{"slack_token", regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`)},
	{"google_api_key", regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35}`)},
	// Body allows - and _ so modern prefixed keys (sk-proj-…, sk-svcacct-…) match,
	// not just the legacy sk-<alnum> shape.
	{"openai_key", regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	// Match the ENTIRE PEM/OpenSSH block (header THROUGH the END marker, body
	// included) so redaction removes the key material, not just the header.
	{"private_key_block", regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY-----.*?-----END (?:[A-Z0-9]+ )*PRIVATE KEY-----`)},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
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
	// Replace longest matches first so a containing secret (e.g. a whole PEM
	// PRIVATE KEY block) is redacted before any shorter secret nested inside its
	// body. Redacting the inner match first would corrupt the outer block's exact
	// string, leaving its BEGIN/END header un-redacted. findings is sorted by
	// type for the returned API contract, so order replacements on a copy.
	order := make([]Finding, len(findings))
	copy(order, findings)
	sort.SliceStable(order, func(i, j int) bool {
		return len(order[i].Match) > len(order[j].Match)
	})
	redacted := text
	for _, f := range order {
		redacted = strings.ReplaceAll(redacted, f.Match, "[REDACTED:"+f.Type+"]")
	}
	return redacted, findings
}
