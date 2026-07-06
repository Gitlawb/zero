package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLastAssistantAnswerPrefersFinal(t *testing.T) {
	m := model{transcript: []transcriptRow{
		{kind: rowUser, text: "hi"},
		{kind: rowAssistant, text: "interim narration"},
		{kind: rowAssistant, text: "the final answer", final: true},
		{kind: rowSystem, text: "worked for 3s"},
	}}
	if got := m.lastAssistantAnswer(); got != "the final answer" {
		t.Fatalf("lastAssistantAnswer = %q, want the final answer", got)
	}

	// With no final row, falls back to the most recent assistant row.
	m2 := model{transcript: []transcriptRow{
		{kind: rowAssistant, text: "first"},
		{kind: rowAssistant, text: "second"},
	}}
	if got := m2.lastAssistantAnswer(); got != "second" {
		t.Fatalf("fallback lastAssistantAnswer = %q, want second", got)
	}

	// Empty when there's no assistant text.
	if got := (model{transcript: []transcriptRow{{kind: rowUser, text: "hi"}}}).lastAssistantAnswer(); got != "" {
		t.Fatalf("expected empty answer, got %q", got)
	}
}

func TestPlainTranscriptTextSkipsNoise(t *testing.T) {
	m := model{transcript: []transcriptRow{
		{kind: rowUser, text: "add a flag"},
		{kind: rowToolCall, tool: "bash", text: "go build"},
		{kind: rowToolResult, tool: "bash", text: "ok"},
		{kind: rowAssistant, text: "Done — added --version.", final: true},
		{kind: rowError, text: "provider error: boom"},
	}}
	out := m.plainTranscriptText()
	if !strings.Contains(out, "you: add a flag") || !strings.Contains(out, "zero: Done — added --version.") {
		t.Fatalf("export missing conversation text:\n%s", out)
	}
	if !strings.Contains(out, "error: provider error: boom") {
		t.Fatalf("export should include error rows:\n%s", out)
	}
	if strings.Contains(out, "go build") || strings.Contains(out, "bash") {
		t.Fatalf("export should skip tool-call noise:\n%s", out)
	}
}

func TestHandleExportCommandWritesFile(t *testing.T) {
	dir := t.TempDir()
	m := model{
		cwd: dir,
		now: func() time.Time { return time.Date(2026, 7, 4, 9, 30, 0, 0, time.UTC) },
		transcript: []transcriptRow{
			{kind: rowUser, text: "hello"},
			{kind: rowAssistant, text: "hi there", final: true},
		},
	}

	// Explicit relative path resolves against cwd.
	msg := m.handleExportCommand("out.txt")
	if !strings.Contains(msg, "wrote transcript to") {
		t.Fatalf("export status = %q", msg)
	}
	outPath := filepath.Join(dir, "out.txt")
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("reading exported file: %v", err)
	}
	if !strings.Contains(string(data), "you: hello") || !strings.Contains(string(data), "zero: hi there") {
		t.Fatalf("exported content = %q", string(data))
	}
	// The transcript may contain secrets echoed in tool output; it must not be
	// world/group-readable. Unix perm bits are meaningless on Windows (os.WriteFile
	// ignores them and Stat reports 0666), so only assert the mode where it applies.
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(outPath); err != nil {
			t.Fatalf("stat exported file: %v", err)
		} else if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("exported file mode = %o, want 0600", perm)
		}
	}

	// No-arg export derives a timestamped filename in cwd.
	if msg := m.handleExportCommand(""); !strings.Contains(msg, "zero-transcript-20260704-093000.txt") {
		t.Fatalf("default export status = %q", msg)
	}

	// Nothing to export on an empty conversation.
	empty := model{cwd: dir, now: m.now}
	if msg := empty.handleExportCommand(""); !strings.Contains(msg, "nothing to export") {
		t.Fatalf("empty export status = %q", msg)
	}
}
