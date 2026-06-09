// Package zeroline renders the ZERO "Zeroline" terminal surface — a Zen home page
// and a Statusline (vim/powerline) chat page sharing 5 switchable color themes.
// It is pure presentation: callers (the TUI model) build the data structs from
// live agent state and zeroline turns them into styled terminal frames.
package zeroline

import "github.com/charmbracelet/lipgloss"

// Pal is a resolved color palette (one theme in one light/dark mode), mirroring
// the CSS custom properties in the original mockup.
type Pal struct {
	Bg, Panel, Panel2, Fg, Dim, Mute, Line, Line2 lipgloss.Color
	Accent, Accent2, Green, Amber, Red, Sel       lipgloss.Color
}

// Theme is a named color identity with a dark and a light variant.
type Theme struct {
	Name  string
	Swt   string
	Dark  Pal
	Light Pal
}

func mkPal(bg, panel, panel2, fg, dim, mute, line, line2, accent, accent2, green, amber, red, sel string) Pal {
	c := func(s string) lipgloss.Color { return lipgloss.Color(s) }
	return Pal{c(bg), c(panel), c(panel2), c(fg), c(dim), c(mute), c(line), c(line2),
		c(accent), c(accent2), c(green), c(amber), c(red), c(sel)}
}

// Themes is the ordered list of the 5 selectable color identities (keys 1-5).
var Themes = []Theme{
	{
		Name: "Phosphor", Swt: "#ffb000",
		Dark:  mkPal("#040804", "#0a0f0a", "#0e150e", "#dfe8d6", "#8aa07f", "#566b50", "#16241a", "#22382a", "#ffb000", "#36ff7a", "#36ff7a", "#ffb000", "#ff6b6b", "#171f12"),
		Light: mkPal("#f4f2e6", "#fbfaf0", "#eceada", "#23271c", "#5e6650", "#969c80", "#ddd9c0", "#cbc6a8", "#b66a00", "#1f8a3f", "#1f8a3f", "#b66a00", "#c0392b", "#e7e3cd"),
	},
	{
		Name: "Cyan", Swt: "#38bdf8",
		// Cyan Dark/Light use zeroTheme's exact cyan #34E2EA / green #43D17A / amber #E8B84B / red #F2616B / dim-muted
		// for palette unification (consistent accents/dim across hybrid V1 home + timeline execution).
		// See design doc: "Hybrid Target: V1 + V4 Screen-by-Screen Specification" (color/palette notes,
		// "Exact chrome/layout..."), "## Key Decisions" (nomenclature "var zeroTheme tuiTheme"),
		// PR1 "Theme & shared chrome unification (hybrid base)", and "References" (Lip Gloss dark terminals).
		Dark:  mkPal("#0a111c", "#0e1726", "#11203a", "#cfe0f0", "#6C7682", "#6C7682", "#1d2d44", "#28415f", "#34E2EA", "#5EC8D8", "#43D17A", "#E8B84B", "#F2616B", "#1F6E78"),
		Light: mkPal("#eef2f7", "#ffffff", "#e7eef6", "#15202e", "#6C7682", "#6C7682", "#d3dde8", "#bccbdb", "#34E2EA", "#5EC8D8", "#43D17A", "#E8B84B", "#F2616B", "#1F6E78"),
	},
	{
		Name: "Sage", Swt: "#9cb98f",
		Dark:  mkPal("#14130d", "#1a180f", "#201d12", "#e9e1cc", "#a59c82", "#6f664f", "#2c281b", "#3a3424", "#9cb98f", "#cf915f", "#9cb98f", "#d6a45c", "#cf7d5a", "#241f12"),
		Light: mkPal("#f4ecd8", "#faf4e3", "#efe6cf", "#2b2b2b", "#6c6450", "#a59a7c", "#ddd2b4", "#cdc0a0", "#5f7d57", "#c77b58", "#5f7d57", "#a9802f", "#bb5d3c", "#e9dfc2"),
	},
	{
		Name: "Violet", Swt: "#c084fc",
		Dark:  mkPal("#0c0a16", "#120f20", "#17132b", "#ddd6f0", "#9286b8", "#665a8a", "#241d3a", "#322952", "#c084fc", "#f472b6", "#5eead4", "#fcd34d", "#fb7185", "#1c1633"),
		Light: mkPal("#f3effa", "#ffffff", "#ece5f6", "#241a33", "#5f5278", "#9488ad", "#ddd4ea", "#cabfdd", "#7c3aed", "#c026a3", "#0d9488", "#a16207", "#be123c", "#e7def4"),
	},
	{
		Name: "Mono", Swt: "#cfd3d8",
		Dark:  mkPal("#0c0d0f", "#121316", "#17181c", "#dfe2e6", "#8b9097", "#5b6066", "#23252a", "#2f3238", "#d7dbe0", "#9aa0a8", "#9fd8b4", "#d8c79a", "#e08c8c", "#1b1d21"),
		Light: mkPal("#f4f5f6", "#ffffff", "#ececee", "#1b1d20", "#5c626a", "#9398a0", "#dcdee1", "#c8cbcf", "#2b2f36", "#5c626a", "#1f7a44", "#7a6a42", "#b42318", "#e6e8ea"),
	},
}

// Resolve returns the active palette for a theme index + mode.
func Resolve(variant int, dark bool) Pal {
	if variant < 0 || variant >= len(Themes) {
		variant = 0
	}
	if dark {
		return Themes[variant].Dark
	}
	return Themes[variant].Light
}

// ThemeName returns the display name for a variant index.
func ThemeName(variant int) string {
	if variant < 0 || variant >= len(Themes) {
		variant = 0
	}
	return Themes[variant].Name
}
