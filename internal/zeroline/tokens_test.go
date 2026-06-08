package zeroline

import "testing"

func TestHumanTokens(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{-1, "0"},    // negative clamps to zero
		{0, "0"},     //
		{999, "999"}, // under 1k stays exact
		{1000, "1k"}, // .0 trimmed
		{1234, "1.2k"},
		{1500, "1.5k"},
		{999999, "1000k"}, // rounds up but stays in k (documented behavior)
	}
	for _, c := range cases {
		if got := humanTokens(c.in); got != c.want {
			t.Errorf("humanTokens(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}
