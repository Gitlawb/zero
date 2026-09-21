package redaction

import (
	"strings"
	"testing"
)

const (
	awsKey       = "AKIAIOSFODNN7EXAMPLE"
	anthropicKey = "sk-ant-api03-aaaaaaaaaaaaaaaaaaaaaaaa0123456789ABCD"
	githubKey    = "ghp_cccccccccccccccccccccccccccccccccccc"
	openaiKey    = "sk-proj-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// rejoin drops every character a reader would never see, which is what a
// terminal, a log viewer or a copy-paste does to this output. If the secret is
// back after that, the redaction did not happen.
func rejoin(value string) string {
	return strings.Map(func(r rune) rune {
		if splitSecretSeparator(r) {
			return -1
		}
		return r
	}, value)
}

// AN INVISIBLE BYTE INSIDE A KEY MUST NOT END THE MATCH.
//
// Every shape matcher describes a contiguous body, so one NUL or ESC in the
// middle of a key ended the match and both fragments were emitted. Dropping the
// byte — which is what the next reader does, because it was never displayed —
// put the original key back. Reported in #969 for NUL and ESC; this covers the
// classes those two belong to, because every member behaves the same way.
func TestRedactStringRedactsSecretsSplitByInvisibleCharacters(t *testing.T) {
	separators := map[string]string{
		"NUL":               "\x00",
		"ESC":               "\x1b",
		"BEL":               "\x07",
		"backspace":         "\x08",
		"vertical tab":      "\x0b",
		"form feed":         "\x0c",
		"DEL":               "\x7f",
		"C1 NEL":            "\u0085",
		"C1 CSI":            "\u009b",
		"zero width space":  "\u200b",
		"zero width joiner": "\u200d",
		"word joiner":       "\u2060",
		"byte order mark":   "\ufeff",
		"soft hyphen":       "\u00ad",
	}
	// Every entry has to BE a separator, or its leg passes vacuously: inserting an
	// ordinary character into a key also stops the secret matching, and the rejoin
	// below would then find nothing. An escape mangled on the way into this file is
	// exactly how that happens.
	for name, separator := range separators {
		if runes := []rune(separator); len(runes) != 1 || !splitSecretSeparator(runes[0]) {
			t.Fatalf("SETUP INVALID: %s is %q, which is not a single invisible separator", name, separator)
		}
	}
	secrets := map[string]string{"aws": awsKey, "anthropic": anthropicKey, "github": githubKey, "openai": openaiKey}
	for secretName, secret := range secrets {
		// The premise: the unsplit form already redacts, so a failure below is
		// about the separator and not about the shape being unknown.
		if out := RedactString(secret, Options{}); strings.Contains(out, secret) {
			t.Fatalf("SETUP INVALID: the unsplit %s key is not redacted at all: %q", secretName, out)
		}
		for sepName, separator := range separators {
			for _, at := range []int{1, len(secret) / 2, len(secret) - 1} {
				split := secret[:at] + separator + secret[at:]
				out := RedactString(split, Options{})
				if got := rejoin(out); strings.Contains(got, secret) {
					t.Errorf("%s key split by %s at byte %d survives: %q rejoins to %q", secretName, sepName, at, out, got)
				}
			}
		}
	}
}

// The replacement has to cover the ORIGINAL bytes, separators included. Leaving
// the separator behind would emit `[REDACTED]\x00[REDACTED]` and tell a reader
// there were two secrets, and leaving either fragment behind is the leak itself.
func TestRedactStringCoversTheSeparatorsInsideASplitSecret(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value string
		want  string
	}{
		{"one separator", "key=" + awsKey[:8] + "\x00" + awsKey[8:], "key=[REDACTED]"},
		{"several separators", "key=" + strings.Join(strings.Split(awsKey, ""), "\x1b"), "key=[REDACTED]"},
		{"adjacent separators", "key=" + awsKey[:8] + "\x00\x1b\u200b" + awsKey[8:], "key=[REDACTED]"},
		{"two split secrets", awsKey[:8] + "\x00" + awsKey[8:] + " and " + githubKey[:8] + "\x00" + githubKey[8:], "[REDACTED] and [REDACTED]"},
		{"separator before the secret", "\x00" + awsKey, "\x00[REDACTED]"},
		{"separator after the secret", awsKey + "\x00", "[REDACTED]\x00"},
		{"text around it survives", "before " + awsKey[:4] + "\x00" + awsKey[4:] + " after", "before [REDACTED] after"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := RedactString(testCase.value, Options{}); got != testCase.want {
				t.Errorf("RedactString(...) = %q, want %q", got, testCase.want)
			}
		})
	}
}

// NOTHING ELSE MOVES. The compaction exists to find a match; it must never
// reach text that has no secret in it, and text with no invisible character at
// all must take exactly the path it took before.
func TestRedactStringLeavesOrdinaryTextWithControlCharactersAlone(t *testing.T) {
	for _, value := range []string{
		"a plain sentence",
		"a sentence\x00with a NUL in it",
		"\x1b[31mcolored output\x1b[0m",
		"tab\tseparated\tcolumns",
		"line one\nline two\r\nline three",
		"zero\u200bwidth\u200bspaces",
		"", "\x00", "\x00\x00\x00",
		"not-a-secret-just-a-long-kebab-case-identifier-here",
		"sk-some-kebab-case-value-without-any-digits-at-all",
	} {
		if got := RedactString(value, Options{}); got != value {
			t.Errorf("RedactString(%q) = %q, want it unchanged", value, got)
		}
	}
}

// The openai filter that keeps kebab-case identifiers out of the redactor has
// to apply on the split path too, or working around the false positive would
// become a matter of inserting a control byte.
func TestSplitPathKeepsTheOpenAIKebabCaseFilter(t *testing.T) {
	kebab := "sk-some-kebab-case-value-without-digits"
	split := kebab[:6] + "\x00" + kebab[6:]
	if got := RedactString(split, Options{}); got != split {
		t.Errorf("a kebab-case identifier split by a NUL was redacted: %q", got)
	}
	// ... while a real key of the same family, split the same way, is not spared.
	realKey := openaiKey[:6] + "\x00" + openaiKey[6:]
	if got := RedactString(realKey, Options{}); strings.Contains(rejoin(got), openaiKey) {
		t.Errorf("a real openai key split by a NUL survived: %q", got)
	}
}

// TAB, NEWLINE AND CARRIAGE RETURN ARE OUT OF SCOPE ON PURPOSE, and this says
// so in a place that fails if someone changes it without meaning to. They are
// real text structure: stripping them would let one match span a line break and
// replace unrelated lines, and a credential broken across a line is visible to
// whoever reads it rather than hidden from them.
func TestLineStructureIsNotTreatedAsAnInvisibleSeparator(t *testing.T) {
	for name, separator := range map[string]string{"tab": "\t", "newline": "\n", "carriage return": "\r"} {
		if splitSecretSeparator([]rune(separator)[0]) {
			t.Errorf("%s is treated as an invisible separator; if that is now wanted, this test and the note in control_split.go both need to change", name)
		}
		split := awsKey[:8] + separator + awsKey[8:]
		if got := RedactString(split, Options{}); got != split {
			t.Errorf("%s split was redacted: %q. That may be an improvement, but it is a scope change this test is here to make deliberate", name, got)
		}
	}
}

// Malformed UTF-8 must not move the span mapping, which works in the source's
// own byte offsets. A decoder that measured an invalid byte as the three bytes
// of U+FFFD would shift every later span.
// The exact output is asserted, not just the absence of the key. An invalid
// byte measured as three would still cover the secret while moving the
// replacement's edges onto the bytes around it, which reads as a pass if the
// test only asks whether the secret is gone.
func TestSplitRedactionHandlesMalformedUTF8(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		value string
		want  string
	}{
		{"invalid byte before the secret", "\xff" + awsKey[:8] + "\x00" + awsKey[8:], "\xff[REDACTED]"},
		// An invalid byte is NOT one of the separators: it decodes to U+FFFD and
		// is drawn, so a key broken by one is visible to whoever reads it, the
		// same argument that keeps tab and newline out. It is kept in the
		// compacted text and so it still ends the match.
		{"invalid byte inside the split", awsKey[:8] + "\x00\xff" + awsKey[8:], awsKey[:8] + "\x00\xff" + awsKey[8:]},
		{"invalid byte after the secret", awsKey[:8] + "\x00" + awsKey[8:] + "\xfe", "[REDACTED]\xfe"},
		{"invalid bytes on both sides", "\xff\xfe" + awsKey[:8] + "\x00" + awsKey[8:] + "\xfd\xfc", "\xff\xfe[REDACTED]\xfd\xfc"},
		{"invalid byte then text then the secret", "\xffpre " + awsKey[:8] + "\x00" + awsKey[8:] + " post", "\xffpre [REDACTED] post"},
		{"only invalid bytes", "\xff\xfe\x00\xff", "\xff\xfe\x00\xff"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := RedactString(testCase.value, Options{})
			if got != testCase.want {
				t.Errorf("RedactString(%q) = %q, want %q", testCase.value, got, testCase.want)
			}
			if strings.Contains(rejoin(got), awsKey) {
				t.Errorf("the key survived: %q", got)
			}
		})
	}
}

// The compaction is only a lens for finding the match. A string with no
// separator at all must come back byte-identical, including its invalid bytes.
func TestCompactionDoesNotRewriteTextItDoesNotRedact(t *testing.T) {
	for _, value := range []string{"plain", "with\xffinvalid", "tab\there", "\xef\xbb\xbfbom at the front"} {
		if got := redactControlSplitSecrets(value, RedactedSecret); got != value {
			t.Errorf("redactControlSplitSecrets(%q) = %q, want it unchanged", value, got)
		}
	}
}
