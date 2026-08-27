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
	// shape so the patterns miss it unless controls are stripped first.
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
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := prefix + tc.split + body
			got := RedactString(input, Options{})
			if strings.Contains(got, body) {
				t.Fatalf("secret split by %s leaked in %q", tc.name, got)
			}
			if strings.Contains(got, prefix) {
				t.Fatalf("secret prefix split by %s leaked in %q", tc.name, got)
			}
			if !strings.Contains(got, RedactedSecret) {
				t.Fatalf("expected %q after %s split, got %q", RedactedSecret, tc.name, got)
			}
		})
	}
}
