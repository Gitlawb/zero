package cli

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/tools"
)

type mcpToolListItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SideEffect  string `json:"sideEffect"`
	Permission  string `json:"permission"`
}

func registerMCPToolsForWorkspace(ctx context.Context, workspaceRoot string, registry *tools.Registry, deps appDeps, autonomy mcp.PermissionAutonomy) (mcpToolRuntime, error) {
	cfg, err := deps.resolveMCPConfig(workspaceRoot)
	if err != nil {
		return nil, err
	}
	if len(cfg.Servers) == 0 {
		return noopMCPRuntime{}, nil
	}
	store, err := deps.newMCPStore()
	if err != nil {
		return nil, err
	}
	return deps.registerMCPTools(ctx, registry, cfg, mcp.RegisterOptions{
		PermissionStore: store,
		Autonomy:        autonomy,
	})
}

func execMCPAutonomy(options execOptions) mcp.PermissionAutonomy {
	if options.skipPermissionsUnsafe || strings.EqualFold(strings.TrimSpace(options.autonomy), "high") {
		return mcp.AutonomyHigh
	}
	if strings.EqualFold(strings.TrimSpace(options.autonomy), "medium") {
		return mcp.AutonomyMedium
	}
	return mcp.AutonomyLow
}

func mcpToolList(registry *tools.Registry) []mcpToolListItem {
	registered := registry.All()
	items := make([]mcpToolListItem, 0, len(registered))
	for _, tool := range registered {
		if !strings.HasPrefix(tool.Name(), "mcp_") {
			continue
		}
		safety := tool.Safety()
		items = append(items, mcpToolListItem{
			Name:        tool.Name(),
			Description: tool.Description(),
			SideEffect:  string(safety.SideEffect),
			Permission:  string(safety.Permission),
		})
	}
	sort.Slice(items, func(left int, right int) bool {
		return items[left].Name < items[right].Name
	})
	return items
}

func formatMCPToolList(items []mcpToolListItem) string {
	if len(items) == 0 {
		return "No MCP tools configured."
	}
	lines := []string{"MCP Tools:"}
	for _, item := range items {
		lines = append(lines, fmt.Sprintf("  %s [%s/%s] - %s", item.Name, item.SideEffect, item.Permission, item.Description))
	}
	return strings.Join(lines, "\n")
}
