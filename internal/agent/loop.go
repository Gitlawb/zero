package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/Gitlawb/zero/internal/tools"
)

const defaultSystemPrompt = "You are Zero, a terminal coding agent. Help with the current workspace and use tools when needed."
const maxTurnsAnswer = "Agent reached maximum number of turns without a final answer."

type pendingToolCall struct {
	id        string
	name      string
	arguments string
}

func Run(ctx context.Context, prompt string, provider Provider, options Options) (Result, error) {
	if provider == nil {
		return Result{}, errors.New("agent provider is required")
	}

	maxTurns := options.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 12
	}

	registry := options.Registry
	if registry == nil {
		registry = tools.NewRegistry()
	}

	permissionMode := options.PermissionMode
	if permissionMode == "" {
		permissionMode = PermissionModeAuto
	}

	messages := []Message{
		{Role: RoleSystem, Content: defaultSystemPrompt},
		{Role: RoleUser, Content: prompt},
	}

	result := Result{Messages: copyMessages(messages)}
	for turn := 0; turn < maxTurns; turn++ {
		result.Turns = turn + 1
		request := CompletionRequest{
			Messages: copyMessages(messages),
			Tools:    toolDefinitions(registry, permissionMode),
		}

		stream, err := provider.StreamCompletion(ctx, request)
		if err != nil {
			result.Messages = copyMessages(messages)
			return result, err
		}

		currentText, toolCalls, err := collectTurn(ctx, stream, options.OnText, options.OnUsage)
		if err != nil {
			result.Messages = copyMessages(messages)
			return result, err
		}

		messages = append(messages, Message{
			Role:      RoleAssistant,
			Content:   currentText,
			ToolCalls: toolCalls,
		})

		if len(toolCalls) == 0 {
			result.FinalAnswer = currentText
			result.Messages = copyMessages(messages)
			return result, nil
		}

		for _, call := range toolCalls {
			if options.OnToolCall != nil {
				options.OnToolCall(call)
			}
			toolResult := executeToolCall(ctx, registry, call, permissionMode)
			if options.OnToolResult != nil {
				options.OnToolResult(toolResult)
			}
			messages = append(messages, Message{
				Role:       RoleTool,
				Content:    toolResult.Output,
				ToolCallID: toolResult.ToolCallID,
			})
		}
	}

	result.FinalAnswer = maxTurnsAnswer
	result.Messages = copyMessages(messages)
	return result, nil
}

func collectTurn(ctx context.Context, stream <-chan StreamEvent, onText func(string), onUsage func(Usage)) (string, []ToolCall, error) {
	currentText := ""
	pending := make(map[string]*pendingToolCall)
	order := make([]string, 0)

	for {
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case event, ok := <-stream:
			if !ok {
				return currentText, buildToolCalls(order, pending), nil
			}

			switch event.Type {
			case EventText:
				currentText += event.Content
				if onText != nil {
					onText(event.Content)
				}
			case EventToolCallStart:
				call := pending[event.ToolCallID]
				if call == nil {
					call = &pendingToolCall{id: event.ToolCallID}
					pending[event.ToolCallID] = call
					order = append(order, event.ToolCallID)
				}
				call.name = event.ToolName
			case EventToolCallDelta:
				call := pending[event.ToolCallID]
				if call == nil {
					call = &pendingToolCall{id: event.ToolCallID}
					pending[event.ToolCallID] = call
					order = append(order, event.ToolCallID)
				}
				call.arguments += event.ArgumentsFragment
			case EventToolCallEnd:
			case EventUsage:
				if onUsage != nil {
					onUsage(Usage{
						PromptTokens:     event.PromptTokens,
						CompletionTokens: event.CompletionTokens,
					})
				}
			case EventDone:
				return currentText, buildToolCalls(order, pending), nil
			}
		}
	}
}

func buildToolCalls(order []string, pending map[string]*pendingToolCall) []ToolCall {
	calls := make([]ToolCall, 0, len(order))
	for _, id := range order {
		call := pending[id]
		if call == nil {
			continue
		}
		calls = append(calls, ToolCall{
			ID:        call.id,
			Name:      call.name,
			Arguments: call.arguments,
		})
	}
	return calls
}

func executeToolCall(ctx context.Context, registry *tools.Registry, call ToolCall, permissionMode PermissionMode) ToolResult {
	args := map[string]any{}
	if call.Arguments != "" {
		if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
			return ToolResult{
				ToolCallID: call.ID,
				Name:       call.Name,
				Status:     tools.StatusError,
				Output:     "Error: Failed to parse arguments for " + call.Name + ": " + err.Error(),
			}
		}
	}

	permissionGranted := permissionMode == PermissionModeUnsafe
	if tool, ok := registry.Get(call.Name); ok && tool.Safety().Permission == tools.PermissionAllow {
		permissionGranted = true
	}

	result := registry.RunWithOptions(ctx, call.Name, args, tools.RunOptions{
		PermissionGranted: permissionGranted,
	})
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Status:     result.Status,
		Output:     result.Output,
	}
}

func toolDefinitions(registry *tools.Registry, permissionMode PermissionMode) []ToolDefinition {
	registeredTools := registry.All()
	definitions := make([]ToolDefinition, 0, len(registeredTools))
	for _, tool := range registeredTools {
		if !isAdvertised(tool, permissionMode) {
			continue
		}
		definitions = append(definitions, ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  tool.Parameters(),
		})
	}

	sort.Slice(definitions, func(left int, right int) bool {
		return definitions[left].Name < definitions[right].Name
	})
	return definitions
}

func isAdvertised(tool tools.Tool, permissionMode PermissionMode) bool {
	if tool.Safety().Permission == tools.PermissionDeny {
		return false
	}
	if permissionMode == PermissionModeAuto {
		return tool.Safety().Permission == tools.PermissionAllow
	}
	return true
}

func copyMessages(messages []Message) []Message {
	copied := make([]Message, len(messages))
	for index, message := range messages {
		copied[index] = message
		if message.ToolCalls != nil {
			copied[index].ToolCalls = append([]ToolCall{}, message.ToolCalls...)
		}
	}
	return copied
}
