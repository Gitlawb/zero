package tui

import (
	"strings"

	"github.com/Gitlawb/zero/internal/redaction"
)

type commandStatus string

const (
	commandStatusOK      commandStatus = "ok"
	commandStatusWarning commandStatus = "warning"
	commandStatusBlocked commandStatus = "blocked"
	commandStatusInfo    commandStatus = "info"
)

type commandOutput struct {
	Title    string
	Status   commandStatus
	Sections []commandSection
	Hints    []string
}

type commandSection struct {
	Title  string
	Fields []commandField
	Lines  []string
	Rows   []commandRow
	Hints  []string
}

type commandField struct {
	Key   string
	Value string
}

type commandRow struct {
	Text string
}

func formatCommandOutput(output commandOutput) string {
	lines := []string{}

	status := normalizeCommandStatus(output.Status)
	title := compactCommandOutputText(output.Title)
	if title == "" {
		lines = append(lines, "Zero")
	} else {
		lines = append(lines, title)
	}
	lines = append(lines, "status: "+string(status))

	for _, section := range output.Sections {
		lines = append(lines, formatCommandSection(section)...)
	}
	for _, hint := range output.Hints {
		if text := compactCommandOutputText(hint); text != "" {
			lines = append(lines, "hint: "+text)
		}
	}

	return strings.Join(lines, "\n")
}

func formatCommandSection(section commandSection) []string {
	lines := []string{}

	if title := compactCommandOutputText(section.Title); title != "" {
		lines = append(lines, title)
	}
	for _, field := range section.Fields {
		key := compactCommandOutputText(field.Key)
		value := compactCommandOutputText(field.Value)
		if key == "" || value == "" {
			continue
		}
		lines = append(lines, "  "+key+": "+value)
	}
	for _, line := range section.Lines {
		if text := compactCommandOutputText(line); text != "" {
			lines = append(lines, "  "+text)
		}
	}
	for _, row := range section.Rows {
		if text := compactCommandOutputText(row.Text); text != "" {
			lines = append(lines, "  - "+text)
		}
	}
	for _, hint := range section.Hints {
		if text := compactCommandOutputText(hint); text != "" {
			lines = append(lines, "  hint: "+text)
		}
	}

	return lines
}

func normalizeCommandStatus(status commandStatus) commandStatus {
	switch status {
	case commandStatusOK, commandStatusWarning, commandStatusBlocked, commandStatusInfo:
		return status
	default:
		return commandStatusInfo
	}
}

func compactCommandOutputText(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	return redaction.RedactString(text, redaction.Options{})
}

func renderCommandOutput(output commandOutput) string {
	return formatCommandOutput(output)
}

func commandBullet(value string) string {
	return "- " + compactCommandOutputText(value)
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
