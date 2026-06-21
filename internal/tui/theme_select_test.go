package tui

import (
	"math"
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

// wcagRatio is the true WCAG 2.x contrast ratio (sRGB-linearized luminance), unlike
// relLum which is a cheap perceptual ordering. Used to assert AA (>=4.5) for the
// text-bearing theme tokens.
func wcagRatio(t *testing.T, fg, bg string) float64 {
	t.Helper()
	rel := func(hex string) float64 {
		h := strings.TrimPrefix(hex, "#")
		v, err := strconv.ParseUint(h, 16, 32)
		if err != nil || len(h) != 6 {
			t.Fatalf("bad hex %q", hex)
		}
		lin := func(c float64) float64 {
			c /= 255
			if c <= 0.03928 {
				return c / 12.92
			}
			return math.Pow((c+0.055)/1.055, 2.4)
		}
		return 0.2126*lin(float64((v>>16)&0xff)) + 0.7152*lin(float64((v>>8)&0xff)) + 0.0722*lin(float64(v&0xff))
	}
	l1, l2 := rel(fg), rel(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// The highlighted picker/autocomplete row must both stand out from the panel
// AND keep its label readable. Guards the regression this fixes: the light
// selBg (#e7f2cd) sat at 1.01 vs the panel (#ececed) — effectively invisible.
func TestSelectedRowBandIsVisibleAndReadable(t *testing.T) {
	for _, c := range []struct {
		name string
		pal  palette
	}{
		{"dark", darkPalette},
		{"light", lightPalette},
	} {
		if r := wcagRatio(t, c.pal.ink, c.pal.selBg); r < 4.5 {
			t.Errorf("%s: ink on selBg contrast %.2f < 4.5 — selected-row label unreadable", c.name, r)
		}
		if sep := wcagRatio(t, c.pal.selBg, c.pal.panel); sep < 1.10 {
			t.Errorf("%s: selBg vs panel separation %.2f < 1.10 — selected row does not stand out", c.name, sep)
		}
	}
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
	// AUDIT-H5/H6/M: text-bearing tokens (incl. faint/faintest, which carry line
	// numbers, diff @@/+++/---, help text, placeholders, and the accent prompt glyph)
	// must meet WCAG AA (>=4.5) against the worst-case background (the panel) — a real
	// contrast ratio, not just a luminance ordering.
	for _, tok := range []struct {
		name   string
		fg, bg string
	}{
		{"dark muted", darkPalette.muted, darkPalette.panel},
		{"dark faint", darkPalette.faint, darkPalette.panel},
		{"dark faintest", darkPalette.faintest, darkPalette.panel},
		{"dark accent", darkPalette.accent, darkPalette.panel},
		{"light muted", lightPalette.muted, lightPalette.panel},
		{"light faint", lightPalette.faint, lightPalette.panel},
		{"light faintest", lightPalette.faintest, lightPalette.panel},
		{"light accent", lightPalette.accent, lightPalette.panel},
	} {
		if r := wcagRatio(t, tok.fg, tok.bg); r < 4.5 {
			t.Errorf("%s contrast %.2f < 4.5 (WCAG AA): %s on %s", tok.name, r, tok.fg, tok.bg)
		}
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
