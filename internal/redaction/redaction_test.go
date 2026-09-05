package redaction

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactStringCoversCommonSecretShapes(t *testing.T) {
	input := strings.Join([]string{
		`{"apiKey":"sk-proj-abcdefghijklmnopqrstuvwxyz"}`,
		"authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz123456",
		"https://zero:super-secret@example.test/path?token=glpat-abcdefghijklmnopqrstuvwxyz",
		"-----BEGIN PRIVATE KEY-----\nabc123\n-----END PRIVATE KEY-----",
	}, "\n")

	got := RedactString(input, Options{ExtraSecretValues: []string{"super-secret"}})

	for _, leaked := range []string{
		"sk-proj-abcdefghijklmnopqrstuvwxyz",
		"ghp_abcdefghijklmnopqrstuvwxyz123456",
		"super-secret",
		"glpat-abcdefghijklmnopqrstuvwxyz",
		"abc123",
	} {
		if strings.Contains(got, leaked) {
			t.Fatalf("redacted string leaked %q in %q", leaked, got)
		}
	}
	if count := strings.Count(got, RedactedSecret); count < 5 {
		t.Fatalf("expected multiple redaction markers, got %d in %q", count, got)
	}
}

func TestRedactValueHandlesSensitiveKeysAndCycles(t *testing.T) {
	type node struct {
		Name     string
		Password string
		Next     *node
	}
	root := &node{Name: "root", Password: "open-sesame"}
	root.Next = root

	got := RedactValue(root, Options{})
	asMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map output, got %T", got)
	}
	if asMap["Password"] != RedactedSecret {
		t.Fatalf("expected sensitive key redacted, got %#v", asMap["Password"])
	}
	if asMap["Next"] != CircularReference {
		t.Fatalf("expected circular reference marker, got %#v", asMap["Next"])
	}
}

func TestRedactErrorRedactsMessageStackAndFields(t *testing.T) {
	err := withFieldsError{
		err:    errors.New("request failed with api_key=sk-test-secret1234567890"),
		Token:  "ghp_abcdefghijklmnopqrstuvwxyz123456",
		Detail: "safe",
	}

	got := RedactError(err, Options{})

	if strings.Contains(got.Message, "sk-test-secret") {
		t.Fatalf("message leaked secret: %#v", got)
	}
	if got.Fields["Token"] != RedactedSecret {
		t.Fatalf("token field was not redacted: %#v", got.Fields)
	}
	if got.Fields["Detail"] != "safe" {
		t.Fatalf("non-sensitive field changed: %#v", got.Fields)
	}
}

type withFieldsError struct {
	err    error
	Token  string
	Detail string
}

func (err withFieldsError) Error() string {
	return err.err.Error()
}

func TestRedactValueSharedPointerIsNotMistakenForCycle(t *testing.T) {
	// Two sibling fields referencing the SAME object form a DAG, not a cycle.
	// Both must redact normally; the second must not collapse to CircularReference.
	type leaf struct{ Name string }
	type root struct {
		A *leaf
		B *leaf
	}
	shared := &leaf{Name: "shared"}
	out := RedactValue(root{A: shared, B: shared}, Options{})
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("expected map, got %T", out)
	}
	for _, field := range []string{"A", "B"} {
		sub, ok := m[field].(map[string]any)
		if !ok {
			t.Fatalf("field %s = %#v, want a redacted leaf (sibling DAG must not be %q)", field, m[field], CircularReference)
		}
		if sub["Name"] != "shared" {
			t.Fatalf("field %s Name = %v, want \"shared\"", field, sub["Name"])
		}
	}
}

func TestRedactValueTrueCycleStillDetected(t *testing.T) {
	type node struct {
		Name string
		Next *node
	}
	a := &node{Name: "a"}
	a.Next = a // genuine self-cycle
	out := RedactValue(a, Options{})
	if !containsCircular(out) {
		t.Fatalf("expected a CircularReference marker somewhere in %#v", out)
	}
}

func containsCircular(v any) bool {
	switch t := v.(type) {
	case string:
		return t == CircularReference
	case map[string]any:
		for _, val := range t {
			if containsCircular(val) {
				return true
			}
		}
	case []any:
		for _, val := range t {
			if containsCircular(val) {
				return true
			}
		}
	}
	return false
}

func TestRedactStringCatchesSecretsSplitByControlBytes(t *testing.T) {
	// Unsplit passing is not coverage: a NUL/ESC/C1 in the body splits the
	// shape so the patterns miss it unless matching allows those controls as
	// gaps between body characters (without joining unrelated tokens).
	const prefix = "sk-ant-api03-"
	const body = "abcdefghijklmnopqrstuvwxyz"
	unsplit := prefix + body
	if got := RedactString(unsplit, Options{}); strings.Contains(got, body) {
		t.Fatalf("unsplit secret not redacted (test setup): %q", got)
	}

	cases := []struct {
		name  string
		split string
	}{
		{name: "NUL", split: "\x00"},
		{name: "ESC", split: "\x1b"},
		{name: "C1", split: "\x9b"},
		{name: "UTF-8 C1", split: string(rune(0x9B))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inputs := []struct {
				placement string
				input     string
			}{
				{placement: "prefix-body boundary", input: prefix + tc.split + body},
				{placement: "inside body", input: prefix + body[:13] + tc.split + body[13:]},
			}
			for _, input := range inputs {
				t.Run(input.placement, func(t *testing.T) {
					got := RedactString(input.input, Options{})
					if got != RedactedSecret {
						t.Fatalf("expected %q after %s %s split, got %q", RedactedSecret, tc.name, input.placement, got)
					}
				})
			}
		})
	}
}

func TestRedactStringPreservesControlAfterCredential(t *testing.T) {
	const secret = "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"
	input := "key=" + secret + "\x00path/one.go\x00path/two.go"
	want := "key=" + RedactedSecret + "\x00path/one.go\x00path/two.go"
	if got := RedactString(input, Options{}); got != want {
		t.Fatalf("terminal credential separator changed:\n got=%q\nwant=%q", got, want)
	}
}

func TestRedactStringSuffixCannotDisableOpenAIKeyMatch(t *testing.T) {
	const secret = "sk-aaaaaaaaaaaaaaaaaaaabcdefgh"
	input := "key " + secret + "\x1bkebab-case tail"
	want := "key " + RedactedSecret + "\x1bkebab-case tail"
	if got := RedactString(input, Options{}); got != want {
		t.Fatalf("suffix changed OpenAI key classification:\n got=%q\nwant=%q", got, want)
	}
}

func TestRedactStringDistinguishesInvalidC1FromValidReplacementRune(t *testing.T) {
	const prefix = "sk-ant-api03-"
	const body = "abcdefghijklmnopqrstuvwxyz"

	invalidC1 := prefix + body[:13] + "\x9b" + body[13:]
	if got := RedactString(invalidC1, Options{}); got != RedactedSecret {
		t.Fatalf("raw invalid C1 split was not redacted: %q", got)
	}

	validReplacement := prefix + body[:13] + "\uFFFD" + body[13:]
	got := RedactString(validReplacement, Options{})
	if !strings.Contains(got, "\uFFFD"+body[13:]) {
		t.Fatalf("valid U+FFFD was treated as a control gap: %q", got)
	}
}

func TestRedactStringPreservesAllowedWhitespaceAndUTF8(t *testing.T) {
	input := "safe\tline\nnext\rfinal café"
	if got := RedactString(input, Options{}); got != input {
		t.Fatalf("unexpected normalization: %q", got)
	}
}

func TestRedactStringWordcharBeforeNULAnthropicKey(t *testing.T) {
	// Matching on a control-stripped copy joins "id42" and the key, so \b in
	// textSecretPatterns misses and the secret leaks. Matching on the original
	// treats the NUL as a boundary; leaked must be false.
	const secret = "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"
	if got := RedactString(secret, Options{}); strings.Contains(got, "sk-ant-api03-") {
		t.Fatalf("unsplit secret not redacted (test setup): %q", got)
	}
	input := "id42\x00" + secret
	got := RedactString(input, Options{})
	leaked := strings.Contains(got, secret) || strings.Contains(got, "sk-ant-api03-")
	if leaked {
		t.Fatalf("wordchar-before-NUL+anthropic-key leaked=true out=%q", got)
	}
	if !strings.Contains(got, RedactedSecret) {
		t.Fatalf("wordchar-before-NUL+anthropic-key leaked=false want %q, got %q", RedactedSecret, got)
	}
}

func TestRedactStringControlBytesWithoutSecretStayIdentical(t *testing.T) {
	// scrubResultSecrets sets Result.Redacted when RedactString's result !=
	// Output. Stripping is matching-time only: no-secret control bytes must
	// remain byte-identical so Redacted stays false.
	cases := []struct {
		name  string
		input string
	}{
		{name: "form feed in source", input: "package main\n\ffunc main() {}\n"},
		{name: "Windows-1252 quotes", input: "Don\x92t \x93quote\x94 me\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactString(tc.input, Options{})
			if got != tc.input {
				t.Fatalf("no-secret input not byte-identical:\n in=%q\nout=%q", tc.input, got)
			}
		})
	}
}
