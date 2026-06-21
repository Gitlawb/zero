package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestDarkPaletteLimeRefinedTokens pins the Lime Refined dark palette to the
// design-spec values. These are the surface/accent/diff/selection tokens the
// redesign installs; if a future change drifts one, this fails loudly.
func TestDarkPaletteLimeRefinedTokens(t *testing.T) {
	want := map[string]string{
		"bg":         "#0d0f13",
		"panel":      "#14171d",
		"cardHeader": "#161a21",
		"border":     "#232830", // palette.line
		"selection":  "#1a2310", // palette.selBg
		"text":       "#d4d8e0", // palette.ink
		"dim":        "#8b93a1", // palette.muted
		"accent":     "#caff3f",
		"white":      "#f0f3f7",
		"user":       "#9db4ff",
		"ok":         "#3fb950", // palette.green
		"diffAddFg":  "#7ee787", // palette.addInk
		"diffAddBg":  "#102a19", // palette.addBg
		"diffDelFg":  "#ff9b95", // palette.delInk
		"diffDelBg":  "#2a1618", // palette.delBg
	}
	got := map[string]string{
		"bg":         darkPalette.bg,
		"panel":      darkPalette.panel,
		"cardHeader": darkPalette.cardHeader,
		"border":     darkPalette.line,
		"selection":  darkPalette.selBg,
		"text":       darkPalette.ink,
		"dim":        darkPalette.muted,
		"accent":     darkPalette.accent,
		"white":      darkPalette.white,
		"user":       darkPalette.user,
		"ok":         darkPalette.green,
		"diffAddFg":  darkPalette.addInk,
		"diffAddBg":  darkPalette.addBg,
		"diffDelFg":  darkPalette.delInk,
		"diffDelBg":  darkPalette.delBg,
	}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("Lime Refined token %s = %s, want %s", k, got[k], w)
		}
	}
}

// TestLimeRefinedContentTokensAA covers the content-bearing tokens beyond the
// muted/faint/faintest/accent set that TestLightPaletteContrastAndHierarchy
// already guards: the wordmark, user prose, and diff text must clear WCAG AA
// against the worst-case surface they sit on (the panel, or the diff band).
func TestLimeRefinedContentTokensAA(t *testing.T) {
	cases := []struct{ name, fg, bg string }{
		{"ink on panel", darkPalette.ink, darkPalette.panel},
		{"white on panel", darkPalette.white, darkPalette.panel},
		{"user on panel", darkPalette.user, darkPalette.panel},
		{"green on panel", darkPalette.green, darkPalette.panel},
		{"addInk on addBg", darkPalette.addInk, darkPalette.addBg},
		{"delInk on delBg", darkPalette.delInk, darkPalette.delBg},
	}
	for _, c := range cases {
		if r := wcagRatio(t, c.fg, c.bg); r < 4.5 {
			t.Errorf("%s contrast %.2f < 4.5 (WCAG AA): %s on %s", c.name, r, c.fg, c.bg)
		}
	}
}

// TestBuildThemeResolvesLimeTokens guards that buildTheme wires the raw colors
// added this phase (overlay/sidebar canvas + card-header surface) rather than
// leaving them nil, so renderers reading them get a themed value.
func TestBuildThemeResolvesLimeTokens(t *testing.T) {
	for _, p := range []palette{darkPalette, lightPalette} {
		th := buildTheme(p)
		if th.bgCanvas == nil {
			t.Error("buildTheme left bgCanvas nil")
		}
		if th.bgCardHeader == nil {
			t.Error("buildTheme left bgCardHeader nil")
		}
	}
}

// TestNoHexLiteralsOutsideTheme enforces the single-source-of-truth invariant the
// redesign relies on: quoted "#rrggbb" color literals live ONLY in theme.go (the
// darkPalette/lightPalette tables). Every other renderer must consume a named
// zeroTheme token. Guards all later phases (cards, glamour style, sidebar) too.
func TestNoHexLiteralsOutsideTheme(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	hex := regexp.MustCompile(`"#[0-9a-fA-F]{6}"`)
	for _, f := range files {
		if f == "theme.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := hex.FindIndex(b); loc != nil {
			t.Errorf("%s contains a hex color literal %s — colors must live in theme.go palettes", f, b[loc[0]:loc[1]])
		}
	}
}
