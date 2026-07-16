package tools

import "context"

const RequestPermissionsToolName = "request_permissions"

type requestPermissionsTool struct {
	baseTool
}

func NewRequestPermissionsTool() Tool {
	return requestPermissionsTool{
		baseTool: baseTool{
			name:        RequestPermissionsToolName,
			description: "Request additional filesystem or network permissions from the user. Granted permissions apply to later tool calls in this task, or for the rest of the session when approved with session scope.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"reason": {
						Type:        "string",
						Description: "Optional short explanation for why additional permissions are needed.",
					},
					"environment_id": {
						Type:        "string",
						Description: "Environment id from the active environment context. Omit to use the current workspace.",
					},
					"permissions": requestPermissionsProfileSchema(),
				},
				Required:             []string{"permissions"},
				AdditionalProperties: false,
			},
			safety: Safety{
				SideEffect: SideEffectNone,
				Permission: PermissionAllow,
				Reason:     "Requests a user permission decision but does not read, write, run commands, or use the network.",
			},
			// Interactive permission escalation.
			capabilities: ToolCapabilities{Effect: EffectInteractive, ThreadSafe: false},
		},
	}
}

func (tool requestPermissionsTool) Run(context.Context, map[string]any) Result {
	return errorResult("Error: request_permissions must be handled by the agent runtime.")
}

func requestPermissionsProfileSchema() PropertySchema {
	return PropertySchema{
		Type: "object",
		Properties: map[string]PropertySchema{
			"network": {
				Type: "object",
				Properties: map[string]PropertySchema{
					"enabled": {
						Type:        "boolean",
						Description: "Set true to request network access.",
					},
				},
			},
			"file_system": {
				Type: "object",
				Properties: map[string]PropertySchema{
					"read": {
						Type:        "array",
						Description: "Filesystem paths to read. Relative paths resolve against the workspace.",
						Items:       &PropertySchema{Type: "string"},
					},
					"write": {
						Type:        "array",
						Description: "Filesystem paths to write. Relative paths resolve against the workspace.",
						Items:       &PropertySchema{Type: "string"},
					},
					"deny_read": {
						Type:        "array",
						Description: "Filesystem paths to keep unreadable.",
						Items:       &PropertySchema{Type: "string"},
					},
					"entries": {
						Type:        "array",
						Description: "Canonical filesystem permission entries.",
						Items: &PropertySchema{
							Type: "object",
							Properties: map[string]PropertySchema{
								"path": {
									Type: "object",
									Properties: map[string]PropertySchema{
										"type": {
											Type:        "string",
											Description: "Use path for normal filesystem paths.",
											Enum:        []string{"path"},
										},
										"path": {
											Type:        "string",
											Description: "Filesystem path. Relative paths resolve against the workspace.",
										},
									},
									Required: []string{"type", "path"},
								},
								"access": {
									Type:        "string",
									Description: "Permission for this path.",
									Enum:        []string{"read", "write", "deny"},
								},
							},
							Required: []string{"path", "access"},
						},
					},
				},
			},
		},
		Required: []string{},
	}
}
