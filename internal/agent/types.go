package agent

import (
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

type Message = zeroruntime.Message
type Provider = zeroruntime.Provider
type ToolCall = zeroruntime.ToolCall
type Usage = zeroruntime.Usage

type PermissionMode string
type PermissionAction string

const (
	PermissionModeAuto   PermissionMode = "auto"
	PermissionModeAsk    PermissionMode = "ask"
	PermissionModeUnsafe PermissionMode = "unsafe"
)

const (
	PermissionActionAllow  PermissionAction = "allow"
	PermissionActionPrompt PermissionAction = "prompt"
	PermissionActionDeny   PermissionAction = "deny"
)

type ToolResult struct {
	ToolCallID string
	Name       string
	Status     tools.Status
	Output     string
	Meta       map[string]string
}

type PermissionEvent struct {
	ToolCallID        string             `json:"toolCallId"`
	ToolName          string             `json:"name"`
	Action            PermissionAction   `json:"action"`
	Permission        string             `json:"permission"`
	PermissionGranted bool               `json:"permissionGranted,omitempty"`
	PermissionMode    PermissionMode     `json:"permissionMode"`
	Autonomy          string             `json:"autonomy,omitempty"`
	SideEffect        string             `json:"sideEffect"`
	Reason            string             `json:"reason,omitempty"`
	Risk              sandbox.Risk       `json:"risk"`
	Violation         *sandbox.Violation `json:"violation,omitempty"`
	GrantMatched      bool               `json:"grantMatched,omitempty"`
	Grant             *sandbox.Grant     `json:"grant,omitempty"`
}

type Options struct {
	MaxTurns       int
	Registry       *tools.Registry
	PermissionMode PermissionMode
	Autonomy       string
	Sandbox        *sandbox.Engine
	EnabledTools   []string
	DisabledTools  []string
	OnText         func(string)
	OnToolCall     func(ToolCall)
	OnPermission   func(PermissionEvent)
	OnToolResult   func(ToolResult)
	OnUsage        func(Usage)
}

type Result struct {
	FinalAnswer string
	Turns       int
	Messages    []Message
}
