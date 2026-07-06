package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// lastAssistantAnswer returns the text of the most recent assistant row — the
// last final answer if one exists, else the last assistant row of any kind. Empty
// when the conversation has no assistant text yet.
func (m model) lastAssistantAnswer() string {
	lastAny := ""
	for i := len(m.transcript) - 1; i >= 0; i-- {
		row := m.transcript[i]
		if row.kind != rowAssistant {
			continue
		}
		if row.final {
			return row.text
		}
		if lastAny == "" {
			lastAny = row.text
		}
	}
	return lastAny
}

// plainTranscriptText renders the conversation as a plain, role-prefixed text
// document for /export — the readable content (user prompts, assistant answers,
// system notes, errors), skipping tool-call/permission UI noise.
func (m model) plainTranscriptText() string {
	var b strings.Builder
	for _, row := range m.transcript {
		var prefix string
		switch row.kind {
		case rowUser:
			prefix = "you: "
		case rowAssistant:
			prefix = "zero: "
		case rowSystem:
			prefix = "· "
		case rowError:
			prefix = "error: "
		default:
			continue
		}
		text := strings.TrimRight(row.text, "\n")
		if strings.TrimSpace(text) == "" {
			continue
		}
		b.WriteString(prefix)
		b.WriteString(text)
		b.WriteString("\n\n")
	}
	return b.String()
}

// handleExportCommand writes the transcript to a file and returns a status
// message. With no argument it derives a timestamped filename in the workspace;
// a relative path is resolved against the workspace root.
func (m model) handleExportCommand(args string) string {
	body := m.plainTranscriptText()
	if strings.TrimSpace(body) == "" {
		return "Export\nnothing to export yet."
	}

	path := strings.TrimSpace(args)
	if path == "" {
		stamp := m.now().Format("20060102-150405")
		path = fmt.Sprintf("zero-transcript-%s.txt", stamp)
	}
	if !filepath.IsAbs(path) && m.cwd != "" {
		path = filepath.Join(m.cwd, path)
	}
	// 0o600: a transcript can include tool/bash output that echoed secrets, so it
	// must not be world/group-readable on a shared machine.
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return "Export\nfailed to write " + path + ": " + err.Error()
	}
	return "Export\nwrote transcript to " + path
}
