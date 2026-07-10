# TUI Themes

Zero's TUI ships a set of built-in color themes. Pick one with `--theme <name>`,
the `ZERO_THEME` environment variable, or the `/theme <name>` command while
running (no argument shows the active theme). `auto` (the default) follows the
terminal's detected background.

Run `/theme` with no argument to list the registered names.

## Claude (`claude`)

A warm, low-density palette: sand/cream surface, charcoal ink, and a soft
amber accent.

## Codex (`codex`)

A high-density, neon-on-black palette: pitch-black surface, bright green ink,
and a cyan accent.

## Adding a theme

Every theme is a `palette` (a table of color hex tokens) plus one entry in
`themeRegistry`, both in `internal/tui/theme_palettes.go`. `buildTheme` in
`internal/tui/theme.go` turns a `palette` into the resolved `lipgloss.Style`
set every renderer reads from the active `zeroTheme`. Adding a theme means
adding a new `palette{...}` literal and a `themeRegistry` entry; nothing else
needs editing.

New palettes must clear the WCAG AA contrast and gray-ramp invariants
asserted in `internal/tui/theme_select_test.go` against the whole registry.
