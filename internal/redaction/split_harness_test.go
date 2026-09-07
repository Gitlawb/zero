package redaction

import (
	"strconv"
	"strings"
	"testing"
)

// Construct deliberately synthetic tokens for shape matching without storing
// credential-shaped Slack literals that GitHub push protection rejects.
func syntheticSlackToken(digit, letter byte) string {
	return "xoxb-" + strings.Repeat(string(digit), 12) + "-" + strings.Repeat(string(letter), 15)
}

func TestSplitRedactionHarness(t *testing.T) {
	// Representative secrets for all supported shapes
	secrets := []struct {
		name   string
		secret string
	}{
		{"Anthropic", "sk-ant-api03-abcdefghijklmnopqrstuvwxyz1234"},               // gitleaks:allow -- synthetic redaction fixture
		{"OpenAI standard", "sk-abcdefghijklmnopqrstuvwxyz12345678"},               // gitleaks:allow -- synthetic redaction fixture
		{"OpenAI with hyphen and digit", "sk-aaaaaaaaaa-bbbbbbbbb1234567890"},      // gitleaks:allow -- synthetic redaction fixture
		{"OpenAI proj", "sk-proj-abcdefghijklmnopqrstuvwxyz12345"},                 // gitleaks:allow -- synthetic redaction fixture
		{"GitHub PAT", "github_pat_11AAAAAAA0123456789abcdefghijklmnopqrstuvwxyz"}, // gitleaks:allow -- synthetic redaction fixture
		{"GitHub Fine-Grained", "ghp_123456789012345678901234567890123456"},        // gitleaks:allow -- synthetic redaction fixture
		{"GitLab PAT", "glpat-12345678901234567890"},                               // gitleaks:allow -- synthetic redaction fixture
		{"Google API", "AIzaSyD-1234567890123456789012345678901"},                  // gitleaks:allow -- synthetic redaction fixture
		{"Slack bot", syntheticSlackToken('1', 'a')},
		{"AWS AKIA", "AKIAIOSFODNN7EXAMPLE"}, // gitleaks:allow -- synthetic redaction fixture
		{"JWT", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"}, // gitleaks:allow -- synthetic redaction fixture
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

					if got != RedactedSecret {
						t.Fatalf("split at pos %d with %s did not equal RedactedSecret: got %q, want %q", pos, ctrl.name, got, RedactedSecret)
					}
				}
			}
		})
	}
}

func TestSplitRedactionMultiControlCases(t *testing.T) {
	t.Run("Internal gap before minimum then terminal delimiter", func(t *testing.T) {
		input := "sk-ant-api03-\x00abcdefghijklmnopqrstuvwxyz\x00path/file.go" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x00path/file.go"
		if got != want {
			t.Fatalf("multi-control anthropic mismatch:\n got=%q\nwant=%q", got, want)
		}
	})

	t.Run("OpenAI internal gap then terminal delimiter before kebab suffix", func(t *testing.T) {
		input := "sk-\x00abcdefghijklmnopqrstuv\x1bkebab-case tail" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x1bkebab-case tail"
		if got != want {
			t.Fatalf("multi-control openai mismatch:\n got=%q\nwant=%q", got, want)
		}
	})

	t.Run("OpenAI internal gap before digit suffix then terminal delimiter", func(t *testing.T) {
		input := "sk-aaaaaaaaaa-bbbbbbbbb\x001234567890\x00path/one.go" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x00path/one.go"
		if got != want {
			t.Fatalf("multi-control openai with digits mismatch:\n got=%q\nwant=%q", got, want)
		}
	})

	t.Run("JWT multiple internal gaps and terminal delimiter", func(t *testing.T) {
		input := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9\x00.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ\x1b.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c\x00trailing/text" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x00trailing/text"
		if got != want {
			t.Fatalf("multi-control jwt mismatch:\n got=%q\nwant=%q", got, want)
		}
	})

	t.Run("Multiple internal controls in credential body", func(t *testing.T) {
		input := "sk-ant-\x00api03-\x1babcdefghijklmnopqrstuvwxyz" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		if got != RedactedSecret {
			t.Fatalf("multiple internal gaps in anthropic key mismatch: got %q, want %q", got, RedactedSecret)
		}
	})

	t.Run("Terminal delimiter separating two credentials", func(t *testing.T) {
		input := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz\x00ghp_123456789012345678901234567890123456" // gitleaks:allow -- synthetic redaction fixture
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
			{"OpenAI", "sk-aaaaaaaaaaaaaaaaaaaabcdefgh", "sk-bbbbbbbbbbbbbbbbbbbbcdefghi"},                                                         // gitleaks:allow -- synthetic redaction fixture
			{"GitHub Fine-Grained", "ghp_123456789012345678901234567890123456", "ghp_abcdefghijklmnopqrstuvwxyz1234567890"},                        // gitleaks:allow -- synthetic redaction fixture
			{"GitHub PAT", "github_pat_11AAAAAAA0123456789abcdefghijklmnopqrstuvwxyz", "github_pat_22BBBBBBB0123456789abcdefghijklmnopqrstuvwxyz"}, // gitleaks:allow -- synthetic redaction fixture
			{"GitLab PAT", "glpat-12345678901234567890", "glpat-abcdefghijklmnopqrst"},                                                             // gitleaks:allow -- synthetic redaction fixture
			{"Google API", "AIzaSyD-1234567890123456789012345678901", "AIzaSyD-abcdefghijklmnopqrstuvwxyz12345"},                                   // gitleaks:allow -- synthetic redaction fixture
			{"Slack", syntheticSlackToken('1', 'a'), syntheticSlackToken('2', 'b')},
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
		input := "sk-aaaaaaaaaaaaaaaaaaaabcdefgh\x00sk-bbbbbbbbbbbbbbbbbbbbcdefghi\x1bsk-ccccccccccccccccccccdefghij" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x00" + RedactedSecret + "\x1b" + RedactedSecret
		if got != want {
			t.Fatalf("three OpenAI keys mismatch: got %q, want %q", got, want)
		}
	})

	t.Run("Short sk- token before credential", func(t *testing.T) {
		input := "sk-ab\x00sk-aaaaaaaaaaaaaaaaaaaabcdefgh" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		want := "sk-ab\x00" + RedactedSecret // gitleaks:allow -- synthetic redaction fixture
		if got != want {
			t.Fatalf("short sk- token before credential mismatch: got %q, want %q", got, want)
		}
	})

	t.Run("OpenAI kebab false positive before path with digit", func(t *testing.T) {
		input := "sk-my-awesome-kebab-project\x00v2/file.go" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		want := "sk-my-awesome-kebab-project\x00v2/file.go" // gitleaks:allow -- synthetic redaction fixture
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
				input := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz" + tc.suffix // gitleaks:allow -- synthetic redaction fixture
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
		kebab := "sk-my-awesome-kebab-project" // gitleaks:allow -- synthetic redaction fixture
		for _, ctrl := range controls {
			input := kebab[:10] + ctrl + kebab[10:]
			got := RedactString(input, Options{})
			if got != input {
				t.Fatalf("digit-free kebab falsely redacted with control %q: got %q, want %q", ctrl, got, input)
			}
		}
	})

	t.Run("Control immediately before complete credential", func(t *testing.T) {
		secret := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz" // gitleaks:allow -- synthetic redaction fixture
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
		secret := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz" // gitleaks:allow -- synthetic redaction fixture
		for _, ctrl := range controls {
			input := secret + ctrl + "path/file"
			got := RedactString(input, Options{})
			want := RedactedSecret + ctrl + "path/file"
			if got != want {
				t.Fatalf("control after secret mutated delimiter: got %q, want %q", got, want)
			}
		}
	})

	t.Run("Kebab project before OpenAI key separated by control", func(t *testing.T) {
		// Finding 1: kebab before OpenAI key separated by control
		input := "sk-my-awesome-kebab-project\x00sk-abcdefghijklmnopqrstuv123456" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		want := "sk-my-awesome-kebab-project\x00" + RedactedSecret // gitleaks:allow -- synthetic redaction fixture
		if got != want {
			t.Fatalf("kebab before key mismatch: got %q, want %q", got, want)
		}
	})

	t.Run("Neighboring complete JWTs separated by control", func(t *testing.T) {
		// Finding 2: neighboring complete JWTs separated by control
		jwt1 := "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c" // gitleaks:allow -- synthetic redaction fixture
		jwt2 := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.4peTcaNQZNs4FcW3Usagee0"                                                                                    // gitleaks:allow -- synthetic redaction fixture
		for _, ctrl := range controls {
			input := jwt1 + ctrl + jwt2
			got := RedactString(input, Options{})
			want := RedactedSecret + ctrl + RedactedSecret
			if got != want {
				t.Fatalf("neighboring JWTs with %q mismatch: got %q, want %q", ctrl, got, want)
			}
		}
	})

	t.Run("Split key before minimum followed by slash in path", func(t *testing.T) {
		// Finding 4: split key before minimum followed by slash
		input := "ghp_1234567890\x0012345678901234567890123456/file" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		want := RedactedSecret + "/file"
		if got != want {
			t.Fatalf("split before min then slash mismatch: got %q, want %q", got, want)
		}
	})

	t.Run("Split key with incidental incomplete prefix", func(t *testing.T) {
		// Finding 5: split key with incidental incomplete prefix glpat-1234\x00AKIA56789012
		input := "glpat-1234\x00AKIA56789012" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		if got != input {
			t.Fatalf("incomplete glpat with control and incomplete AKIA falsely redacted: got %q, want %q", got, input)
		}
	})

	t.Run("Enclosing OpenAI key containing inner ghp suffix", func(t *testing.T) {
		// Finding 6: enclosing OpenAI key containing inner ghp_
		input := "sk-abcdefghijklmnop-ghp_123456789012345678901234567890123456" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		if got != RedactedSecret {
			t.Fatalf("enclosing key mismatch: got %q, want %q", got, RedactedSecret)
		}
	})

	t.Run("Retain left context across boundaries", func(t *testing.T) {
		// Finding 7: adjacent AKIA keys
		input := "AKIAIOSFODNN7EXAMPLEAKIAIOSFODNN7EXAMPLE" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		want := RedactedSecret + RedactedSecret
		if got != want {
			t.Fatalf("adjacent AKIA keys mismatch: got %q, want %q", got, want)
		}
	})

	t.Run("Preserve entire terminal control run across encodings", func(t *testing.T) {
		// Finding 8: terminal control run \x00\x1b[path/file
		input := "sk-ant-api03-abcdefghijklmnopqrstuvwxyz\x00\x1b[path/file" // gitleaks:allow -- synthetic redaction fixture
		got := RedactString(input, Options{})
		want := RedactedSecret + "\x00\x1b[path/file"
		if got != want {
			t.Fatalf("terminal control run mismatch: got %q, want %q", got, want)
		}
	})
}

func TestIncompletePrefixInsideValidOpenAIKey(t *testing.T) {
	first := "sk-" + strings.Repeat("a", 24) + "123456"
	for _, tail := range []string{"sk-proj-abcdefg", "sk-ant-abcdefgh", "github_pat_abcd", "glpat-abcdefghi", "AIzaabcdefghijk"} {
		for _, gap := range []string{"\x00", "\x1b", "\x9b", "\u009b", "\x00\x1b\u009b"} {
			if got := RedactString(first+gap+tail, Options{}); got != RedactedSecret {
				t.Errorf("incomplete prefix %q after gap %q leaked: %q", tail, gap, got)
			}
		}
	}
	// A complete neighbor remains independent, with the original gap intact.
	second := "sk-proj-" + strings.Repeat("b", 24)
	if got, want := RedactString(first+"\x00"+second, Options{}), RedactedSecret+"\x00"+RedactedSecret; got != want {
		t.Errorf("complete neighboring key: got %q, want %q", got, want)
	}
	// An incomplete prefix still separates non-secret prose from a split key.
	prose := "sk-my-awesome-kebab-project"
	if got, want := RedactString(prose+"\x00sk-proj-abcdefg\x00hijklmnop12345", Options{}), prose+"\x00"+RedactedSecret; got != want {
		t.Errorf("prose before split key: got %q, want %q", got, want)
	}
}

func TestNeighboringJWTLengths(t *testing.T) {
	header := "eyJhbGciOiJIUzI1NiJ9"                                                                                     // gitleaks:allow -- synthetic redaction fixture
	payloads := []string{"eyJzdWIiOiIxMjM0NTY3ODkwIn0", "eyJzdWIiOiJib2JieSIsIm5hbWUiOiJCIn0", strings.Repeat("a", 256)} // gitleaks:allow -- synthetic redaction fixture
	for _, firstPayload := range payloads {
		for _, secondPayload := range payloads {
			a := header + "." + firstPayload + ".SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
			b := header + "." + secondPayload + ".Qm9iYnlTaWduYXR1cmVCQkJCQkJCQkJCQg"
			for _, gap := range []string{"\x00", "\x1b", "\x9b", "\u009b", "\x00\x1b\u009b"} {
				if got, want := RedactString(a+gap+b, Options{}), RedactedSecret+gap+RedactedSecret; got != want {
					t.Errorf("payload lengths %d/%d, gap %q: got %q, want %q", len(firstPayload), len(secondPayload), gap, got, want)
				}
			}
		}
	}
}

func TestSplitRedactionNoCredentialSuffixRemains(t *testing.T) {
	// Regression test for splits before and after minimum length:
	// ensure no credential suffix is leaked in either case.
	key := "sk-abcdefghijklmnopqrstuvwxyz12345678" // minOpenAILen = 23, total = 37 // gitleaks:allow -- synthetic redaction fixture
	splitBeforeMin := key[:10] + "\x00" + key[10:] // pos = 10 (< 23)
	splitAfterMin := key[:28] + "\x00" + key[28:]  // pos = 28 (> 23)

	for _, tc := range []struct {
		name  string
		input string
	}{
		{"split before minimum length", splitBeforeMin},
		{"split after minimum length", splitAfterMin},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactString(tc.input, Options{})
			if got != RedactedSecret {
				t.Fatalf("%s leaked: got %q, want %q", tc.name, got, RedactedSecret)
			}
			if strings.Contains(got, key[28:]) {
				t.Fatalf("%s leaked suffix %q in %q", tc.name, key[28:], got)
			}
		})
	}
}

func TestSplitRedactionLargeInputs(t *testing.T) {
	sizes := []int{8 * 1024, 16 * 1024, 32 * 1024, 64 * 1024, 128 * 1024}

	t.Run("OpenAI kebab repeated gaps scaling", func(t *testing.T) {
		for _, size := range sizes {
			var b strings.Builder
			b.WriteString("sk-kebab-") // gitleaks:allow -- synthetic redaction fixture
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

	t.Run("OpenAI kebab starting with bare sk- and repeated gaps scaling", func(t *testing.T) {
		for _, size := range sizes {
			var b strings.Builder
			b.WriteString("sk-") // gitleaks:allow -- synthetic redaction fixture
			for b.Len() < size {
				b.WriteString("\x00a-b")
			}
			input := b.String()
			got := RedactString(input, Options{})
			if got != input {
				t.Fatalf("kebab false positive with bare sk- prefix was falsely redacted at size %d", size)
			}
		}
	})

	t.Run("JWT repeated gaps scaling and correct redaction", func(t *testing.T) {
		for _, size := range sizes {
			segLen := size / 2
			var b strings.Builder
			b.WriteString("eyJ") // gitleaks:allow -- synthetic redaction fixture
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

	t.Run("Anthropic repeated gaps scaling and correct redaction", func(t *testing.T) {
		for _, size := range sizes {
			var b strings.Builder
			b.WriteString("sk-ant-api03-abcdefghijklmnopqrstuvwxyz") // gitleaks:allow -- synthetic redaction fixture
			for b.Len() < size {
				b.WriteString("\x00a")
			}
			input := b.String()
			got := RedactString(input, Options{})
			if got != RedactedSecret {
				t.Fatalf("Anthropic at size %d failed to redact: got %q, want %q", size, got, RedactedSecret)
			}
		}
	})
}

func BenchmarkRedactJWTGaps800KB(b *testing.B) {
	var builder strings.Builder
	builder.WriteString("eyJ") // gitleaks:allow -- synthetic redaction fixture
	for builder.Len() < 400*1024 {
		builder.WriteString("\x00a")
	}
	builder.WriteString(".eyJ")
	for builder.Len() < 800*1024 {
		builder.WriteString("\x00b")
	}
	builder.WriteString(".SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c")
	input := builder.String()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = RedactString(input, Options{})
	}
}

func BenchmarkRedactOpenAIKebabGaps128KB(b *testing.B) {
	var builder strings.Builder
	builder.WriteString("sk-kebab-") // gitleaks:allow -- synthetic redaction fixture
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
	builder.WriteString("eyJ") // gitleaks:allow -- synthetic redaction fixture
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

// Keep runtime measurements out of correctness tests: race instrumentation and
// shared CI runners make absolute wall-clock deadlines unreliable.
func BenchmarkSplitRedactionScaling(b *testing.B) {
	for _, size := range []int{8 << 10, 32 << 10, 128 << 10} {
		inputs := map[string]string{
			"anthropic":       "sk-ant-api03-" + strings.Repeat("a", 20) + strings.Repeat("\x00a", size/2),
			"neighboringJWTs": strings.Repeat("eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJib2JieSIsIm5hbWUiOiJCIn0.Qm9iYnlTaWduYXR1cmVCQkJCQkJCQkJCQg\x00", size/90), // gitleaks:allow -- synthetic redaction fixture
		}
		for name, input := range inputs {
			b.Run(name+"/"+strconv.Itoa(size), func(b *testing.B) {
				b.SetBytes(int64(len(input)))
				b.ReportAllocs()
				for b.Loop() {
					_ = RedactString(input, Options{})
				}
			})
		}
	}
}
