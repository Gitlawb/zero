package mcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/tools"
)

type RegisterOptions struct {
	PermissionStore *PermissionStore
	Autonomy        PermissionAutonomy
	ClientFactory   func(context.Context, Server) (ToolClient, error)
}

// SkippedServer records an MCP server that was not registered because it could
// not be reached or its tools could not be validated. Registration is
// best-effort per server: one unreachable server is skipped (and reported here)
// rather than aborting startup or disabling the others.
type SkippedServer struct {
	Name string
	Err  error
}

type Runtime struct {
	clients []ToolClient
	skipped []SkippedServer
	once    sync.Once
	err     error
}

// Skipped returns the servers that were skipped during registration (unreachable
// or invalid), so the caller can warn the user without failing the launch.
func (runtime *Runtime) Skipped() []SkippedServer {
	if runtime == nil {
		return nil
	}
	return runtime.skipped
}

var unsafeToolNameChars = regexp.MustCompile(`[^A-Za-z0-9_]+`)

func RegisterTools(ctx context.Context, registry *tools.Registry, cfg config.MCPConfig, options RegisterOptions) (*Runtime, error) {
	if registry == nil {
		return nil, fmt.Errorf("MCP tool registry is required")
	}
	servers, err := NormalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{}
	if len(servers) == 0 {
		return runtime, nil
	}

	factory := options.ClientFactory
	if factory == nil {
		factory = func(ctx context.Context, server Server) (ToolClient, error) {
			return Connect(ctx, server)
		}
	}

	// Registration is best-effort across servers but atomic within a server: each
	// server is connected and ALL of its tools validated before any are committed,
	// so a server never contributes a partial tool set (no tools pointing at a
	// now-closed client). A server that cannot be reached, lists no valid tools, or
	// conflicts with an already-committed tool is SKIPPED — recorded in
	// runtime.skipped, not fatal — so one unreachable MCP server can't abort startup
	// or disable the others. The conflict check still spans the registry plus every
	// tool committed by an earlier server in this call.
	staged := make([]registryTool, 0)
	stagedNames := make(map[string]struct{})
	for _, server := range servers {
		client, serverTools, stageErr := stageServer(ctx, registry, factory, server, options, stagedNames)
		if stageErr != nil {
			runtime.skipped = append(runtime.skipped, SkippedServer{Name: server.Name, Err: stageErr})
			continue
		}
		runtime.clients = append(runtime.clients, client)
		for _, tool := range serverTools {
			stagedNames[tool.Name()] = struct{}{}
			staged = append(staged, tool)
		}
	}
	// Commit the tools of every server that fully validated.
	for _, tool := range staged {
		registry.Register(tool)
	}
	return runtime, nil
}

// stageServer connects to one server and validates ALL of its tools against the
// registry and the names already committed by earlier servers. It returns the
// client and the server's tools only when every tool is named and conflict-free;
// on any failure it closes the client and returns an error, so the caller skips
// the whole server (per-server atomicity — never a partial tool set, never a
// dangling tool on a closed client).
func stageServer(
	ctx context.Context,
	registry *tools.Registry,
	factory func(context.Context, Server) (ToolClient, error),
	server Server,
	options RegisterOptions,
	stagedNames map[string]struct{},
) (ToolClient, []registryTool, error) {
	client, err := factory(ctx, server)
	if err != nil {
		return nil, nil, err
	}
	remoteTools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("list MCP tools for %s: %w", server.Name, err)
	}
	serverTools := make([]registryTool, 0, len(remoteTools))
	localNames := make(map[string]struct{})
	for _, remote := range remoteTools {
		if strings.TrimSpace(remote.Name) == "" {
			_ = client.Close()
			return nil, nil, fmt.Errorf("MCP server %s returned a tool without a name", server.Name)
		}
		tool := newRegistryTool(server, remote, client, options)
		if existing, ok := registry.Get(tool.Name()); ok {
			_ = client.Close()
			return nil, nil, fmt.Errorf("MCP tool %s from %s conflicts with existing tool %s", remote.Name, server.Name, existing.Name())
		}
		if _, ok := stagedNames[tool.Name()]; ok {
			_ = client.Close()
			return nil, nil, fmt.Errorf("MCP tool %s from %s conflicts with another MCP tool named %s", remote.Name, server.Name, tool.Name())
		}
		if _, ok := localNames[tool.Name()]; ok {
			_ = client.Close()
			return nil, nil, fmt.Errorf("MCP tool %s from %s conflicts with another tool from the same server", remote.Name, server.Name)
		}
		localNames[tool.Name()] = struct{}{}
		serverTools = append(serverTools, tool)
	}
	return client, serverTools, nil
}

func (runtime *Runtime) Close() error {
	if runtime == nil {
		return nil
	}
	runtime.once.Do(func() {
		for _, client := range runtime.clients {
			if err := client.Close(); err != nil && runtime.err == nil {
				runtime.err = err
			}
		}
	})
	return runtime.err
}

type registryTool struct {
	name       string
	server     Server
	remote     RemoteTool
	client     ToolClient
	parameters tools.Schema
	safety     tools.Safety
}

func newRegistryTool(server Server, remote RemoteTool, client ToolClient, options RegisterOptions) registryTool {
	remote.Name = strings.TrimSpace(remote.Name)
	name := registryToolName(server.Name, remote.Name)
	permission := tools.PermissionPrompt
	if isPersistentlyApproved(options.PermissionStore, server, remote.Name, defaultAutonomy(options.Autonomy)) {
		permission = tools.PermissionAllow
	}
	return registryTool{
		name:       name,
		server:     server,
		remote:     remote,
		client:     client,
		parameters: SchemaFromMCP(remote.InputSchema),
		safety: tools.Safety{
			SideEffect: tools.SideEffectNetwork,
			Permission: permission,
			Reason:     fmt.Sprintf("MCP tool %s/%s runs through the configured %s server.", server.Name, remote.Name, server.Type),
		},
	}
}

func (tool registryTool) Name() string {
	return tool.name
}

func (tool registryTool) Description() string {
	if strings.TrimSpace(tool.remote.Description) != "" {
		return tool.remote.Description
	}
	return fmt.Sprintf("Call MCP tool %s/%s", tool.server.Name, tool.remote.Name)
}

func (tool registryTool) Parameters() tools.Schema {
	return tool.parameters
}

func (tool registryTool) Safety() tools.Safety {
	return tool.safety
}

// Deferred marks every MCP tool as deferred-eligible: when many MCP tools are
// registered the agent loop may withhold their full schema and advertise them
// via tool_search. Built-in tools do not implement this interface and stay
// eager.
func (tool registryTool) Deferred() bool {
	return true
}

// MCPServerName reports the tool's originating MCP server name so the deferred-
// tools reminder labels it correctly, even when the sanitized server token in the
// synthesized tool name contains an underscore (which the name-only parser would
// truncate). It returns the true configured server name, not the sanitized token.
func (tool registryTool) MCPServerName() string {
	return tool.server.Name
}

func (tool registryTool) Run(ctx context.Context, args map[string]any) tools.Result {
	result, err := tool.client.CallTool(ctx, tool.remote.Name, args)
	if err != nil {
		return tools.Result{
			Status: tools.StatusError,
			Output: "Error: MCP tool " + tool.server.Name + "/" + tool.remote.Name + " failed: " + err.Error(),
			Meta:   tool.meta(),
		}
	}
	status := tools.StatusOK
	if result.IsError {
		status = tools.StatusError
	}
	output := TextContent(result.Content)
	if output == "" {
		output = "(empty MCP tool result)"
	}
	return tools.Result{
		Status: status,
		Output: output,
		Meta:   tool.meta(),
	}
}

func (tool registryTool) meta() map[string]string {
	return map[string]string{
		"mcp.server":   tool.server.Name,
		"mcp.tool":     tool.remote.Name,
		"mcp.identity": tool.server.Identity,
	}
}

func registryToolName(serverName string, toolName string) string {
	serverPart := sanitizeToolNamePart(serverName)
	toolPart := sanitizeToolNamePart(toolName)
	if toolPart == "" {
		toolPart = "tool"
	}
	return "mcp_" + serverPart + "_" + toolPart
}

func sanitizeToolNamePart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = unsafeToolNameChars.ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "server"
	}
	return value
}

func isPersistentlyApproved(store *PermissionStore, server Server, toolName string, autonomy PermissionAutonomy) bool {
	if store == nil {
		return false
	}
	approved, err := store.IsToolPersistentlyApproved(CheckToolInput{
		ServerName:        server.Name,
		ServerIdentity:    server.Identity,
		ToolName:          toolName,
		RequestedAutonomy: autonomy,
	})
	return err == nil && approved
}
