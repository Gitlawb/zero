package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sort"

	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const defaultSystemPrompt = "You are Zero, a terminal coding agent. Help with the current workspace and use tools when needed."
const maxTurnsAnswer = "Agent reached maximum number of turns without a final answer."

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

	messages := zeroruntime.SeedMessages(defaultSystemPrompt, prompt)

	result := Result{Messages: copyMessages(messages)}
	for turn := 0; turn < maxTurns; turn++ {
		result.Turns = turn + 1
		request := zeroruntime.CompletionRequest{
			Messages: copyMessages(messages),
			Tools:    toolDefinitions(registry, permissionMode),
		}

		stream, err := provider.StreamCompletion(ctx, request)
		if err != nil {
			result.Messages = copyMessages(messages)
			return result, err
		}

		collected := zeroruntime.CollectStreamWithOptions(ctx, stream, zeroruntime.CollectOptions{
			OnText:  options.OnText,
			OnUsage: options.OnUsage,
		})
		if collected.Error != "" {
			result.Messages = copyMessages(messages)
			return result, errors.New(collected.Error)
		}
		if ctx.Err() != nil {
			result.Messages = copyMessages(messages)
			return result, ctx.Err()
		}

		messages = append(messages, zeroruntime.Message{
			Role:      zeroruntime.MessageRoleAssistant,
			Content:   collected.Text,
			ToolCalls: collected.ToolCalls,
		})

		if len(collected.ToolCalls) == 0 {
			result.FinalAnswer = collected.Text
			result.Messages = copyMessages(messages)
			return result, nil
		}

		for _, call := range collected.ToolCalls {
			if options.OnToolCall != nil {
				options.OnToolCall(call)
			}
			toolResult := executeToolCall(ctx, registry, call, permissionMode)
			if options.OnToolResult != nil {
				options.OnToolResult(toolResult)
			}
			messages = append(messages, zeroruntime.Message{
				Role:       zeroruntime.MessageRoleTool,
				Content:    toolResult.Output,
				ToolCallID: toolResult.ToolCallID,
			})
		}
	}

	result.FinalAnswer = maxTurnsAnswer
	result.Messages = copyMessages(messages)
	return result, nil
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

func toolDefinitions(registry *tools.Registry, permissionMode PermissionMode) []zeroruntime.ToolDefinition {
	registeredTools := registry.All()
	definitions := make([]zeroruntime.ToolDefinition, 0, len(registeredTools))
	for _, tool := range registeredTools {
		if !isAdvertised(tool, permissionMode) {
			continue
		}
		definitions = append(definitions, zeroruntime.ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  schemaToRuntimeMap(tool.Parameters()),
		})
	}

	sort.Slice(definitions, func(left int, right int) bool {
		return definitions[left].Name < definitions[right].Name
	})
	return definitions
}

func schemaToRuntimeMap(schema tools.Schema) map[string]any {
	parameters := map[string]any{
		"type":                 schema.Type,
		"additionalProperties": schema.AdditionalProperties,
	}

	if len(schema.Required) > 0 {
		parameters["required"] = append([]string{}, schema.Required...)
	}

	if len(schema.Properties) > 0 {
		properties := make(map[string]any, len(schema.Properties))
		for name, property := range schema.Properties {
			properties[name] = propertyToRuntimeMap(property)
		}
		parameters["properties"] = properties
	}

	return parameters
}

func propertyToRuntimeMap(property tools.PropertySchema) map[string]any {
	schema := map[string]any{
		"type": property.Type,
	}
	if property.Description != "" {
		schema["description"] = property.Description
	}
	if len(property.Enum) > 0 {
		schema["enum"] = append([]string{}, property.Enum...)
	}
	if property.Default != nil {
		schema["default"] = property.Default
	}
	if property.Minimum != nil {
		schema["minimum"] = *property.Minimum
	}
	if property.Maximum != nil {
		schema["maximum"] = *property.Maximum
	}
	return schema
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
