package cli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/execution"
	"github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/tools"
)

type mcpToolListItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SideEffect  string `json:"sideEffect"`
	Permission  string `json:"permission"`
}

// registerMCPToolsForWorkspace resolves and registers the workspace's MCP servers.
// This spawns stdio servers, so it gates the project config layer behind the
// workspace-trust check: trustRoot is the ORIGINAL launch directory (resolved before
// any --worktree reassignment) so a worktree of a trusted repo inherits that trust.
// resolveTrust fails closed, so an empty trustRoot or a store-read error excludes the
// project layer and a cloned repo cannot spawn its ./.zero/config.json MCP servers.
//
// It returns a trustSkip alongside the runtime so the caller can fold the MCP gate
// into the one-line trust notice (mirroring the hooks and plugins chokepoints);
// otherwise a workspace whose only project config is MCP would be gated silently.
func registerMCPToolsForWorkspace(ctx context.Context, workspaceRoot string, registry *tools.Registry, deps appDeps, autonomy mcp.PermissionAutonomy, trustRoot string, runners ...*execution.Runner) (mcpToolRuntime, trustSkip, error) {
	excludeProject, trustCheckErrored := resolveTrust(trustRoot)
	skip := trustSkip{
		excludedProjectConfig: excludeProject && projectMCPConfigExists(workspaceRoot),
		trustCheckErrored:     trustCheckErrored,
	}
	cfg, err := deps.resolveMCPConfig(workspaceRoot, excludeProject)
	if err != nil {
		return nil, skip, err
	}
	if len(cfg.Servers) == 0 {
		return noopMCPRuntime{}, skip, nil
	}
	store, err := deps.newMCPStore()
	if err != nil {
		return nil, skip, err
	}
	var runner *execution.Runner
	if len(runners) > 0 {
		runner = runners[0]
	}
	runtime, err := deps.registerMCPTools(ctx, registry, cfg, mcp.RegisterOptions{
		PermissionStore: store,
		Autonomy:        autonomy,
		Execution:       runner,
		WorkspaceRoot:   workspaceRoot,
	})
	return runtime, skip, err
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

func formatMCPServerList(servers map[string]config.MCPServerConfig) string {
	if len(servers) == 0 {
		return "No MCP servers configured."
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	lines := []string{"MCP Servers:"}
	for _, name := range names {
		server := servers[name]
		state := "enabled"
		if server.Disabled {
			state = "disabled"
		}
		identity := strings.TrimSpace(server.Command)
		if identity == "" {
			identity = redactMCPURL(server.URL, "[REDACTED]")
		}
		lines = append(lines, fmt.Sprintf("  %s [%s] %s %s", name, server.Type, state, identity))
	}
	return strings.Join(lines, "\n")
}

func redactMCPURL(raw string, marker string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if parsed.User != nil {
		parsed.User = nil
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery = redactMCPRawQuery(parsed.RawQuery, marker)
	}
	if parsed.Fragment != "" {
		parsed.Fragment = redactMCPRawQuery(parsed.Fragment, marker)
	}
	out := parsed.String()
	if strings.TrimSpace(out) == "" {
		return raw
	}
	return out
}

func redactMCPRawQuery(rawQuery string, marker string) string {
	parts := strings.Split(rawQuery, "&")
	for index, part := range parts {
		if part == "" {
			continue
		}
		key, _, hasValue := strings.Cut(part, "=")
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			decodedKey = key
		}
		if !isSensitiveMCPDisplayKey(decodedKey) {
			continue
		}
		if hasValue {
			parts[index] = key + "=" + marker
		} else {
			parts[index] = key
		}
	}
	return strings.Join(parts, "&")
}

func isSensitiveMCPDisplayKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	key = strings.ReplaceAll(key, "-", "_")
	if key == "key" {
		return true
	}
	for _, token := range []string{"token", "secret", "password", "passwd", "api_key", "apikey", "access_key", "auth", "credential"} {
		if strings.Contains(key, token) {
			return true
		}
	}
	return false
}

// mcpStartupDisclosing is the optional interface a runtime implements when it
// can report what its launched server processes ran under. Optional rather than
// part of mcpToolRuntime so a runtime that launches nothing, and every test
// double, stays unchanged.
type mcpStartupDisclosing interface {
	StartupDisclosures() []mcp.StartupDisclosure
}

// mcpStartupStreaming is the push form: the runtime queues each disclosure as a
// typed event, including a launch that completes after registration returned,
// and this package drains it on the goroutine that owns stderr.
type mcpStartupStreaming interface {
	StartupDisclosureStream() *mcp.StartupDisclosureStream
}

// reportMCPStartupDisclosures states once what enforcement applied to the MCP
// server processes this run launched.
//
// A PUSH, NOT A SAMPLE. This used to read StartupDisclosures once, here, and a
// stdio attempt abandoned at the connect timeout could still be inside cmd.Start
// at that moment. The process then started under the reduced write confinement,
// the reaper closed its client, and nothing read the runtime again: the operator
// saw the skipped-server warning and never the disclosure.
//
// THIS GOROUTINE OWNS THE WRITER. The runtime queues typed disclosures; every
// write to stderr happens either on the caller's goroutine (the set already known
// when this returns, in server order, so startup output keeps its order) or on
// the single pump started here, never both at once and never after stop.
//
// The returned stop ends delivery and joins the pump, so no write to stderr can
// outlive the caller's ownership of it. The caller must run it before handing the
// terminal to anything else. A disclosure that arrives after stop is dropped: it
// is worth printing while someone owns the writer, and worth losing rather than
// writing into a screen that now belongs to Bubble Tea. Anything already queued
// when stop runs is still printed, on the caller's goroutine, with the pump
// already finished.
//
// The pull form is kept for a runtime that implements no stream, which today is
// only test doubles; it has no late launches to deliver, so its stop is a no-op.
func reportMCPStartupDisclosures(stderr io.Writer, runtime mcpToolRuntime) (stop func()) {
	print := func(disclosure mcp.StartupDisclosure) {
		for _, notice := range disclosure.Notices {
			fmt.Fprintf(stderr, "notice: MCP server %s started with reduced enforcement: %s\n", disclosure.Name, notice)
		}
	}
	printAll := func(disclosures []mcp.StartupDisclosure) {
		for _, disclosure := range disclosures {
			print(disclosure)
		}
	}
	streaming, ok := runtime.(mcpStartupStreaming)
	if !ok {
		if disclosing, ok := runtime.(mcpStartupDisclosing); ok {
			printAll(disclosing.StartupDisclosures())
		}
		return func() {}
	}
	stream := streaming.StartupDisclosureStream()
	if stream == nil {
		return func() {}
	}
	printAll(stream.Drain())
	pumped := make(chan struct{})
	go func() {
		defer close(pumped)
		for stream.Wait() {
			printAll(stream.Drain())
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			stream.Close()
			<-pumped
			printAll(stream.Drain())
		})
	}
}
