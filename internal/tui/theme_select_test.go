package tui

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
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

// The word-level diff's brighter changed-span band must keep its text AA-readable
// and stay clearly distinct from the base add/del band, on both themes.
func TestDiffWordSpanContrast(t *testing.T) {
	for _, entry := range themeRegistry {
		name, pal := entry.Name, entry.Palette
		if r := wcagRatio(t, pal.addInk, pal.addBgWord); r < 4.5 {
			t.Errorf("%s: addInk on addBgWord %.2f < 4.5 (AA)", name, r)
		}
		if r := wcagRatio(t, pal.delInk, pal.delBgWord); r < 4.5 {
			t.Errorf("%s: delInk on delBgWord %.2f < 4.5 (AA)", name, r)
		}
		if sep := wcagRatio(t, pal.addBgWord, pal.addBg); sep < 1.2 {
			t.Errorf("%s: addBgWord vs addBg separation %.2f < 1.2 (span not distinct)", name, sep)
		}
		if sep := wcagRatio(t, pal.delBgWord, pal.delBg); sep < 1.2 {
			t.Errorf("%s: delBgWord vs delBg separation %.2f < 1.2 (span not distinct)", name, sep)
		}
	}
}

// The highlighted picker/autocomplete row must both stand out from the panel
// AND keep its label readable. Guards the regression this fixes: the light
// selBg (#e7f2cd) sat at 1.01 vs the panel (#ececed) — effectively invisible.
func TestSelectedRowBandIsVisibleAndReadable(t *testing.T) {
	for _, entry := range themeRegistry {
		name, pal := entry.Name, entry.Palette
		if r := wcagRatio(t, pal.ink, pal.selBg); r < 4.5 {
			t.Errorf("%s: ink on selBg contrast %.2f < 4.5 — selected-row label unreadable", name, r)
		}
		if sep := wcagRatio(t, pal.selBg, pal.panel); sep < 1.10 {
			t.Errorf("%s: selBg vs panel separation %.2f < 1.10 — selected row does not stand out", name, sep)
		}
	}
}

// Every registered theme — not just the two built-ins — must clear WCAG AA on its
// text-bearing tokens against the panel and keep the muted>faint>faintest>panel
// gray ramp monotonic in its polarity (light-on-dark for dark themes, the inverse
// for light themes). Guards that a newly-added color palette can't ship illegible.
func TestAllThemesContrastAndHierarchy(t *testing.T) {
	for _, entry := range themeRegistry {
		pal := entry.Palette
		for _, tok := range []struct {
			name string
			fg   string
		}{
			{"ink", pal.ink}, {"muted", pal.muted}, {"faint", pal.faint},
			{"faintest", pal.faintest}, {"accent", pal.accent},
		} {
			if r := wcagRatio(t, tok.fg, pal.panel); r < 4.5 {
				t.Errorf("%s %s on panel %.2f < 4.5 (WCAG AA)", entry.Name, tok.name, r)
			}
		}
		if r := wcagRatio(t, pal.onAccent, pal.accent); r < 4.5 {
			t.Errorf("%s onAccent on accent %.2f < 4.5 (WCAG AA)", entry.Name, r)
		}
		// Gray ramp ordered ink -> muted -> faint -> faintest -> panel; luminance
		// rises toward the surface on light themes, falls on dark themes.
		chain := []float64{
			relLum(t, pal.ink), relLum(t, pal.muted), relLum(t, pal.faint),
			relLum(t, pal.faintest), relLum(t, pal.panel),
		}
		for i := 1; i < len(chain); i++ {
			ok := chain[i] > chain[i-1]
			if entry.IsDark {
				ok = chain[i] < chain[i-1]
			}
			if !ok {
				t.Errorf("%s hierarchy not monotonic toward surface at %d: %v", entry.Name, i, chain)
			}
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

func TestNewThemePresetsWired(t *testing.T) {
	neon, ok := lookupTheme("neon")
	if !ok {
		t.Fatal("theme 'neon' is not registered")
	}
	if !neon.IsDark {
		t.Error("theme 'neon' should be marked as dark")
	}

	dune, ok := lookupTheme("dune")
	if !ok {
		t.Fatal("theme 'dune' is not registered")
	}
	if dune.IsDark {
		t.Error("theme 'dune' should be marked as light")
	}

	duneDark, ok := lookupTheme("dune-dark")
	if !ok {
		t.Fatal("theme 'dune-dark' is not registered")
	}
	if !duneDark.IsDark {
		t.Error("theme 'dune-dark' should be marked as dark")
	}

	for _, name := range []string{"neon", "dune", "dune-dark"} {
		if !validThemeMode(name) {
			t.Errorf("%q should be a valid --theme/ZERO_THEME value", name)
		}
	}

	if !contains(themeModes, "neon") || !contains(themeModes, "dune") || !contains(themeModes, "dune-dark") {
		t.Errorf("themeModes = %v, want it to include neon, dune, and dune-dark (the /theme picker list)", themeModes)
	}
}

// The --theme flag and ZERO_THEME both resolve through resolveThemeMode, and
// applyTheme must actually swap zeroTheme to the resolved preset's own palette,
// not silently fall back to a built-in.
func TestNewThemePresetsResolveThroughCLIAndEnvPath(t *testing.T) {
	defer applyTheme(themeDark, true)

	if got := resolveThemeMode("dune", ""); got != themeMode("dune") {
		t.Fatalf(`resolveThemeMode("dune", "") = %q, want "dune"`, got)
	}
	if got := resolveThemeMode("", "neon"); got != themeMode("neon") {
		t.Fatalf(`resolveThemeMode("", "neon") = %q, want "neon"`, got)
	}

	applyTheme(themeMode("dune"), true)
	if r, _, _, _ := zeroTheme.inkColor.RGBA(); r != mustR(t, dunePalette.ink) {
		t.Error("applying \"dune\" did not swap zeroTheme to the dune palette")
	}

	applyTheme(themeMode("neon"), true)
	if r, _, _, _ := zeroTheme.inkColor.RGBA(); r != mustR(t, neonPalette.ink) {
		t.Error("applying \"neon\" did not swap zeroTheme to the neon palette")
	}
}

func TestExtendedThemeContrastInvariants(t *testing.T) {
	// Skip validation for old built-in themes if they have established, non-compliant palettes,
	// but enforce strict compliance on the newly introduced 'neon', 'dune', and 'dune-dark' themes.
	for _, entry := range themeRegistry {
		if entry.Name != "neon" && entry.Name != "dune" && entry.Name != "dune-dark" {
			continue
		}
		name, pal := entry.Name, entry.Palette

		// Finding 1: Permission and status/success surfaces
		if r := wcagRatio(t, pal.amber, pal.permBg); r < 4.5 {
			t.Errorf("%s: amber on permBg contrast %.2f < 4.5", name, r)
		}
		if r := wcagRatio(t, pal.onAccent, pal.amber); r < 4.5 {
			t.Errorf("%s: onAccent on amber contrast %.2f < 4.5", name, r)
		}
		if r := wcagRatio(t, pal.green, pal.panel); r < 4.5 {
			t.Errorf("%s: green on panel contrast %.2f < 4.5", name, r)
		}
		if r := wcagRatio(t, pal.amber, pal.panel); r < 4.5 {
			t.Errorf("%s: amber on panel contrast %.2f < 4.5", name, r)
		}
		if r := wcagRatio(t, pal.red, pal.panel); r < 4.5 {
			t.Errorf("%s: red on panel contrast %.2f < 4.5", name, r)
		}

		// Finding 2: Selected row secondary text
		if r := wcagRatio(t, pal.faint, pal.selBg); r < 4.5 {
			t.Errorf("%s: faint on selBg contrast %.2f < 4.5", name, r)
		}
		if r := wcagRatio(t, pal.faintest, pal.selBg); r < 4.5 {
			t.Errorf("%s: faintest on selBg contrast %.2f < 4.5", name, r)
		}

		// Finding 3: Diff gutter pairings
		if r := wcagRatio(t, pal.faintest, pal.addBg); r < 4.5 {
			t.Errorf("%s: faintest on addBg contrast %.2f < 4.5", name, r)
		}
		if r := wcagRatio(t, pal.faintest, pal.delBg); r < 4.5 {
			t.Errorf("%s: faintest on delBg contrast %.2f < 4.5", name, r)
		}
		if r := wcagRatio(t, pal.green, pal.addBg); r < 4.5 {
			t.Errorf("%s: green on addBg contrast %.2f < 4.5", name, r)
		}
		if r := wcagRatio(t, pal.red, pal.delBg); r < 4.5 {
			t.Errorf("%s: red on delBg contrast %.2f < 4.5", name, r)
		}
	}
}

// hexChannels splits a #rrggbb token into its 8-bit channels.
func hexChannels(t *testing.T, hexColor string) (int, int, int) {
	t.Helper()
	h := strings.TrimPrefix(hexColor, "#")
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil || len(h) != 6 {
		t.Fatalf("bad hex %q", hexColor)
	}
	return int((v >> 16) & 0xff), int((v >> 8) & 0xff), int(v & 0xff)
}

// xterm256Hex returns the nearest xterm-256 color (the 6x6x6 cube plus the
// 24-step grayscale ramp, by squared RGB distance): how a terminal without
// truecolor support downsamples the palette's hex tokens before rendering.
func xterm256Hex(t *testing.T, hexColor string) string {
	t.Helper()
	r, g, b := hexChannels(t, hexColor)
	levels := []int{0, 95, 135, 175, 215, 255}
	bestR, bestG, bestB := 0, 0, 0
	bestDistance := math.MaxFloat64
	try := func(cr, cg, cb int) {
		d := float64((r-cr)*(r-cr) + (g-cg)*(g-cg) + (b-cb)*(b-cb))
		if d < bestDistance {
			bestDistance, bestR, bestG, bestB = d, cr, cg, cb
		}
	}
	for _, cr := range levels {
		for _, cg := range levels {
			for _, cb := range levels {
				try(cr, cg, cb)
			}
		}
	}
	for i := 0; i < 24; i++ {
		gray := 8 + 10*i
		try(gray, gray, gray)
	}
	return fmt.Sprintf("#%02x%02x%02x", bestR, bestG, bestB)
}

// Hex-level AA does not guarantee the rendered pairs hold on a 256-color
// terminal, which quantizes every token to its nearest xterm entry first.
// Guard the pairs that regressed: Dune's selected-row affordances (accent
// caret/favorite star and blue local-model dot over selBg via onSel) and
// diff bands (whose previous addBg/delBg and addBgWord/delBgWord values all
// quantized to the same grays), plus the rendered add-diff content itself
// (gutter and changed-word text, which quantization made unreadable even
// once the bands were distinct), and Neon's diff bands with the same issues.
func TestExtendedThemeANSI256Contrast(t *testing.T) {
	palettes := map[string]palette{}
	for _, entry := range themeRegistry {
		palettes[entry.Name] = entry.Palette
	}
	q := func(hexColor string) string { return xterm256Hex(t, hexColor) }

	greenish := func(hexColor string) bool {
		r, g, b := hexChannels(t, hexColor)
		return g > r && g > b
	}
	reddish := func(hexColor string) bool {
		r, g, b := hexChannels(t, hexColor)
		return r > g && r > b
	}

	for _, themeName := range []string{"dune", "dune-dark"} {
		pal := palettes[themeName]
		if sep := wcagRatio(t, q(pal.selBg), q(pal.panel)); sep < 1.10 {
			t.Errorf("%s: selBg vs panel separation %.2f < 1.10 after xterm-256 quantization (%s vs %s)", themeName, sep, q(pal.selBg), q(pal.panel))
		}
		for _, pair := range []struct{ name, fg, bg string }{
			{"accent on selBg", pal.accent, pal.selBg},
			{"blue on selBg", pal.blue, pal.selBg},
			{"faintest on selBg", pal.faintest, pal.selBg},
			{"ink on selBg", pal.ink, pal.selBg},
		} {
			if r := wcagRatio(t, q(pair.fg), q(pair.bg)); r < 4.5 {
				t.Errorf("%s: %s = %.2f < 4.5 after xterm-256 quantization (%s on %s)", themeName, pair.name, r, q(pair.fg), q(pair.bg))
			}
		}
		if q(pal.addBg) == q(pal.delBg) || !greenish(q(pal.addBg)) || !reddish(q(pal.delBg)) {
			t.Errorf("%s: add/del row bands lose their green/red identity after quantization: addBg %s -> %s, delBg %s -> %s",
				themeName, pal.addBg, q(pal.addBg), pal.delBg, q(pal.delBg))
		}
		if q(pal.addBgWord) == q(pal.delBgWord) || !greenish(q(pal.addBgWord)) || !reddish(q(pal.delBgWord)) {
			t.Errorf("%s: word-span bands lose their green/red identity after quantization: addBgWord %s -> %s, delBgWord %s -> %s",
				themeName, pal.addBgWord, q(pal.addBgWord), pal.delBgWord, q(pal.delBgWord))
		}
		if q(pal.addBgWord) == q(pal.addBg) {
			t.Errorf("%s: changed span is indistinguishable from its add row after quantization (both %s)", themeName, q(pal.addBg))
		}
		if q(pal.delBgWord) == q(pal.delBg) {
			t.Errorf("%s: changed span is indistinguishable from its del row after quantization (both %s)", themeName, q(pal.delBg))
		}
		if r := wcagRatio(t, q(pal.green), q(pal.addBg)); r < 4.5 {
			t.Errorf("%s: green on addBg = %.2f < 4.5 after quantization", themeName, r)
		}
		if r := wcagRatio(t, q(pal.red), q(pal.delBg)); r < 4.5 {
			t.Errorf("%s: red on delBg = %.2f < 4.5 after quantization", themeName, r)
		}
		for _, pair := range []struct{ name, fg, bg string }{
			{"faintest on addBg", pal.faintest, pal.addBg},
			{"faintest on delBg", pal.faintest, pal.delBg},
			{"addInk on addBgWord", pal.addInk, pal.addBgWord},
			{"delInk on delBgWord", pal.delInk, pal.delBgWord},
		} {
			if r := wcagRatio(t, q(pair.fg), q(pair.bg)); r < 4.5 {
				t.Errorf("%s: %s = %.2f < 4.5 after quantization (%s on %s)", themeName, pair.name, r, q(pair.fg), q(pair.bg))
			}
		}
	}

	neon := palettes["neon"]
	if q(neon.addBg) == q(neon.delBg) || !greenish(q(neon.addBg)) || !reddish(q(neon.delBg)) {
		t.Errorf("neon: add/del row bands lose their green/red identity after quantization: addBg %s -> %s, delBg %s -> %s",
			neon.addBg, q(neon.addBg), neon.delBg, q(neon.delBg))
	}
	if q(neon.addBgWord) == q(neon.delBgWord) || !greenish(q(neon.addBgWord)) || !reddish(q(neon.delBgWord)) {
		t.Errorf("neon: word-span bands lose their green/red identity after quantization: addBgWord %s -> %s, delBgWord %s -> %s",
			neon.addBgWord, q(neon.addBgWord), neon.delBgWord, q(neon.delBgWord))
	}
	if q(neon.addBgWord) == q(neon.addBg) {
		t.Errorf("neon: changed span is indistinguishable from its add row after quantization (both %s)", q(neon.addBg))
	}
	if q(neon.delBgWord) == q(neon.delBg) {
		t.Errorf("neon: changed span is indistinguishable from its del row after quantization (both %s)", q(neon.delBg))
	}
	if r := wcagRatio(t, q(neon.green), q(neon.addBg)); r < 4.5 {
		t.Errorf("neon: green on addBg = %.2f < 4.5 after quantization", r)
	}
	if r := wcagRatio(t, q(neon.red), q(neon.delBg)); r < 4.5 {
		t.Errorf("neon: red on delBg = %.2f < 4.5 after quantization", r)
	}
	// The two rendered diff-content pairs a 256-color terminal actually
	// shows: line numbers (faintest on addBg/delBg) and highlighted changed
	// spans (addInk/delInk on their word bands).
	for _, pair := range []struct{ name, fg, bg string }{
		{"faintest on addBg", neon.faintest, neon.addBg},
		{"faintest on delBg", neon.faintest, neon.delBg},
		{"addInk on addBgWord", neon.addInk, neon.addBgWord},
		{"delInk on delBgWord", neon.delInk, neon.delBgWord},
	} {
		if r := wcagRatio(t, q(pair.fg), q(pair.bg)); r < 4.5 {
			t.Errorf("neon: %s = %.2f < 4.5 after quantization (%s on %s)", pair.name, r, q(pair.fg), q(pair.bg))
		}
	}
	// Error-card borders are non-text UI components: they need 3:1 against
	// the panel (WCAG 1.4.11) to be distinguishable at all, in both truecolor
	// and 256-color renderings.
	if r := wcagRatio(t, neon.cardErr, neon.panel); r < 3.0 {
		t.Errorf("neon: cardErr border on panel = %.2f < 3.0", r)
	}
	if r := wcagRatio(t, q(neon.cardErr), q(neon.panel)); r < 3.0 {
		t.Errorf("neon: cardErr border on panel = %.2f < 3.0 after quantization", r)
	}
}

// ansi16Hex returns the hex of a color after colorprofile.ANSI conversion —
// the same path lipgloss/bubbletea use on TERM=xterm-style 16-color terminals.
func ansi16Hex(t *testing.T, hexColor string) string {
	t.Helper()
	c := colorprofile.ANSI.Convert(lipgloss.Color(hexColor))
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// TERM=xterm (and other 16-color profiles) force every palette token through
// ansi.Convert16. Saturated cyan/blue success and bright red error collapse
// onto ANSI pairs that fail AA on the green/maroon diff bands. Guard the
// rendered Dune Dark diff pairs — and the selected-row affordances that use
// the same cool success/permission tokens — after the real ANSI conversion.
func TestDuneDarkANSI16Contrast(t *testing.T) {
	var pal palette
	found := false
	for _, entry := range themeRegistry {
		if entry.Name == "dune-dark" {
			pal = entry.Palette
			found = true
			break
		}
	}
	if !found {
		t.Fatal("theme 'dune-dark' is not registered")
	}
	q := func(hexColor string) string { return ansi16Hex(t, hexColor) }

	// Diff sign text and changed-word text: the pairs users actually read on
	// add/del rows under 16-color. Prior values were bright-blue-on-green
	// (1.67:1) and bright-red-on-maroon (2.74:1).
	// Diff sign text, changed-word text, and gutter line numbers (faintest on
	// addBg/delBg): users actually read these under 16-color. Prior values were
	// bright-blue-on-green (1.67:1), bright-red-on-maroon (2.74:1), and
	// bright-cyan-on-green (4.10:1, short of AA). All must clear WCAG AA.
	for _, pair := range []struct{ name, fg, bg string }{
		{"green on addBg", pal.green, pal.addBg},
		{"red on delBg", pal.red, pal.delBg},
		{"addInk on addBgWord", pal.addInk, pal.addBgWord},
		{"delInk on delBgWord", pal.delInk, pal.delBgWord},
		{"faintest on addBg", pal.faintest, pal.addBg},
		{"faintest on delBg", pal.faintest, pal.delBg},
	} {
		if r := wcagRatio(t, q(pair.fg), q(pair.bg)); r < 4.5 {
			t.Errorf("dune-dark: %s = %.2f < 4.5 after ANSI 16-color conversion (%s on %s)",
				pair.name, r, q(pair.fg), q(pair.bg))
		}
	}

	// Selected-row affordances that share the cool success/permission tokens.
	for _, pair := range []struct{ name, fg, bg string }{
		{"accent on selBg", pal.accent, pal.selBg},
		{"blue on selBg", pal.blue, pal.selBg},
		{"faintest on selBg", pal.faintest, pal.selBg},
		{"ink on selBg", pal.ink, pal.selBg},
		{"green on panel", pal.green, pal.panel},
		{"red on panel", pal.red, pal.panel},
	} {
		if r := wcagRatio(t, q(pair.fg), q(pair.bg)); r < 4.5 {
			t.Errorf("dune-dark: %s = %.2f < 4.5 after ANSI 16-color conversion (%s on %s)",
				pair.name, r, q(pair.fg), q(pair.bg))
		}
	}

	// Add/del row bands must stay distinct under 16-color even when both are
	// only the basic green/maroon pair (not the same ANSI slot).
	if q(pal.addBg) == q(pal.delBg) {
		t.Errorf("dune-dark: addBg and delBg collapse to the same ANSI 16-color (%s)", q(pal.addBg))
	}
}

func mustR(t *testing.T, hex string) uint32 {
	t.Helper()
	r, _, _, _ := lipgloss.Color(hex).RGBA()
	return r
}
