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
	}
	if notify.Enabled(notify.Mode(strings.TrimSpace(options.Notify.Mode))) {
		programOpts = append(programOpts, tea.WithReportFocus())
	}
	initialModel := newModel(ctx, options)
	if initialModel.wantsMouseCapture() {
		programOpts = append(programOpts, tea.WithMouseCellMotion())
		initialModel.mouseCapture = true
	}
	// Mouse capture starts enabled only when the initial surface needs it; later
	// surfaces enable/disable it through syncMouseCapture after each update.
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
