package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/tools"
)

func (m model) execSessionController() (tools.ExecSessionController, bool) {
	if m.registry == nil {
		return nil, false
	}
	tool, ok := m.registry.Get(tools.ExecCommandToolName)
	if !ok {
		return nil, false
	}
	controller, ok := tool.(tools.ExecSessionController)
	return controller, ok
}

func (m model) backgroundTerminalSessions() []tools.ExecSessionSnapshot {
	controller, ok := m.execSessionController()
	if !ok {
		return nil
	}
	return controller.ExecSessions()
}

func (m model) backgroundTerminalSummary() string {
	count := len(m.backgroundTerminalSessions())
	if count == 0 {
		return ""
	}
	plural := ""
	if count != 1 {
		plural = "s"
	}
	return fmt.Sprintf("%d background terminal%s running · /ps to view · /stop to close", count, plural)
}

func (m model) stopAllBackgroundTerminalSessions() []int {
	controller, ok := m.execSessionController()
	if !ok {
		return nil
	}
	return controller.StopAllExecSessions()
}

func (m model) backgroundTerminalsText() string {
	sessions := m.backgroundTerminalSessions()
	card := commandCard{
		Title:   "Background Terminals",
		Summary: []string{fmt.Sprintf("%d running", len(sessions))},
	}
	if len(sessions) == 0 {
		card.Sections = []commandCardSection{{
			Rows: []commandRow{{Text: "No background terminals running."}},
		}}
		return renderCommandCardTranscript(card)
	}
	rows := make([]commandRow, 0, len(sessions))
	now := m.now()
	for _, session := range sessions {
		rows = append(rows, commandRow{Text: formatBackgroundTerminalRow(session, now)})
	}
	card.Sections = []commandCardSection{{
		Title: "running",
		Rows:  rows,
	}}
	card.Actions = []string{"/stop <session_id>", "/stop"}
	return renderCommandCardTranscript(card)
}

func (m model) stopBackgroundTerminalsText(input string) string {
	controller, ok := m.execSessionController()
	if !ok {
		return renderCommandCardTranscript(commandCard{
			Title:   "Background Terminals",
			Summary: []string{"unavailable"},
			Sections: []commandCardSection{{
				Rows: []commandRow{{Text: "exec_command is not registered."}},
			}},
		})
	}
	input = strings.TrimSpace(input)
	if input == "" {
		stopped := m.stopAllBackgroundTerminalSessions()
		return renderStopBackgroundTerminalsCard(stopped, "")
	}
	id, err := strconv.Atoi(input)
	if err != nil || id <= 0 {
		return renderCommandCardTranscript(commandCard{
			Title:   "Background Terminals",
			Summary: []string{"invalid session id"},
			Sections: []commandCardSection{{
				Rows: []commandRow{{Text: "Usage: /stop [session_id]"}},
			}},
		})
	}
	if !controller.StopExecSession(id) {
		return renderCommandCardTranscript(commandCard{
			Title:   "Background Terminals",
			Summary: []string{"not found"},
			Sections: []commandCardSection{{
				Rows: []commandRow{{Text: fmt.Sprintf("No running background terminal with session_id %d.", id)}},
			}},
		})
	}
	return renderStopBackgroundTerminalsCard([]int{id}, "")
}

func renderStopBackgroundTerminalsCard(stopped []int, note string) string {
	card := commandCard{Title: "Background Terminals"}
	if len(stopped) == 0 {
		card.Summary = []string{"none running"}
		card.Sections = []commandCardSection{{
			Rows: []commandRow{{Text: "No background terminals running."}},
		}}
		return renderCommandCardTranscript(card)
	}
	values := make([]string, 0, len(stopped))
	for _, id := range stopped {
		values = append(values, strconv.Itoa(id))
	}
	card.Summary = []string{"stopping " + strings.Join(values, ", ")}
	rows := []commandRow{{Text: "Stopping background terminal sessions."}}
	if strings.TrimSpace(note) != "" {
		rows = append(rows, commandRow{Text: note})
	}
	card.Sections = []commandCardSection{{Rows: rows}}
	return renderCommandCardTranscript(card)
}

func formatBackgroundTerminalRow(session tools.ExecSessionSnapshot, now time.Time) string {
	age := formatTerminalAge(now.Sub(session.StartedAt))
	cwd := strings.TrimSpace(session.RelativeCwd)
	if cwd == "" {
		cwd = shortenPath(session.Cwd)
	}
	command := compactCommandOutputText(session.Command)
	if command == "" {
		command = "command"
	}
	preview := compactCommandOutputText(session.RecentOutput)
	prefix := fmt.Sprintf("%d · %s · %s · %s", session.ID, session.Status, age, cwd)
	if preview != "" {
		return prefix + " · " + command + " · " + preview
	}
	return prefix + " · " + command
}

func formatTerminalAge(duration time.Duration) string {
	if duration < 0 {
		duration = 0
	}
	switch {
	case duration < time.Minute:
		return fmt.Sprintf("%ds", int(duration.Seconds()))
	case duration < time.Hour:
		return fmt.Sprintf("%dm", int(duration.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(duration.Hours()))
	}
}
