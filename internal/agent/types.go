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
	Meta       map[string]string
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
	OnToolResult   func(ToolResult)
	OnUsage        func(Usage)
}

type Result struct {
	FinalAnswer string
	Turns       int
	Messages    []Message
}
