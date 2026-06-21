package tui

import "testing"

func TestNoColorRequestedHonorsSpec(t *testing.T) {
	// AUDIT-M3: NO_COLOR present with ANY non-empty value must disable color
	// (no-color.org), not only strconv.ParseBool-true values.
	cases := map[string]bool{
		"1": true, "true": true, "yes": true, "foo": true, "0": true, " ": true,
		"": false,
	}
	for val, want := range cases {
		got := noColorRequested(func(k string) string {
			if k == "NO_COLOR" {
				return val
			}
			return ""
		})
		if got != want {
			t.Errorf("noColorRequested(NO_COLOR=%q) = %v, want %v", val, got, want)
		}
	}
	// Unset behaves like empty.
	if noColorRequested(func(string) string { return "" }) {
		t.Error("unset NO_COLOR must not disable color")
	}
}
