package tui

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
)

// Run starts the Zero Bubble Tea shell and returns a process-style exit code.
func Run(ctx context.Context, options Options) int {
	externalSink := options.RuntimeMessageSink
	var program *tea.Program
	options.RuntimeMessageSink = func(msg tea.Msg) {
		if externalSink != nil {
			externalSink(msg)
		}
		if program != nil {
			program.Send(msg)
		}
	}
	options.AltScreen = useAltScreen(options)

	programOpts := []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
		tea.WithFilter(mouseEventFilter()),
	}
	// Honor the no-color.org spec ourselves: NO_COLOR set to ANY non-empty value
	// disables color. bubbletea/colorprofile only respects strconv.ParseBool-style
	// values, so NO_COLOR=yes / NO_COLOR=foo would otherwise leave the UI in full
	// color. Force the Ascii (no-color, bold-still-allowed) profile. (AUDIT-M3)
	if noColorRequested(os.Getenv) {
		programOpts = append(programOpts, tea.WithColorProfile(colorprofile.Ascii))
	}
	initialModel := newModel(ctx, options)
	if initialModel.wantsMouseCapture() {
		initialModel.mouseCapture = true
	}
	program = tea.NewProgram(initialModel, programOpts...)

	if _, err := program.Run(); err != nil {
		// Surface the failure: exiting 1 with zero diagnostics left users
		// guessing why the default chat surface died.
		fmt.Fprintln(os.Stderr, "zero: tui error:", err)
		return 1
	}
	return 0
}

func useAltScreen(_ Options) bool {
	return true
}

// noColorRequested reports whether the no-color.org spec is in effect: NO_COLOR set
// to any non-empty value. Checked here rather than via the colorprofile dependency,
// whose strconv.ParseBool gate ignores common values like NO_COLOR=yes. (AUDIT-M3)
func noColorRequested(getenv func(string) string) bool {
	return getenv("NO_COLOR") != ""
}
