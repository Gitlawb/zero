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

func TestSplitRedactionMultiControlCases(t *testing.T) {
	t.Run("Internal gap before minimum then terminal delimiter", func(t *testing.T) {
		input := "sk-ant-api03-\x00abcdefghijklmnopqrstuvwxyz\x00path/file.go"
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x00path/file.go"
		if got != want {
			t.Fatalf("multi-control anthropic mismatch:\n got=%q\nwant=%q", got, want)
		}
	})

	t.Run("OpenAI internal gap then terminal delimiter before kebab suffix", func(t *testing.T) {
		input := "sk-\x00abcdefghijklmnopqrstuv\x1bkebab-case tail"
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x1bkebab-case tail"
		if got != want {
			t.Fatalf("multi-control openai mismatch:\n got=%q\nwant=%q", got, want)
		}
	})

	t.Run("OpenAI internal gap before digit suffix then terminal delimiter", func(t *testing.T) {
		input := "sk-aaaaaaaaaa-bbbbbbbbb\x001234567890\x00path/one.go"
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x00path/one.go"
		if got != want {
			t.Fatalf("multi-control openai with digits mismatch:\n got=%q\nwant=%q", got, want)
		}
	})

	t.Run("JWT multiple internal gaps and terminal delimiter", func(t *testing.T) {
		input := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9\x00.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ\x1b.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c\x00trailing/text"
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x00trailing/text"
		if got != want {
			t.Fatalf("multi-control jwt mismatch:\n got=%q\nwant=%q", got, want)
		}
	})

	t.Run("Multiple internal controls in credential body", func(t *testing.T) {
		input := "sk-ant-\x00api03-\x1babcdefghijklmnopqrstuvwxyz"
		got := RedactString(input, Options{})
		if got != RedactedSecret {
			t.Fatalf("multiple internal gaps in anthropic key mismatch: got %q, want %q", got, RedactedSecret)
		}
	})

	t.Run("Terminal delimiter separating two credentials", func(t *testing.T) {
		input := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz\x00ghp_123456789012345678901234567890123456"
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x00" + RedactedSecret
		if got != want {
			t.Fatalf("two credentials separated by delimiter mismatch: got %q, want %q", got, want)
		}
	})

	t.Run("Two same-shape keys separated by control bytes", func(t *testing.T) {
		keyPairs := []struct {
			name string
			key1 string
			key2 string
		}{
			{"OpenAI", "sk-aaaaaaaaaaaaaaaaaaaabcdefgh", "sk-bbbbbbbbbbbbbbbbbbbbcdefghi"},
			{"GitHub Fine-Grained", "ghp_123456789012345678901234567890123456", "ghp_abcdefghijklmnopqrstuvwxyz1234567890"},
			{"GitHub PAT", "github_pat_11AAAAAAA0123456789abcdefghijklmnopqrstuvwxyz", "github_pat_22BBBBBBB0123456789abcdefghijklmnopqrstuvwxyz"},
			{"GitLab PAT", "glpat-12345678901234567890", "glpat-abcdefghijklmnopqrst"},
			{"Google API", "AIzaSyD-1234567890123456789012345678901", "AIzaSyD-abcdefghijklmnopqrstuvwxyz12345"},
			{"Slack", "xox" + "b-123456789012-abcdefghijklmno", "xox" + "b-987654321098-zyxwvutsrqponml"},
		}
		ctrls := []string{"\x00", "\x1b", "\x9b", "\u009b"}
		for _, pair := range keyPairs {
			for _, ctrl := range ctrls {
				input := pair.key1 + ctrl + pair.key2
				got := RedactString(input, Options{})
				want := RedactedSecret + ctrl + RedactedSecret
				if got != want {
					t.Fatalf("two %s keys separated by %q mismatch: got %q, want %q", pair.name, ctrl, got, want)
				}
			}
		}
	})

	t.Run("Three same-shape keys separated by control bytes", func(t *testing.T) {
		input := "sk-aaaaaaaaaaaaaaaaaaaabcdefgh\x00sk-bbbbbbbbbbbbbbbbbbbbcdefghi\x1bsk-ccccccccccccccccccccdefghij"
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x00" + RedactedSecret + "\x1b" + RedactedSecret
		if got != want {
			t.Fatalf("three OpenAI keys mismatch: got %q, want %q", got, want)
		}
	})

	t.Run("Short sk- token before credential", func(t *testing.T) {
		input := "sk-ab\x00sk-aaaaaaaaaaaaaaaaaaaabcdefgh"
		got := RedactString(input, Options{})
		want := "sk-ab\x00" + RedactedSecret
		if got != want {
			t.Fatalf("short sk- token before credential mismatch: got %q, want %q", got, want)
		}
	})

	t.Run("OpenAI kebab false positive before path with digit", func(t *testing.T) {
		input := "sk-my-awesome-kebab-project\x00v2/file.go"
		got := RedactString(input, Options{})
		want := "sk-my-awesome-kebab-project\x00v2/file.go"
		if got != want {
			t.Fatalf("kebab project before path with digit mismatch: got %q, want %q", got, want)
		}
	})

	t.Run("Complete credential followed by invalid bytes", func(t *testing.T) {
		cases := []struct {
			name   string
			suffix string
		}{
			{"valid U+FFFD", "\uFFFDsuffix"},
			{"malformed byte 0xFF", "\xffsuffix"},
			{"malformed byte 0xC0", "\xc0suffix"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				input := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz" + tc.suffix
				got := RedactString(input, Options{})
				want := RedactedSecret + tc.suffix
				if got != want {
					t.Fatalf("suffix %s mismatch: got %q, want %q", tc.name, got, want)
				}
			})
		}
	})
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

func TestSplitRedactionLinearScaling(t *testing.T) {
	sizes := []int{8 * 1024, 16 * 1024, 32 * 1024, 64 * 1024, 128 * 1024, 800 * 1024}

	t.Run("OpenAI kebab repeated gaps scaling", func(t *testing.T) {
		for _, size := range sizes {
			// Construct "sk-kebab-" + repeated "\x00a" up to size
			var b strings.Builder
			b.WriteString("sk-kebab-")
			for b.Len() < size {
				b.WriteString("\x00a")
			}
			input := b.String()
			got := RedactString(input, Options{})
			if got != input {
				t.Fatalf("kebab false positive was falsely redacted at size %d", size)
			}
		}
	})

	t.Run("JWT repeated gaps scaling and correct redaction", func(t *testing.T) {
		for _, size := range sizes {
			// Construct "eyJ" + repeated "\x00a" + ".eyJ" + repeated "\x00b" + ".SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
			segLen := size / 2
			var b strings.Builder
			b.WriteString("eyJ")
			for b.Len() < segLen {
				b.WriteString("\x00a")
			}
			b.WriteString(".eyJ")
			for b.Len() < size {
				b.WriteString("\x00b")
			}
			b.WriteString(".SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c")
			input := b.String()
			got := RedactString(input, Options{})
			if !strings.Contains(got, RedactedSecret) {
				t.Fatalf("JWT at size %d failed to redact", size)
			}
		}
	})
}

func BenchmarkRedactOpenAIKebabGaps128KB(b *testing.B) {
	var builder strings.Builder
	builder.WriteString("sk-kebab-")
	for builder.Len() < 128*1024 {
		builder.WriteString("\x00a")
	}
	input := builder.String()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RedactString(input, Options{})
	}
}

func BenchmarkRedactJWTGaps128KB(b *testing.B) {
	var builder strings.Builder
	builder.WriteString("eyJ")
	for builder.Len() < 64*1024 {
		builder.WriteString("\x00a")
	}
	builder.WriteString(".eyJ")
	for builder.Len() < 128*1024 {
		builder.WriteString("\x00b")
	}
	builder.WriteString(".SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c")
	input := builder.String()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RedactString(input, Options{})
	}
}
