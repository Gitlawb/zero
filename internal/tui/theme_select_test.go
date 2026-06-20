package tui

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func relLum(t *testing.T, hex string) float64 {
	t.Helper()
	h := strings.TrimPrefix(hex, "#")
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil || len(h) != 6 {
		t.Fatalf("bad hex %q", hex)
	}
	r := float64((v>>16)&0xff) / 255
	g := float64((v>>8)&0xff) / 255
	b := float64(v&0xff) / 255
	return 0.2126*r + 0.7152*g + 0.0722*b
}

// resolveThemeMode precedence: explicit flag > ZERO_THEME env > auto.
func TestResolveThemeModePrecedence(t *testing.T) {
	cases := []struct {
		flag, env string
		want      themeMode
	}{
		{"light", "dark", themeLight}, // flag wins
		{"dark", "light", themeDark},  // flag wins
		{"", "light", themeLight},     // env
		{"", "dark", themeDark},       // env
		{"", "", themeAuto},           // default
		{"garbage", "also-bad", themeAuto},
		{"AUTO", "", themeAuto},
	}
	for _, c := range cases {
		if got := resolveThemeMode(c.flag, c.env); got != c.want {
			t.Errorf("resolveThemeMode(%q,%q) = %q, want %q", c.flag, c.env, got, c.want)
		}
	}
}

// applyTheme: auto resolves from background; explicit dark/light ignore it.
func TestApplyThemeResolution(t *testing.T) {
	defer applyTheme(themeDark, true) // restore the global default
	cases := []struct {
		mode    themeMode
		darkBg  bool
		want    themeMode
		wantInk string
	}{
		{themeAuto, true, themeDark, darkPalette.ink},
		{themeAuto, false, themeLight, lightPalette.ink},
		{themeDark, false, themeDark, darkPalette.ink},   // explicit ignores bg
		{themeLight, true, themeLight, lightPalette.ink}, // explicit ignores bg
	}
	for _, c := range cases {
		got := applyTheme(c.mode, c.darkBg)
		if got != c.want {
			t.Errorf("applyTheme(%q, darkBg=%v) = %q, want %q", c.mode, c.darkBg, got, c.want)
		}
		wantR, wantG, wantB, _ := lipgloss.Color(c.wantInk).RGBA()
		gotR, gotG, gotB, _ := zeroTheme.inkColor.RGBA()
		if gotR != wantR || gotG != wantG || gotB != wantB {
			t.Errorf("applyTheme(%q,%v): zeroTheme.inkColor not the %q ink", c.mode, c.darkBg, c.want)
		}
	}
}

// The light palette must be a real dark-on-light set: distinct from dark, ink
// well-contrasted against the panel, accent readable, and the gray hierarchy
// (ink→faintest) ordered toward the surface so it still reads on white.
func TestLightPaletteContrastAndHierarchy(t *testing.T) {
	if lightPalette.ink == darkPalette.ink || lightPalette.panel == darkPalette.panel {
		t.Fatal("light palette must differ from dark")
	}
	inkL, panelL := relLum(t, lightPalette.ink), relLum(t, lightPalette.panel)
	if panelL-inkL < 0.5 {
		t.Errorf("light ink/panel contrast too low: panel=%.2f ink=%.2f", panelL, inkL)
	}
	if panelL-relLum(t, lightPalette.accent) < 0.25 {
		t.Errorf("light accent not contrasted enough against panel")
	}
	// Dark-on-light: ink darkest, then progressively lighter toward the surface.
	chain := []float64{
		relLum(t, lightPalette.ink),
		relLum(t, lightPalette.muted),
		relLum(t, lightPalette.faint),
		relLum(t, lightPalette.faintest),
		relLum(t, lightPalette.panel),
	}
	for i := 1; i < len(chain); i++ {
		if !(chain[i] > chain[i-1]) {
			t.Errorf("light hierarchy not monotonic toward surface at %d: %v", i, chain)
		}
	}
	// Dark theme keeps the inverse ordering (light-on-dark).
	dchain := []float64{
		relLum(t, darkPalette.ink),
		relLum(t, darkPalette.muted),
		relLum(t, darkPalette.faint),
		relLum(t, darkPalette.faintest),
		relLum(t, darkPalette.panel),
	}
	for i := 1; i < len(dchain); i++ {
		if !(dchain[i] < dchain[i-1]) {
			t.Errorf("dark hierarchy not monotonic toward surface at %d: %v", i, dchain)
		}
	}
}

// /theme switches the active theme live and shows state with no arg.
func TestHandleThemeCommand(t *testing.T) {
	defer applyTheme(themeDark, true)
	m := model{themeMode: themeAuto, hasDarkBg: true}

	m, out := m.handleThemeCommand("light")
	if m.themeMode != themeLight {
		t.Fatalf("after /theme light, mode = %q", m.themeMode)
	}
	if r, _, _, _ := zeroTheme.inkColor.RGBA(); r != mustR(t, lightPalette.ink) {
		t.Error("/theme light did not swap the active palette")
	}
	if !strings.Contains(out, "light") {
		t.Errorf("output should confirm light: %q", out)
	}

	m, _ = m.handleThemeCommand("dark")
	if m.themeMode != themeDark {
		t.Fatalf("after /theme dark, mode = %q", m.themeMode)
	}

	_, state := m.handleThemeCommand("")
	if !strings.Contains(state, "active theme") {
		t.Errorf("no-arg /theme should show state: %q", state)
	}
	if _, bad := m.handleThemeCommand("solarized"); !strings.Contains(bad, "Unknown theme") {
		t.Errorf("invalid theme should error: %q", bad)
	}
}

func mustR(t *testing.T, hex string) uint32 {
	t.Helper()
	r, _, _, _ := lipgloss.Color(hex).RGBA()
	return r
}
