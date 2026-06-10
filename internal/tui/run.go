package tui

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/notify"
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
	}
	if options.AltScreen {
		programOpts = append(programOpts, tea.WithAltScreen())
		programOpts = append(programOpts, tea.WithMouseCellMotion())
	}
	if notify.Enabled(notify.Mode(strings.TrimSpace(options.Notify.Mode))) {
		programOpts = append(programOpts, tea.WithReportFocus())
	}
	// Mouse capture is scoped to the alt-screen app so wheel events scroll the
	// managed transcript instead of being translated into ↑/↓ keypresses by the
	// terminal, which would recall composer history.
	program = tea.NewProgram(newModel(ctx, options), programOpts...)

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
