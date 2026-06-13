package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/zerocommands"
)

func (m model) toolsText() string {
	registered := m.registry.All()
	if len(registered) == 0 {
		return renderCommandOutput(commandOutput{
			Title:  "Tools",
			Status: commandStatusWarning,
			Sections: []commandSection{{
				Title: "Registry",
				Lines: []string{"registered tools: 0"},
			}},
		})
	}

	names := make([]string, 0, len(registered))
	for _, tool := range registered {
		names = append(names, commandBullet(tool.Name()))
	}
	sort.Strings(names)
	return renderCommandOutput(commandOutput{
		Title:  "Tools",
		Status: commandStatusOK,
		Sections: []commandSection{
			{
				Title: "Registry",
				Lines: []string{fmt.Sprintf("registered tools: %d", len(names))},
			},
			{
				Title: "Available",
				Lines: names,
			},
		},
	})
}

func (m *model) mcpText() string {
	width := 0
	if m.width > 0 {
		width = chatWidth(m.width)
	}
	return renderMCPView(m.mcpViewState(), width)
}

func (m *model) refreshMCPViewState() {
	m.mcpViewStateCache = BuildMCPViewState(MCPStateOptions{
		Config:          m.mcpConfig,
		Registry:        m.registry,
		PermissionStore: m.mcpPermissionStore,
		PermissionMode:  string(m.permissionMode),
		TokenStore:      m.mcpTokenStore,
	})
	m.mcpViewStateReady = true
}

func (m *model) mcpViewState() MCPViewState {
	if m.mcpViewStateReady {
		return m.mcpViewStateCache
	}
	// Older tests may construct a zero-value model; keep that path useful, while
	// production refreshes the cache before any MCP view can render.
	m.refreshMCPViewState()
	return m.mcpViewStateCache
}

func (m model) startMCPTranscriptCommand(args string) (model, tea.Cmd) {
	args = strings.TrimSpace(args)
	if args == "" {
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "mcp", text: m.mcpText()})
		return m, nil
	}
	parsedArgs, err := splitMCPCommandArgs(args)
	if err != nil {
		text := strings.Join([]string{
			"MCP action failed",
			err.Error(),
			"",
			m.mcpText(),
		}, "\n")
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "mcp", text: text})
		return m, nil
	}
	return m.startMCPCommand(mcpCommandRequest{origin: mcpCommandOriginTranscript, raw: args, args: parsedArgs})
}

func (m model) startMCPCommand(request mcpCommandRequest) (model, tea.Cmd) {
	if m.mcpCommand == nil {
		result := MCPCommandResult{
			ExitCode: 1,
			Error:    "MCP action unavailable",
			Config:   m.mcpConfig,
		}
		return m.applyMCPCommandResultMessage(mcpCommandResultMsg{request: request, result: result}), nil
	}
	m.cancelMCPCommand()
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	m.mcpCommandSeq++
	request.id = m.mcpCommandSeq
	request.args = append([]string{}, request.args...)
	m.mcpCommandCancel = cancel
	runner := m.mcpCommand
	return m, func() tea.Msg {
		return mcpCommandResultMsg{
			request: request,
			result:  runner(ctx, request.args),
		}
	}
}

func (m *model) cancelMCPCommand() {
	if m.mcpCommandCancel != nil {
		m.mcpCommandCancel()
		m.mcpCommandCancel = nil
		m.mcpCommandSeq++
	}
}

func (m model) applyMCPCommandResultMessage(msg mcpCommandResultMsg) model {
	if msg.request.id != 0 && msg.request.id != m.mcpCommandSeq {
		return m
	}
	m.mcpCommandCancel = nil
	switch msg.request.origin {
	case mcpCommandOriginManager:
		text := ""
		m, text = m.applyMCPCommandResult(strings.Join(msg.request.args, " "), msg.result)
		m.mcpManager = &mcpManagerState{selected: msg.request.managerSelected, query: msg.request.managerQuery}
		if items := m.mcpManagerItems(); len(items) > 0 {
			m.mcpManager.selected = clampInt(m.mcpManager.selected, 0, len(items)-1)
		}
		if text != "" {
			m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "mcp", text: text})
		}
	case mcpCommandOriginWizard:
		m = m.applyMCPAddWizardSaveResult(msg.result, msg.request.wizardDisabled)
	default:
		text := ""
		m, text = m.applyMCPCommandResult(msg.request.raw, msg.result)
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "mcp", text: text})
	}
	return m
}

func (m model) applyMCPCommandResult(args string, result MCPCommandResult) (model, string) {
	if result.ExitCode != 0 || strings.TrimSpace(result.Error) != "" {
		message := strings.TrimSpace(result.Error)
		if message == "" {
			message = strings.TrimSpace(result.Output)
		}
		if message == "" {
			message = "MCP command failed"
		}
		return m, strings.Join([]string{
			"MCP action failed",
			message,
			"",
			m.mcpText(),
		}, "\n")
	}
	if len(result.Config.Servers) > 0 || len(m.mcpConfig.Servers) > 0 {
		m.mcpConfig = result.Config
		m.refreshMCPViewState()
	}
	output := strings.TrimSpace(result.Output)
	if output == "" {
		output = "zero mcp " + args
	}
	return m, strings.Join([]string{
		"MCP action complete",
		output,
		"",
		m.mcpText(),
	}, "\n")
}

func splitMCPCommandArgs(args string) ([]string, error) {
	args = strings.TrimSpace(args)
	if args == "" {
		return nil, nil
	}
	out := []string{}
	var current strings.Builder
	var quote rune
	hasToken := false
	runes := []rune(args)
	for index := 0; index < len(runes); index++ {
		r := runes[index]
		if quote != 0 {
			if r == quote {
				quote = 0
				hasToken = true
				continue
			}
			if r == '\\' && index+1 < len(runes) && runes[index+1] == quote {
				index++
				current.WriteRune(runes[index])
				hasToken = true
				continue
			}
			current.WriteRune(r)
			hasToken = true
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
			hasToken = true
		case r == '\\' && index+1 < len(runes) && (runes[index+1] == '\'' || runes[index+1] == '"' || strings.TrimSpace(string(runes[index+1])) == ""):
			index++
			current.WriteRune(runes[index])
			hasToken = true
		case strings.TrimSpace(string(r)) == "":
			if hasToken {
				out = append(out, current.String())
				current.Reset()
				hasToken = false
			}
		default:
			current.WriteRune(r)
			hasToken = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in MCP command")
	}
	if hasToken {
		out = append(out, current.String())
	}
	return out, nil
}

func (m model) permissionsText() string {
	stateLines := []string{
		"Permission mode: " + string(m.permissionMode),
	}
	if m.sandboxStore == nil {
		return renderCommandOutput(commandOutput{
			Title:  "Permissions",
			Status: commandStatusWarning,
			Sections: []commandSection{
				{Title: "State", Lines: stateLines},
				{Title: "Sandbox grants:", Lines: []string{"persistent grants: unavailable"}},
			},
		})
	}

	grants, err := m.sandboxStore.List()
	if err != nil {
		return renderCommandOutput(commandOutput{
			Title:  "Permissions",
			Status: commandStatusBlocked,
			Sections: []commandSection{
				{Title: "State", Lines: stateLines},
				{Title: "Sandbox grants:", Lines: []string{"error: " + err.Error()}},
			},
		})
	}
	snapshots := zerocommands.SandboxGrantSnapshots(grants)
	grantLines := []string{fmt.Sprintf("persistent grants: %d", len(snapshots))}
	if len(snapshots) == 0 {
		grantLines = append(grantLines, "none")
	} else {
		for _, grant := range snapshots {
			line := fmt.Sprintf("%s [%s/%s]", grant.ToolName, grant.Decision, grant.MaxAutonomy)
			if grant.ApprovedAt != "" {
				line += " approved " + grant.ApprovedAt
			}
			if grant.Reason != "" {
				line += " - " + grant.Reason
			}
			grantLines = append(grantLines, commandBullet(line))
		}
	}
	return renderCommandOutput(commandOutput{
		Title:  "Permissions",
		Status: commandStatusOK,
		Sections: []commandSection{
			{Title: "State", Lines: stateLines},
			{Title: "Sandbox grants:", Lines: grantLines},
		},
	})
}

func (m model) providerText() string {
	profileLines := []string{
		"provider: " + displayValue(m.providerName, "none"),
		"model: " + displayValue(m.modelName, "none"),
	}
	if !config.HasProviderProfile(m.providerProfile) {
		profileLines = append(profileLines, "profile: not configured")
		return renderCommandOutput(commandOutput{
			Title:  "Provider",
			Status: commandStatusWarning,
			Sections: []commandSection{
				{Title: "Active", Lines: profileLines},
				{Title: "Next actions", Lines: []string{
					"zero providers catalog",
					"zero providers setup openai --set-active",
					"zero providers add openai --api-key-env OPENAI_API_KEY --set-active",
				}},
			},
		})
	}

	snapshot := zerocommands.ProviderSnapshotFromProfile(m.providerProfile, true)
	profileLines = append(profileLines,
		"active: "+boolText(snapshot.Active),
		"kind: "+displayValue(snapshot.ProviderKind, "unknown"),
		"api model: "+displayValue(snapshot.APIModel, "unknown"),
		"base url: "+displayValue(snapshot.BaseURL, "default"),
		"api key: "+apiKeyState(snapshot.APIKeySet),
	)
	if snapshot.Message != "" {
		profileLines = append(profileLines, "provider status: "+snapshot.Status+" - "+snapshot.Message)
	}

	status := commandStatusOK
	actionLines := providerNextActionLines(m.providerProfile, snapshot, m.providerName)
	if providerCredentialRequired(m.providerProfile, snapshot.ProviderKind) && !providerProfileHasCredential(m.providerProfile) {
		status = commandStatusWarning
	}
	return renderCommandOutput(commandOutput{
		Title:  "Provider",
		Status: status,
		Sections: []commandSection{
			{Title: "Active", Lines: profileLines},
			{Title: "Next actions", Lines: actionLines},
		},
	})
}

func providerNextActionLines(profile config.ProviderProfile, snapshot zerocommands.ProviderSnapshot, activeName string) []string {
	providerName := firstProviderDisplayValue(snapshot.Name, activeName, profile.Name, providerSetupCatalogID(profile, snapshot.ProviderKind), "openai")
	setupID := providerSetupCatalogID(profile, snapshot.ProviderKind)
	lines := []string{}
	if providerCredentialRequired(profile, snapshot.ProviderKind) && !providerProfileHasCredential(profile) {
		if envName := providerCredentialEnvName(profile, snapshot.ProviderKind); envName != "" {
			lines = append(lines,
				"set "+envName+" in your environment",
				"zero providers add "+setupID+" --api-key-env "+envName+" --set-active",
			)
		} else {
			lines = append(lines, "set provider credentials in your environment")
		}
	}
	return append(lines,
		"zero providers check "+providerName+" --connectivity",
		"zero providers catalog",
		"zero providers setup "+setupID+" --set-active",
	)
}

func providerProfileHasCredential(profile config.ProviderProfile) bool {
	return strings.TrimSpace(profile.APIKey) != "" || strings.TrimSpace(profile.AuthHeaderValue) != ""
}

func providerCredentialRequired(profile config.ProviderProfile, providerKind string) bool {
	if descriptor, ok := providerCatalogDescriptor(profile); ok {
		return descriptor.RequiresAuth
	}
	switch config.ProviderKind(strings.TrimSpace(providerKind)) {
	case config.ProviderKindOpenAI, config.ProviderKindOpenAICompatible, config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat, config.ProviderKindGoogle:
		return true
	default:
		return false
	}
}

func providerCredentialEnvName(profile config.ProviderProfile, providerKind string) string {
	if envName := strings.TrimSpace(profile.APIKeyEnv); envName != "" {
		return envName
	}
	if descriptor, ok := providerCatalogDescriptor(profile); ok && len(descriptor.AuthEnvVars) > 0 {
		return descriptor.AuthEnvVars[0]
	}
	switch config.ProviderKind(strings.TrimSpace(providerKind)) {
	case config.ProviderKindOpenAI, config.ProviderKindOpenAICompatible:
		return "OPENAI_API_KEY"
	case config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat:
		return "ANTHROPIC_API_KEY"
	case config.ProviderKindGoogle:
		return "GEMINI_API_KEY"
	default:
		return ""
	}
}

func providerSetupCatalogID(profile config.ProviderProfile, providerKind string) string {
	if catalogID := strings.TrimSpace(profile.CatalogID); catalogID != "" {
		return catalogID
	}
	switch config.ProviderKind(strings.TrimSpace(providerKind)) {
	case config.ProviderKindOpenAI:
		return "openai"
	case config.ProviderKindAnthropic:
		return "anthropic"
	case config.ProviderKindGoogle:
		return "google"
	case config.ProviderKindOpenAICompatible:
		return "custom-openai-compatible"
	case config.ProviderKindAnthropicCompat:
		return "custom-anthropic-compatible"
	default:
		return firstProviderDisplayValue(profile.Name, "openai")
	}
}

func providerCatalogDescriptor(profile config.ProviderProfile) (providercatalog.Descriptor, bool) {
	catalogID := strings.TrimSpace(profile.CatalogID)
	if catalogID == "" {
		return providercatalog.Descriptor{}, false
	}
	descriptor, err := providercatalog.Require(catalogID)
	if err != nil {
		return providercatalog.Descriptor{}, false
	}
	return descriptor, true
}

func firstProviderDisplayValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func (m model) modelText(args string) string {
	return renderCommandOutput(commandOutput{
		Title:  "Model",
		Status: commandStatusOK,
		Sections: []commandSection{{
			Title: "Active",
			Lines: []string{
				"model: " + displayValue(m.modelName, "none"),
				"provider: " + displayValue(m.providerName, "none"),
				"effort: " + m.effortDisplay(),
			},
		}},
		Hints: []string{"use /model list to inspect models or /model <id> to switch this TUI session"},
	})
}

func (m model) contextText() string {
	toolCount := len(m.registry.All())
	return renderCommandOutput(commandOutput{
		Title:  "Context",
		Status: commandStatusOK,
		Sections: []commandSection{
			{
				Title: "Runtime",
				Lines: []string{
					"cwd: " + displayValue(m.cwd, "unknown"),
					"provider: " + displayValue(m.providerName, "none"),
					"model: " + displayValue(m.modelName, "none"),
					"permission mode: " + string(m.permissionMode),
					"effort: " + m.effortDisplay(),
					"style: " + m.responseStyle,
					"usage: " + m.usageSummaryText(),
					fmt.Sprintf("max turns: %d", m.agentOptions.MaxTurns),
				},
			},
			{
				Title: "Session",
				Lines: []string{
					"active session: " + displayValue(m.activeSession.SessionID, "none"),
					"session root: " + displayValue(m.sessionStore.RootDir, "unknown"),
					"compaction: " + m.compactionStatus(),
				},
			},
			{
				Title: "Tools",
				Lines: []string{
					fmt.Sprintf("registered tools: %d", toolCount),
				},
			},
		},
	})
}

func (m model) configText() string {
	return renderCommandOutput(commandOutput{
		Title:  "Config",
		Status: commandStatusOK,
		Sections: []commandSection{
			{
				Title: "Runtime",
				Lines: []string{
					"runtime: go",
					fmt.Sprintf("max turns: %d", m.agentOptions.MaxTurns),
					"permission mode: " + string(m.permissionMode),
				},
			},
			{
				Title: "Provider",
				Lines: []string{
					"provider: " + displayValue(m.providerName, "none"),
					"model: " + displayValue(m.modelName, "none"),
					"api key: " + apiKeyState(strings.TrimSpace(m.providerProfile.APIKey) != ""),
				},
			},
		},
	})
}

func (m model) debugText() string {
	state := "idle"
	if m.pending {
		state = "running"
	}
	return renderCommandOutput(commandOutput{
		Title:  "Debug",
		Status: commandStatusInfo,
		Sections: []commandSection{{
			Title: "Runtime",
			Lines: []string{
				"run state: " + state,
				"active run: " + fmt.Sprint(m.activeRunID),
				"pending permission: " + boolText(m.pendingPermission != nil),
			},
		}},
	})
}
