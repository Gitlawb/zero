package redaction

import (
	"strings"
	"testing"
)

func TestSplitRedactionHarness(t *testing.T) {
	// Representative secrets for all supported shapes
	secrets := []struct {
		name   string
		secret string
	}{
		{"Anthropic", "sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234"},
		{"OpenAI standard", "sk-abcdefghijklmnopqrstuvwxyz12345678"},
		{"OpenAI with hyphen and digit", "sk-aaaaaaaaaa-bbbbbbbbb1234567890"},
		{"OpenAI proj", "sk-proj-abcdefghijklmnopqrstuvwxyz12345"},
		{"GitHub PAT", "github_pat_11AAAAAAA0123456789abcdefghijklmnopqrstuvwxyz"},
		{"GitHub Fine-Grained", "ghp_123456789012345678901234567890123456"},
		{"GitLab PAT", "glpat-12345678901234567890"},
		{"Google API", "AIzaSyD-1234567890123456789012345678901"},
		{"Slack bot", "xoxb-123456789012-abcdefghijklmno"},
		{"AWS AKIA", "AKIAIOSFODNN7EXAMPLE"},
		{"JWT", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"},
	}

	controls := []struct {
		name string
		char string
	}{
		{"NUL", "\x00"},
		{"ESC", "\x1b"},
		{"lone C1", "\x9b"},
		{"UTF-8 C1", "\u009b"},
	}

	for _, s := range secrets {
		t.Run(s.name, func(t *testing.T) {
			// First verify unsplit redacts
			gotUnsplit := RedactString(s.secret, Options{})
			if strings.Contains(gotUnsplit, s.secret) || !strings.Contains(gotUnsplit, RedactedSecret) {
				t.Fatalf("unsplit secret %q failed to redact: %q", s.secret, gotUnsplit)
			}

			// Test split at all interior positions throughout the secret
			for _, ctrl := range controls {
				for pos := 1; pos < len(s.secret); pos++ {
					splitSecret := s.secret[:pos] + ctrl.char + s.secret[pos:]
					got := RedactString(splitSecret, Options{})

					// Strip controls from output and assert original secret cannot be recovered
					strippedOutput := stripControlBytes(got)
					if strings.Contains(strippedOutput, s.secret) {
						t.Fatalf("split at pos %d with %s leaked secret!\n split input=%q\n got=%q\n stripped=%q", pos, ctrl.name, splitSecret, got, strippedOutput)
					}
					if !strings.Contains(got, RedactedSecret) {
						t.Fatalf("split at pos %d with %s did not contain RedactedSecret!\n got=%q", pos, ctrl.name, got)
					}
				}
			}
		})
	}
}

func TestSplitRedactionNegativeCases(t *testing.T) {
	controls := []string{"\x00", "\x1b", "\x9b", "\u009b"}

	t.Run("OpenAI kebab false positive with control", func(t *testing.T) {
		kebab := "sk-my-awesome-kebab-project"
		for _, ctrl := range controls {
			input := kebab[:10] + ctrl + kebab[10:]
			got := RedactString(input, Options{})
			if got != input {
				t.Fatalf("digit-free kebab falsely redacted with control %q: got %q, want %q", ctrl, got, input)
			}
		}
	})

	t.Run("Control immediately before complete credential", func(t *testing.T) {
		secret := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"
		for _, ctrl := range controls {
			input := "prefix" + ctrl + secret
			got := RedactString(input, Options{})
			want := "prefix" + ctrl + RedactedSecret
			if got != want {
				t.Fatalf("control before secret mutated boundary: got %q, want %q", got, want)
			}
		}
	})

	t.Run("Control immediately after complete credential", func(t *testing.T) {
		secret := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"
		for _, ctrl := range controls {
			input := secret + ctrl + "suffix"
			got := RedactString(input, Options{})
			want := RedactedSecret + ctrl + "suffix"
			if got != want {
				t.Fatalf("control after secret mutated delimiter: got %q, want %q", got, want)
			}
		}
	})
}
