package agent

import (
	"context"

	"github.com/Gitlawb/zero/internal/tools"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ToolDefinition struct {
	Name        string
	Description string
	Parameters  tools.Schema
}

type CompletionRequest struct {
	Messages []Message
	Tools    []ToolDefinition
}

type EventType string

const (
	EventText          EventType = "text"
	EventToolCallStart EventType = "tool-call-start"
	EventToolCallDelta EventType = "tool-call-delta"
	EventToolCallEnd   EventType = "tool-call-end"
	EventUsage         EventType = "usage"
	EventDone          EventType = "done"
)

type StreamEvent struct {
	Type              EventType
	Content           string
	ToolCallID        string
	ToolName          string
	ArgumentsFragment string
	PromptTokens      int
	CompletionTokens  int
}

type Provider interface {
	StreamCompletion(ctx context.Context, request CompletionRequest) (<-chan StreamEvent, error)
}

type PermissionMode string

const (
	PermissionModeAuto   PermissionMode = "auto"
	PermissionModeAsk    PermissionMode = "ask"
	PermissionModeUnsafe PermissionMode = "unsafe"
)

type ToolResult struct {
	ToolCallID string
	Name       string
	Status     tools.Status
	Output     string
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

type Options struct {
	MaxTurns       int
	Registry       *tools.Registry
	PermissionMode PermissionMode
	OnText         func(string)
	OnToolCall     func(ToolCall)
	OnToolResult   func(ToolResult)
	OnUsage        func(Usage)
}

type Result struct {
	FinalAnswer string
	Turns       int
	Messages    []Message
}
