package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/sandbox"
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
			Tools:    toolDefinitions(registry, permissionMode, options),
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
			toolResult := executeToolCall(ctx, registry, call, permissionMode, options)
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

func executeToolCall(ctx context.Context, registry *tools.Registry, call ToolCall, permissionMode PermissionMode, options Options) ToolResult {
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
	if !ToolAllowedByFilters(call.Name, options.EnabledTools, options.DisabledTools) {
		return ToolResult{
			ToolCallID: call.ID,
			Name:       call.Name,
			Status:     tools.StatusError,
			Output:     `Error: Tool "` + call.Name + `" is not enabled for this run.`,
		}
	}

	tool, toolFound := registry.Get(call.Name)
	permissionGranted := permissionMode == PermissionModeUnsafe
	if toolFound && tool.Safety().Permission == tools.PermissionAllow {
		permissionGranted = true
	}

	var preflightDecision *sandbox.Decision
	if toolFound && options.Sandbox != nil {
		decision := options.Sandbox.Evaluate(ctx, sandboxRequest(call.Name, tool, args, permissionGranted, permissionMode, options))
		preflightDecision = &decision
	}

	decisionReason := ""
	if toolFound && options.OnPermissionRequest != nil && shouldRequestPermission(tool, permissionGranted, preflightDecision) {
		requestEvent, ok := buildPermissionEvent(call, tool, args, permissionGranted, permissionMode, options, preflightDecision)
		if !ok {
			requestEvent = fallbackPermissionEvent(call, tool, args, permissionMode, options)
		}
		request := permissionRequestFromEvent(requestEvent, args)
		decision, err := requestPermission(ctx, request, options)
		if err != nil {
			decision = PermissionDecision{Action: PermissionDecisionDeny, Reason: err.Error()}
		}
		decision.Action = normalizePermissionDecisionAction(decision.Action)
		decisionReason = strings.TrimSpace(decision.Reason)
		switch decision.Action {
		case PermissionDecisionAllow:
			permissionGranted = true
		case PermissionDecisionAlwaysAllow:
			permissionGranted = true
			grant, err := persistPermissionGrant(call.Name, decisionReason, options)
			if err != nil {
				emitDeniedPermission(options, call, requestEvent, "failed to persist permission grant: "+err.Error())
				return deniedPermissionResult(call, "failed to persist permission grant: "+err.Error(), requestEvent)
			}
			requestEvent.GrantMatched = true
			requestEvent.Grant = &grant
		default:
			emitDeniedPermission(options, call, requestEvent, decisionReason)
			return deniedPermissionResult(call, decisionReason, requestEvent)
		}
	}

	result := registry.RunWithOptions(ctx, call.Name, args, tools.RunOptions{
		PermissionGranted: permissionGranted,
		PermissionMode:    string(permissionMode),
		Autonomy:          options.Autonomy,
		Sandbox:           options.Sandbox,
		ToolCallID:        call.ID,
		SessionID:         options.SessionID,
		Model:             options.Model,
		ReasoningEffort:   options.ReasoningEffort,
		Depth:             options.Depth,
		Cwd:               options.Cwd,
		// Note: we no longer rely on OnSandboxDecision callback for capture here
		// (it is still supported for other observers and is invoked asynchronously in the registry).
		// The sandbox decision (if any) is now returned synchronously on the Result for permission event building.
	})
	sandboxDecision := result.SandboxDecision
	if toolFound && options.OnPermission != nil {
		if event, ok := buildPermissionEvent(call, tool, args, permissionGranted, permissionMode, options, sandboxDecision); ok {
			event.DecisionReason = decisionReason
			options.OnPermission(event)
		}
	}
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Status:     result.Status,
		Output:     result.Output,
		Meta:       result.Meta,
	}
}

func sandboxRequest(toolName string, tool tools.Tool, args map[string]any, permissionGranted bool, permissionMode PermissionMode, options Options) sandbox.Request {
	safety := tool.Safety()
	return sandbox.Request{
		WorkspaceRoot:     "",
		ToolName:          toolName,
		SideEffect:        sandbox.SideEffect(safety.SideEffect),
		Permission:        sandbox.Permission(safety.Permission),
		PermissionGranted: permissionGranted,
		PermissionMode:    sandbox.PermissionMode(permissionMode),
		Autonomy:          sandbox.Autonomy(options.Autonomy),
		Args:              args,
		Reason:            safety.Reason,
	}
}

func shouldRequestPermission(tool tools.Tool, permissionGranted bool, decision *sandbox.Decision) bool {
	if permissionGranted || tool.Safety().Permission != tools.PermissionPrompt {
		return false
	}
	if decision != nil {
		return decision.Action == sandbox.ActionPrompt
	}
	return true
}

func requestPermission(ctx context.Context, request PermissionRequest, options Options) (PermissionDecision, error) {
	if options.OnPermissionRequest == nil {
		return PermissionDecision{Action: PermissionDecisionDeny, Reason: request.Reason}, nil
	}
	return options.OnPermissionRequest(ctx, request)
}

func normalizePermissionDecisionAction(action PermissionDecisionAction) PermissionDecisionAction {
	switch action {
	case PermissionDecisionAllow, PermissionDecisionAlwaysAllow:
		return action
	default:
		return PermissionDecisionDeny
	}
}

func persistPermissionGrant(toolName string, reason string, options Options) (sandbox.Grant, error) {
	if options.Sandbox == nil {
		return sandbox.Grant{}, errors.New("sandbox engine is not configured")
	}
	maxAutonomy := sandbox.Autonomy(options.Autonomy)
	if maxAutonomy == "" {
		maxAutonomy = sandbox.AutonomyMedium
	}
	if normalized, err := sandbox.NormalizeAutonomy(maxAutonomy); err == nil {
		maxAutonomy = normalized
	}
	return options.Sandbox.Grant(sandbox.GrantInput{
		ToolName:    toolName,
		Decision:    sandbox.GrantAllow,
		MaxAutonomy: maxAutonomy,
		Reason:      reason,
	})
}

func emitDeniedPermission(options Options, call ToolCall, requestEvent PermissionEvent, reason string) {
	if options.OnPermission == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = requestEvent.Reason
	}
	event := requestEvent
	event.ToolCallID = call.ID
	event.ToolName = call.Name
	event.Action = PermissionActionDeny
	event.PermissionGranted = false
	event.DecisionReason = reason
	options.OnPermission(event)
}

func deniedPermissionResult(call ToolCall, reason string, requestEvent PermissionEvent) ToolResult {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = requestEvent.Reason
	}
	if reason == "" {
		reason = "tool requires approval before execution"
	}
	event := requestEvent
	event.Action = PermissionActionDeny
	event.PermissionGranted = false
	event.DecisionReason = reason
	if event.Risk.Level == "" {
		event.Risk = sandbox.Risk{Level: sandbox.RiskMedium, Reason: reason}
	}
	if requestEvent.ToolName == "" {
		event.ToolName = call.Name
	}
	return ToolResult{
		ToolCallID: call.ID,
		Name:       call.Name,
		Status:     tools.StatusError,
		Output:     "Error: Permission denied for " + call.Name + ": " + reason,
		Meta: map[string]string{
			"permission_action": string(event.Action),
		},
	}
}

func buildPermissionEvent(call ToolCall, tool tools.Tool, args map[string]any, permissionGranted bool, permissionMode PermissionMode, options Options, decision *sandbox.Decision) (PermissionEvent, bool) {
	safety := tool.Safety()
	var action PermissionAction
	reason := safety.Reason
	risk := sandbox.Classify(sandbox.Request{
		WorkspaceRoot:     "",
		ToolName:          call.Name,
		SideEffect:        sandbox.SideEffect(safety.SideEffect),
		Permission:        sandbox.Permission(safety.Permission),
		PermissionGranted: permissionGranted,
		PermissionMode:    sandbox.PermissionMode(permissionMode),
		Autonomy:          sandbox.Autonomy(options.Autonomy),
		Args:              args,
		Reason:            safety.Reason,
	})
	var violation *sandbox.Violation
	grantMatched := false
	var grant *sandbox.Grant

	if decision != nil {
		action = permissionActionFromSandbox(decision.Action)
		if decision.Reason != "" {
			reason = decision.Reason
		}
		risk = decision.Risk
		violation = decision.Violation
		grantMatched = decision.GrantMatched
		grant = decision.Grant
	} else {
		switch safety.Permission {
		case tools.PermissionDeny:
			action = PermissionActionDeny
		case tools.PermissionPrompt:
			if permissionGranted {
				action = PermissionActionAllow
			} else {
				action = PermissionActionPrompt
			}
		default:
			return PermissionEvent{}, false
		}
	}

	if safety.Permission == tools.PermissionAllow && action == PermissionActionAllow && !grantMatched && violation == nil {
		return PermissionEvent{}, false
	}

	autonomy := options.Autonomy
	if normalized, err := sandbox.NormalizeAutonomy(sandbox.Autonomy(autonomy)); err == nil {
		autonomy = string(normalized)
	}

	return PermissionEvent{
		ToolCallID:        call.ID,
		ToolName:          call.Name,
		Action:            action,
		Permission:        string(safety.Permission),
		PermissionGranted: permissionGranted,
		PermissionMode:    permissionMode,
		Autonomy:          autonomy,
		SideEffect:        string(safety.SideEffect),
		Reason:            reason,
		Risk:              risk,
		Violation:         violation,
		GrantMatched:      grantMatched,
		Grant:             grant,
	}, true
}

func fallbackPermissionEvent(call ToolCall, tool tools.Tool, args map[string]any, permissionMode PermissionMode, options Options) PermissionEvent {
	event, _ := buildPermissionEvent(call, tool, args, false, permissionMode, options, nil)
	return event
}

func permissionRequestFromEvent(event PermissionEvent, args map[string]any) PermissionRequest {
	return PermissionRequest{
		ToolCallID:     event.ToolCallID,
		ToolName:       event.ToolName,
		Action:         event.Action,
		Permission:     event.Permission,
		PermissionMode: event.PermissionMode,
		Autonomy:       event.Autonomy,
		SideEffect:     event.SideEffect,
		Reason:         event.Reason,
		Risk:           event.Risk,
		Args:           cloneArgs(args),
		Violation:      event.Violation,
		GrantMatched:   event.GrantMatched,
		Grant:          event.Grant,
	}
}

func cloneArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	copied := make(map[string]any, len(args))
	for key, value := range args {
		copied[key] = value
	}
	return copied
}

func permissionActionFromSandbox(action sandbox.Action) PermissionAction {
	switch action {
	case sandbox.ActionAllow:
		return PermissionActionAllow
	case sandbox.ActionDeny:
		return PermissionActionDeny
	default:
		return PermissionActionPrompt
	}
}

func toolDefinitions(registry *tools.Registry, permissionMode PermissionMode, options Options) []zeroruntime.ToolDefinition {
	registeredTools := registry.All()
	definitions := make([]zeroruntime.ToolDefinition, 0, len(registeredTools))
	for _, tool := range registeredTools {
		if !ToolVisible(tool, permissionMode, options.EnabledTools, options.DisabledTools) {
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

func ToolVisible(tool tools.Tool, permissionMode PermissionMode, enabledTools []string, disabledTools []string) bool {
	return ToolAllowedByFilters(tool.Name(), enabledTools, disabledTools) && ToolAdvertised(tool, permissionMode)
}

func ToolAllowedByFilters(name string, enabledTools []string, disabledTools []string) bool {
	if len(enabledTools) > 0 {
		if !containsToolName(enabledTools, name) {
			return false
		}
	}
	if containsToolName(disabledTools, name) {
		return false
	}
	return true
}

func containsToolName(names []string, name string) bool {
	for _, candidate := range names {
		if candidate == name {
			return true
		}
	}
	return false
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
	if property.Items != nil {
		schema["items"] = propertyToRuntimeMap(*property.Items)
	}
	if property.Minimum != nil {
		schema["minimum"] = *property.Minimum
	}
	if property.Maximum != nil {
		schema["maximum"] = *property.Maximum
	}
	return schema
}

func ToolAdvertised(tool tools.Tool, permissionMode PermissionMode) bool {
	if tool.Safety().Permission == tools.PermissionDeny {
		return false
	}
	if permissionMode == PermissionModeAuto {
		return tool.Safety().Permission == tools.PermissionAllow || tool.Safety().AdvertiseInAuto
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
