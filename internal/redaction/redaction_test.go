package redaction

import (
	"errors"
	"strings"
	"testing"
)

func TestRedactStringCoversCommonSecretShapes(t *testing.T) {
	input := strings.Join([]string{
		`{"apiKey":"sk-proj-abcdefghijklmnopqrstuvwxyz"}`,
		"authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		"https://zero:super-secret@example.test/path?token=glpat-abcdefghijklmnopqrstuvwxyz",
		"-----BEGIN PRIVATE KEY-----\nabc123\n-----END PRIVATE KEY-----",
	}, "\n")

	got := RedactString(input, Options{ExtraSecretValues: []string{"super-secret"}})

	for _, leaked := range []string{
		"sk-proj-abcdefghijklmnopqrstuvwxyz",
		"ghp_abcdefghijklmnopqrstuvwxyz1234567890",
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
		Token:  "ghp_abcdefghijklmnopqrstuvwxyz1234567890",
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

func BenchmarkRedactString(b *testing.B) {
	sample := strings.Join([]string{
		`{"apiKey":"sk-proj-abcdefghijklmnopqrstuvwxyz1234567890"}`,
		"authorization: Bearer ghp_abcdefghijklmnopqrstuvwxyz1234567890",
		"https://zero:super-secret@example.test/path?token=glpat-abcdefghijklmnopqrstuvwxyz",
		"export AWS_SECRET_ACCESS_KEY=AKIAIOSFODNN7EXAMPLE",
		"normal line with no secrets just log messages and numbers 123456789",
		"another normal line about git commit status and file diff output",
	}, "\n")
	opts := Options{ExtraSecretValues: []string{"super-secret"}}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = RedactString(sample, opts)
	}
}

func BenchmarkRedactStringClean(b *testing.B) {
	cleanText := "normal line with no secrets just log messages and numbers 123456789\nanother line without secrets"
	opts := Options{}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = RedactString(cleanText, opts)
	}
}

func TestRedactStringConditionalGates(t *testing.T) {
	const ghp = "ghp_abcdefghijklmnopqrstuvwxyz1234567890"
	const gho = "gho_abcdefghijklmnopqrstuvwxyz1234567890"
	const pat = "github_pat_abcdefghijklmnopqrstuv"
	const ant = "sk-ant-api03-abcdefghijklmnopqrst"
	const glpat = "glpat-abcdefghijkl"
	const aiza = "AIza" + "01234567890123456789012345678901234"
	const slack = "xoxb-1234567890-abcdefghij"
	const akia = "AKIAIOSFODNN7EXAMPLE"
	const jwt = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signaturexx"

	tests := []struct {
		name    string
		in      string
		leaked  []string
		keep    []string
		wantHit bool
	}{
		{
			name:    "private key match",
			in:      "-----BEGIN RSA PRIVATE KEY-----\nabc123\n-----END RSA PRIVATE KEY-----",
			leaked:  []string{"abc123"},
			wantHit: true,
		},
		{
			name:    "private key near-miss",
			in:      "-----BEGIN PUBLIC KEY-----\nabc123\n-----END PUBLIC KEY-----",
			keep:    []string{"abc123"},
			wantHit: false,
		},
		{
			name:    "json sensitive key",
			in:      `{"apiKey":"hunter2secret"}`,
			leaked:  []string{"hunter2secret"},
			wantHit: true,
		},
		{
			name:    "json non-sensitive key",
			in:      `{"name":"hunter2secret"}`,
			keep:    []string{"hunter2secret"},
			wantHit: false,
		},
		{
			name:    "assign sensitive",
			in:      "AWS_SECRET_ACCESS_KEY=supersecretvalue",
			leaked:  []string{"supersecretvalue"},
			wantHit: true,
		},
		{
			name:    "assign non-sensitive",
			in:      "PATH=/usr/bin",
			keep:    []string{"/usr/bin"},
			wantHit: false,
		},
		{
			name:    "authorization header case-insensitive",
			in:      "AUTHORIZATION: Bearer " + ghp,
			leaked:  []string{ghp},
			wantHit: true,
		},
		{
			name:    "authorization near-miss not a header name",
			in:      "X-Request-Id: Bearer not-a-token-value",
			keep:    []string{"not-a-token-value"},
			wantHit: false,
		},
		{
			name:    "query token",
			in:      "https://example.test/x?token=" + glpat,
			leaked:  []string{glpat},
			wantHit: true,
		},
		{
			name:    "query near-miss",
			in:      "https://example.test/x?page=42",
			keep:    []string{"page=42"},
			wantHit: false,
		},
		{
			name:    "openai sk-proj",
			in:      "key=" + "sk-proj-abcdefghijklmnopqrstuvwxyz1234",
			leaked:  []string{"sk-proj-abcdefghijklmnopqrstuvwxyz1234"},
			wantHit: true,
		},
		{
			name:    "sk-ant",
			in:      ant,
			leaked:  []string{ant},
			wantHit: true,
		},
		{
			name:    "github_pat",
			in:      pat,
			leaked:  []string{pat},
			wantHit: true,
		},
		{
			name:    "ghp classic",
			in:      ghp,
			leaked:  []string{ghp},
			wantHit: true,
		},
		{
			name:    "gho oauth",
			in:      gho,
			leaked:  []string{gho},
			wantHit: true,
		},
		{
			name:    "ghp too short unchanged",
			in:      "ghp_shorttoken",
			keep:    []string{"ghp_shorttoken"},
			wantHit: false,
		},
		{
			name:    "unsupported prefix ghx",
			in:      "ghx_abcdefghijklmnopqrstuvwxyz1234567890",
			keep:    []string{"ghx_abcdefghijklmnopqrstuvwxyz1234567890"},
			wantHit: false,
		},
		{
			name:    "glpat",
			in:      glpat,
			leaked:  []string{glpat},
			wantHit: true,
		},
		{
			name:    "google aiza",
			in:      aiza,
			leaked:  []string{aiza},
			wantHit: true,
		},
		{
			name:    "slack xoxb",
			in:      slack,
			leaked:  []string{slack},
			wantHit: true,
		},
		{
			name:    "aws akia",
			in:      akia,
			leaked:  []string{akia},
			wantHit: true,
		},
		{
			name:    "jwt",
			in:      jwt,
			leaked:  []string{jwt},
			wantHit: true,
		},
		{
			name:    "secret header x-api-key",
			in:      "X-API-Key: hunter2secret",
			leaked:  []string{"hunter2secret"},
			wantHit: true,
		},
		{
			name:    "non-secret header unchanged",
			in:      "X-Request-Id: hunter2secret",
			keep:    []string{"hunter2secret"},
			wantHit: false,
		},
		{
			name:    "url password",
			in:      "https://user:hunter2secret@example.test/x",
			leaked:  []string{"hunter2secret"},
			wantHit: true,
		},
		{
			name:    "url user without password unchanged",
			in:      "https://user@example.test/x",
			keep:    []string{"https://user@example.test/x"},
			wantHit: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactString(tc.in, Options{})
			for _, leak := range tc.leaked {
				if strings.Contains(got, leak) {
					t.Fatalf("leaked %q in %q", leak, got)
				}
			}
			for _, keep := range tc.keep {
				if !strings.Contains(got, keep) {
					t.Fatalf("near-miss changed: want %q still in %q", keep, got)
				}
			}
			if tc.wantHit && !strings.Contains(got, RedactedSecret) {
				t.Fatalf("expected %q, got %q", RedactedSecret, got)
			}
			if !tc.wantHit && strings.Contains(got, RedactedSecret) {
				t.Fatalf("unexpected redaction: %q", got)
			}
		})
	}
}
