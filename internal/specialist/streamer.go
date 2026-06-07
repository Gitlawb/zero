package specialist

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Gitlawb/zero/internal/streamjson"
	"github.com/Gitlawb/zero/internal/tools"
)

type StreamResult struct {
	Events    []streamjson.Event
	SessionID string
	Text      string
	Tools     []string
	Errors    []string
	Status    string
	ExitCode  int
}

func ParseStream(reader io.Reader) ([]streamjson.Event, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	events := []streamjson.Event{}
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event streamjson.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return nil, fmt.Errorf("parse stream-json line %d: %w", lineNumber, err)
		}
		if event.Type == "" {
			return nil, fmt.Errorf("parse stream-json line %d: type is required", lineNumber)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream-json output: %w", err)
	}
	return events, nil
}

func SummarizeStream(events []streamjson.Event, processExitCode int) StreamResult {
	result := StreamResult{Events: append([]streamjson.Event(nil), events...), ExitCode: processExitCode}
	textParts := []string{}
	finalText := ""
	seenTools := map[string]bool{}
	for _, event := range events {
		if result.SessionID == "" && strings.TrimSpace(event.SessionID) != "" {
			result.SessionID = strings.TrimSpace(event.SessionID)
		}
		switch event.Type {
		case streamjson.EventText:
			textParts = append(textParts, event.Delta)
		case streamjson.EventFinal:
			finalText = event.Text
		case streamjson.EventToolCall:
			if name := strings.TrimSpace(event.Name); name != "" && !seenTools[name] {
				seenTools[name] = true
				result.Tools = append(result.Tools, name)
			}
		case streamjson.EventError:
			message := strings.TrimSpace(event.Message)
			if message == "" {
				message = strings.TrimSpace(event.Code)
			}
			if message != "" {
				result.Errors = append(result.Errors, message)
			}
		case streamjson.EventRunEnd:
			result.Status = strings.TrimSpace(event.Status)
			if event.ExitCode != nil {
				result.ExitCode = *event.ExitCode
			}
		}
	}
	if finalText != "" {
		result.Text = strings.TrimSpace(finalText)
	} else {
		result.Text = strings.TrimSpace(strings.Join(textParts, ""))
	}
	return result
}

func BuildFinalResult(events []streamjson.Event, stderrOutput string, processExitCode int) tools.Result {
	summary := SummarizeStream(events, processExitCode)
	hasErrors := len(summary.Errors) > 0 || summary.ExitCode != 0
	if summary.Status != "" && summary.Status != "success" && summary.Status != "ok" {
		hasErrors = true
	}
	if !hasErrors {
		output := summary.Text
		if summary.SessionID != "" {
			output = "session_id: " + summary.SessionID + "\n" + output
		}
		return tools.Result{Status: tools.StatusOK, Output: strings.TrimSpace(output)}
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "Subagent failed (exit %d)\n", summary.ExitCode)
	if len(summary.Errors) > 0 {
		fmt.Fprintf(&builder, "errors: %s\n", strings.Join(summary.Errors, "; "))
	}
	if stderr := strings.TrimSpace(stderrOutput); stderr != "" {
		fmt.Fprintf(&builder, "stderr:\n%s\n", stderr)
	}
	if len(summary.Tools) > 0 {
		fmt.Fprintf(&builder, "tools executed: %s\n", strings.Join(summary.Tools, ", "))
	}
	if text := strings.TrimSpace(summary.Text); text != "" {
		fmt.Fprintf(&builder, "\n%s\n", text)
	}
	return tools.Result{Status: tools.StatusError, Output: strings.TrimSpace(builder.String())}
}
