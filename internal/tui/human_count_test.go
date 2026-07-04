package tui

import "testing"

func TestHumanCount(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1K"},
		{12400, "12.4K"},
		{200000, "200K"},
		{999999, "1000K"},
		// The bug: a million-scale count must switch to an M suffix instead
		// of continuing to render as "1000K", "1200K", etc.
		{1_000_000, "1M"},
		{1_048_576, "1M"},
		{1_200_000, "1.2M"},
		{-5, "0"},
	}
	for _, c := range cases {
		if got := humanCount(c.n); got != c.want {
			t.Errorf("humanCount(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
