package tui

import (
	"context"
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

	programOpts := []tea.ProgramOption{
		tea.WithContext(ctx),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	}
	if notify.Enabled(notify.Mode(strings.TrimSpace(options.Notify.Mode))) {
		programOpts = append(programOpts, tea.WithReportFocus())
	}
	// NOTE: we intentionally do NOT enable mouse capture. Mouse cell-motion
	// reporting routes clicks/drags to the program, which breaks the terminal's
	// native text selection + copy and surprises users who expect normal
	// click/select/copy/paste. The permission modal is fully keyboard-driven
	// (a/y/d/Esc), so capturing the mouse buys little and costs core UX.
	program = tea.NewProgram(newModel(ctx, options), programOpts...)

	if _, err := program.Run(); err != nil {
		return 1
	}
	return 0
}
