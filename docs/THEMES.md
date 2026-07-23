# TUI Themes

Zero's TUI ships a set of built-in color themes. Pick one with `--theme <name>`,
the `ZERO_THEME` environment variable, or the `/theme <name>` command while
running. `auto` (the default) follows the terminal's detected background.

Run `/theme` with no argument to open a picker: move through the list to
live-preview each theme, press Enter to apply the highlighted one, or Esc to
cancel and restore the previously active theme. Run `/theme list` to print
the active theme and the registered names without opening the picker.

## Dune (`dune`)

A warm sand-and-cream palette: sand/cream surface, charcoal ink, and a soft
amber accent.

## Neon (`neon`)

A neon-on-black palette: pitch-black surface, bright green ink, and a cyan accent.

## Adding a theme

Every theme is a `palette` (a table of color hex tokens) plus one entry in
`themeRegistry`, both in `internal/tui/theme_palettes.go`. `buildTheme` in
`internal/tui/theme.go` turns a `palette` into the resolved `lipgloss.Style`
set every renderer reads from the active `zeroTheme`. Adding a theme means
adding a new `palette{...}` literal, a `themeRegistry` entry, and test
coverage for the new palette (see below).

Registry-wide tests in `internal/tui/theme_select_test.go` assert the basic
WCAG AA text tokens, the gray-ramp order, the diff word-span pairs, and the
selected-row band for every entry. The rendered-surface invariants beyond
those (permission surfaces, selected-row secondary text, diff gutters, and
the xterm-256 downsampling checks) are asserted per palette, not against the
whole registry: `TestExtendedThemeContrastInvariants` and
`TestExtendedThemeANSI256Contrast` enumerate the palettes they cover. A new
theme must be added to those tests (or given equivalent palette-specific
assertions), or CI can stay green while its permission, selected-row, and
diff surfaces ship unreadable.
