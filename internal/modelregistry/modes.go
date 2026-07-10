package modelregistry

import "strings"

// Mode bundles a model selection, reasoning effort, agent-loop turn budget, and
// an optional tool filter behind a single short name. Modes are presets that let
// callers switch the whole agent profile (smart/deep/fast/...) with one flag or
// command instead of tuning --model/--reasoning-effort/--max-turns by hand.
//
// Model holds a registry id or alias (resolved through Resolve/ResolveWithFallback
// at apply time so deprecation fallbacks and fuzzy aliases stay honored). MaxTurns
// of 0 means "leave the configured/default turn budget untouched". Effort of ""
// means "let the model's effective default apply".
type Mode struct {
	Name        string
	Description string
	Model       string
	Effort      ReasoningEffort
	MaxTurns    int
	// EnabledTools / DisabledTools optionally narrow the tool surface for the
	// mode. Empty slices leave the existing tool selection untouched.
	EnabledTools  []string
	DisabledTools []string
}

// defaultModes is the canonical preset catalog. It is kept data-driven so the
// CLI flag, the TUI command, and tests all read from one source of truth. Models
// reference real registry ids (see catalog.go); efforts reference real
// ReasoningEffort values.
var defaultModes = []Mode{
	{
		Name:        "smart",
		Description: "Balanced daily driver for high-quality agent work.",
		Model:       "claude-sonnet-4.5",
		Effort:      ReasoningEffortMedium,
	},
	{
		Name:        "deep",
		Description: "Hardest tasks: deep reasoning with a larger turn budget.",
		Model:       "claude-opus-4.1",
		Effort:      ReasoningEffortHigh,
		MaxTurns:    160,
	},
	{
		Name:        "fast",
		Description: "Low-latency support for quick edits and summaries.",
		Model:       "claude-haiku-4.5",
		Effort:      ReasoningEffortLow,
		MaxTurns:    15,
	},
	{
		Name:        "large",
		Description: "Long-context work over large inputs.",
		Model:       "gemini-2.5-pro",
		Effort:      ReasoningEffortMedium,
	},
	{
		Name:        "precise",
		Description: "Careful, high-effort reasoning for exacting changes.",
		Model:       "claude-sonnet-4.5",
		Effort:      ReasoningEffortHigh,
	},
}

// Modes returns a copy of the preset catalog, preserving declaration order so
// list output and help text stay stable.
func Modes() []Mode {
	modes := make([]Mode, len(defaultModes))
	for index, mode := range defaultModes {
		modes[index] = cloneMode(mode)
	}
	return modes
}

// LookupMode returns the preset registered under name (case-insensitive,
// whitespace-trimmed). The second result reports whether a preset matched.
func LookupMode(name string) (Mode, bool) {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if normalized == "" {
		return Mode{}, false
	}
	for _, mode := range defaultModes {
		if mode.Name == normalized {
			return cloneMode(mode), true
		}
	}
	return Mode{}, false
}

// ModeNames returns the preset names in declaration order, handy for usage and
// error messages that need to list the valid modes.
func ModeNames() []string {
	names := make([]string, len(defaultModes))
	for index, mode := range defaultModes {
		names[index] = mode.Name
	}
	return names
}

func cloneMode(mode Mode) Mode {
	mode.EnabledTools = append([]string{}, mode.EnabledTools...)
	mode.DisabledTools = append([]string{}, mode.DisabledTools...)
	return mode
}
