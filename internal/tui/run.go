package tui

import (
	"context"
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the Zero Bubble Tea shell and returns a process-style exit code.
func Run(ctx context.Context, options Options) int {
	program := tea.NewProgram(
		newModel(ctx, options),
		tea.WithContext(ctx),
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
	)

	if _, err := program.Run(); err != nil {
		return 1
	}
	return 0
}
