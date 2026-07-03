package specialist

import (
	"encoding/json"
	"strings"

	"github.com/Gitlawb/zero/internal/streamjson"
)

// newHarnessDecoder selects the childDecoder for a harness's stdout format
// (agentcli.Harness.Stream). An unrecognized value — including "text" itself,
// and any future format the catalog names before a dedicated decoder exists —
// falls back to textDecoder, which treats stdout as opaque prose: a new
// harness catalog entry never hard-fails a run just because its stream format
// isn't specially understood yet.
func newHarnessDecoder(stream string) childDecoder {
	switch stream {
	case "claude-stream-json":
		return &claudeStreamDecoder{}
	case "codex-json":
		return &codexJSONDecoder{}
	default:
		return &textDecoder{}
	}
}

// claudeStreamDecoder decodes the NDJSON stdout produced by `claude -p
// --output-format stream-json` (and cursor-agent's compatible format): a
// "system" line is startup noise (ignored), an "assistant" line's message
// content blocks become text/tool_call events, and a "result" line carries
// the run's final answer.
type claudeStreamDecoder struct {
	gotResult bool
}

type claudeStreamLine struct {
	Type    string `json:"type"`
	Message *struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
	} `json:"message"`
	Result string `json:"result"`
}

func (d *claudeStreamDecoder) decodeLine(line string) []streamjson.Event {
	var parsed claudeStreamLine
	// A malformed or shape-mismatched line is tolerated, not fatal — the
	// harness's protocol may add fields/line types this decoder doesn't know
	// about yet.
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		return nil
	}
	switch parsed.Type {
	case "assistant":
		if parsed.Message == nil {
			return nil
		}
		var events []streamjson.Event
		for _, block := range parsed.Message.Content {
			switch block.Type {
			case "text":
				if block.Text != "" {
					events = append(events, streamjson.Event{Type: streamjson.EventText, Delta: block.Text})
				}
			case "tool_use":
				var args any
				if len(block.Input) > 0 {
					_ = json.Unmarshal(block.Input, &args)
				}
				events = append(events, streamjson.Event{Type: streamjson.EventToolCall, ID: block.ID, Name: block.Name, Args: args})
			}
		}
		return events
	case "result":
		d.gotResult = true
		return []streamjson.Event{{Type: streamjson.EventFinal, Text: parsed.Result}}
	default:
		// "system"/"init" and any other line types carry nothing the specialist
		// pipeline needs.
		return nil
	}
}

func (d *claudeStreamDecoder) finish(int) []streamjson.Event {
	// No synthetic final when the stream never produced a "result" line (e.g.
	// the CLI crashed mid-run): BuildFinalResult already reports a non-zero
	// exit as an error using stderr, and inventing a final here would just mask
	// the missing result.
	return nil
}

// codexJSONDecoder decodes the NDJSON stdout produced by `codex exec --json`.
// Grounded against a real capture (codex-cli 0.142.5, `codex exec --json
// --skip-git-repo-check "reply with exactly: hi"`), which emits top-level
// events — not the legacy `{"msg":{"type":...}}` wrapper this decoder
// previously expected:
//
//	{"type":"item.completed","item":{"id":"item_3","type":"agent_message","text":"hi"}}
//	{"type":"turn.completed","usage":{"input_tokens":27089,"cached_input_tokens":4992,"output_tokens":24,"reasoning_output_tokens":17}}
//
// "item.completed" fires once per item reaching a terminal state; only
// item.type "agent_message" carries assistant text the specialist pipeline
// cares about (other item types — "error" for config/tooling warnings,
// "command_execution", "file_change" — are noise for this decoder's scope,
// same as legacy's silent drop of "reasoning"/unknown msg types). Codex's
// final answer is "the last agent_message seen", not a dedicated field, so
// the decoder must remember it across lines. "turn.completed" is the run's
// completion marker and carries token usage.
type codexJSONDecoder struct {
	lastMessage string
	done        bool
}

type codexLine struct {
	Type string `json:"type"`
	Item *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (d *codexJSONDecoder) decodeLine(line string) []streamjson.Event {
	var parsed codexLine
	if err := json.Unmarshal([]byte(line), &parsed); err != nil {
		return nil
	}
	switch parsed.Type {
	case "item.completed":
		if parsed.Item == nil || parsed.Item.Type != "agent_message" || parsed.Item.Text == "" {
			return nil
		}
		d.lastMessage = parsed.Item.Text
		return []streamjson.Event{{Type: streamjson.EventText, Delta: parsed.Item.Text}}
	case "turn.completed":
		d.done = true
		var events []streamjson.Event
		if parsed.Usage != nil {
			input, output := parsed.Usage.InputTokens, parsed.Usage.OutputTokens
			total := input + output
			events = append(events, streamjson.Event{
				Type:             streamjson.EventUsage,
				PromptTokens:     &input,
				CompletionTokens: &output,
				TotalTokens:      &total,
			})
		}
		return append(events, streamjson.Event{Type: streamjson.EventFinal, Text: d.lastMessage})
	default:
		return nil
	}
}

func (d *codexJSONDecoder) finish(int) []streamjson.Event {
	// turn.completed never arrived (e.g. the CLI was killed mid-run) but there
	// was at least one agent_message — still surface it as the final answer
	// rather than losing it.
	if d.done || d.lastMessage == "" {
		return nil
	}
	return []streamjson.Event{{Type: streamjson.EventFinal, Text: d.lastMessage}}
}

// textDecoder is the fallback for any harness whose Stream is "text" (or
// unrecognized): stdout is opaque prose, streamed line-by-line as text
// events, with the full accumulated output surfaced as the final answer once
// the process exits cleanly.
type textDecoder struct {
	lines []string
}

func (d *textDecoder) decodeLine(line string) []streamjson.Event {
	d.lines = append(d.lines, line)
	return []streamjson.Event{{Type: streamjson.EventText, Delta: line + "\n"}}
}

func (d *textDecoder) finish(exitCode int) []streamjson.Event {
	if exitCode != 0 {
		// A non-zero exit is reported as an error by BuildFinalResult using
		// stderr; the accumulated stdout is still available via the text deltas
		// already emitted above, so no separate final event is needed here.
		return nil
	}
	return []streamjson.Event{{Type: streamjson.EventFinal, Text: strings.TrimSpace(strings.Join(d.lines, "\n"))}}
}
