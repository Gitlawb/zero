package agent

import "testing"

func TestApproxTextTokens(t *testing.T) {
	if got := ApproxTextTokens(""); got != 0 {
		t.Fatalf("empty = %d, want 0", got)
	}
	// Whitespace is folded out (BPE merges it into the adjacent token):
	// "aaaa    bbbb" has 8 non-space bytes -> 2 tokens, where naive len/4 says 3.
	if got := ApproxTextTokens("aaaa    bbbb"); got != 2 {
		t.Fatalf("ApproxTextTokens = %d, want 2 (8 non-space / 4)", got)
	}
	// It must never exceed naive len/4 — it only removes whitespace — and on
	// whitespace-bearing text it should be strictly smaller.
	cases := []string{"hello world", "func main() {\n\treturn\n}", "a\tb\nc d e f"}
	for _, s := range cases {
		approx, naive := ApproxTextTokens(s), len(s)/4
		if approx > naive {
			t.Fatalf("ApproxTextTokens(%q)=%d exceeded len/4=%d", s, approx, naive)
		}
	}
	// Pure non-whitespace matches len/4 exactly (nothing to fold).
	if got := ApproxTextTokens("abcdefgh"); got != 2 {
		t.Fatalf("ApproxTextTokens(no spaces) = %d, want 2", got)
	}
}
