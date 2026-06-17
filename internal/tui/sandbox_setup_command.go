package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

const sandboxSetupStatusRowID = "sandbox/setup"

func (m model) startSandboxSetupCommand(args string) (model, tea.Cmd) {
	if strings.TrimSpace(args) != "" {
		m = m.setSandboxSetupStatusRow(sandboxSetupUsageText(commandStatusBlocked, "Usage: /sandbox-setup"))
		return m, nil
	}
	if m.pending {
		m = m.setSandboxSetupStatusRow(sandboxSetupUsageText(commandStatusWarning, "Cannot run sandbox setup while a run is active."))
		return m, nil
	}
	if m.sandboxSetupInFlight {
		m = m.setSandboxSetupStatusRow(sandboxSetupUsageText(commandStatusWarning, "Sandbox setup is already running."))
		return m, nil
	}
	if m.sandboxSetupCommand == nil {
		m = m.setSandboxSetupStatusRow(sandboxSetupUsageText(commandStatusWarning, "Sandbox setup is not available in this TUI session."))
		return m, nil
	}

	m.sandboxSetupSeq++
	id := m.sandboxSetupSeq
	run := m.sandboxSetupCommand
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	m.sandboxSetupInFlight = true
	m = m.setSandboxSetupStatusRow(sandboxSetupRunningText())
	return m, func() tea.Msg {
		return sandboxSetupCommandResultMsg{id: id, result: run(ctx)}
	}
}

func sandboxSetupUsageText(status commandStatus, message string) string {
	return renderCommandOutput(commandOutput{
		Title:  "Sandbox setup",
		Status: status,
		Sections: []commandSection{{
			Title: "Command",
			Lines: []string{message},
		}},
		Hints: []string{"run zero sandbox setup from the shell for the same setup path"},
	})
}

func sandboxSetupRunningText() string {
	return renderCommandOutput(commandOutput{
		Title:  "Sandbox setup",
		Status: commandStatusInfo,
		Sections: []commandSection{{
			Title: "Status",
			Lines: []string{"Running native sandbox setup for this platform."},
		}},
	})
}

func sandboxSetupResultText(result SandboxSetupCommandResult) string {
	status := commandStatusOK
	if result.ExitCode != 0 {
		status = commandStatusBlocked
	}
	lines := sandboxSetupResultLines(result)
	if len(lines) == 0 {
		if result.ExitCode == 0 {
			lines = []string{"Sandbox setup completed."}
		} else {
			lines = []string{"Sandbox setup failed."}
		}
	}
	return renderCommandOutput(commandOutput{
		Title:  "Sandbox setup",
		Status: status,
		Sections: []commandSection{{
			Title: "Result",
			Fields: []commandField{{
				Key:   "exit",
				Value: fmt.Sprintf("%d", result.ExitCode),
			}},
			Lines: lines,
		}},
		Hints: []string{"run /doctor to verify sandbox status"},
	})
}

func sandboxSetupResultLines(result SandboxSetupCommandResult) []string {
	lines := []string{}
	for _, text := range []string{result.Output, result.Error} {
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func (m model) setSandboxSetupStatusRow(text string) model {
	row := transcriptRow{kind: rowSystem, id: sandboxSetupStatusRowID, tool: "sandbox", text: text}
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].id == sandboxSetupStatusRowID {
			m.transcript[i] = row
			return m
		}
	}
	m.transcript = appendTranscriptRow(m.transcript, row)
	return m
}
