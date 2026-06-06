package tui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/usage"
	"github.com/Gitlawb/zero/internal/zerocommands"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const tuiToolOutputLimit = 240
const defaultResponseStyle = "balanced"

type model struct {
	ctx                context.Context
	cwd                string
	gitBranch          string
	providerName       string
	modelName          string
	providerProfile    config.ProviderProfile
	provider           zeroruntime.Provider
	newProvider        func(config.ProviderProfile) (zeroruntime.Provider, error)
	registry           *tools.Registry
	sessionStore       *sessions.Store
	sandboxStore       *sandbox.GrantStore
	activeSession      sessions.Metadata
	sessionEvents      []sessions.Event
	usageTracker       *usage.Tracker
	runtimeMessageSink func(tea.Msg)
	agentOptions       agent.Options
	permissionMode     agent.PermissionMode
	reasoningEffort    modelregistry.ReasoningEffort
	responseStyle      string
	compactRequests    int
	unpricedRequests   int
	unpricedTokens     int
	transcript         []transcriptRow
	input              textinput.Model
	showSplash         bool
	pending            bool
	exiting            bool
	runCancel          context.CancelFunc
	runID              int
	activeRunID        int
	width              int
	height             int
	now                func() time.Time
}

type agentResponseMsg struct {
	runID         int
	rows          []transcriptRow
	usageEvents   []zeroruntime.Usage
	usageModelID  string
	sessionEvents []pendingSessionEvent
	err           error
}

type agentRowMsg struct {
	runID int
	row   transcriptRow
}

func newModel(ctx context.Context, options Options) model {
	if ctx == nil {
		ctx = context.Background()
	}

	cwd := options.Cwd
	if cwd == "" {
		if current, err := os.Getwd(); err == nil {
			cwd = current
		}
	}

	registry := options.Registry
	if registry == nil {
		registry = options.AgentOptions.Registry
	}
	if registry == nil {
		registry = tools.NewRegistry()
	}
	sessionStore := options.SessionStore
	if sessionStore == nil {
		sessionStore = sessions.NewStore(sessions.StoreOptions{})
	}
	sandboxStore := options.SandboxStore
	usageTracker := options.UsageTracker
	if usageTracker == nil {
		usageTracker = usage.NewTracker(usage.TrackerOptions{})
	}

	permissionMode := options.PermissionMode
	if permissionMode == "" {
		permissionMode = options.AgentOptions.PermissionMode
	}
	if permissionMode == "" {
		permissionMode = agent.PermissionModeAuto
	}

	input := textinput.New()
	input.Prompt = "zero > "
	input.Placeholder = "Ask Zero to inspect, edit, explain, or run a command..."
	input.Focus()

	return model{
		ctx:                ctx,
		cwd:                cwd,
		gitBranch:          gitBranch(cwd),
		providerName:       options.ProviderName,
		modelName:          options.ModelName,
		providerProfile:    options.ProviderProfile,
		provider:           options.Provider,
		newProvider:        options.NewProvider,
		registry:           registry,
		sessionStore:       sessionStore,
		sandboxStore:       sandboxStore,
		agentOptions:       options.AgentOptions,
		runtimeMessageSink: options.RuntimeMessageSink,
		permissionMode:     permissionMode,
		reasoningEffort:    options.ReasoningEffort,
		responseStyle:      defaultedResponseStyle(options.ResponseStyle),
		usageTracker:       usageTracker,
		transcript:         initialTranscript(),
		input:              input,
		showSplash:         true,
		now:                time.Now,
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			m.cancelRun()
			m.exiting = true
			return m, tea.Quit
		case tea.KeyEsc:
			m.input.SetValue("")
			if m.pending {
				m.cancelRun()
			}
			return m, nil
		case tea.KeyEnter:
			return m.handleSubmit()
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case agentResponseMsg:
		if msg.runID != m.activeRunID {
			return m, nil
		}
		m.pending = false
		m.runCancel = nil
		m.activeRunID = 0
		for _, event := range msg.usageEvents {
			var usageRows []transcriptRow
			m, usageRows = m.recordUsageEvent(msg.usageModelID, event)
			for _, row := range usageRows {
				m.transcript = appendTranscriptRow(m.transcript, row)
			}
		}
		var sessionRows []transcriptRow
		m, sessionRows = m.appendSessionEvents(msg.sessionEvents)
		for _, row := range sessionRows {
			m.transcript = appendTranscriptRow(m.transcript, row)
		}
		for _, row := range msg.rows {
			m.transcript = appendTranscriptRow(m.transcript, row)
		}
		if msg.err != nil {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{
				kind: actionAppendError,
				text: msg.err.Error(),
			})
		}
		return m, nil
	case agentRowMsg:
		if msg.runID != m.activeRunID {
			return m, nil
		}
		m.transcript = appendTranscriptRow(m.transcript, msg.row)
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.showSplash {
		return m.startupView()
	}
	return m.transcriptView()
}

func (m model) transcriptView() string {
	width := normalizedStartupWidth(m.width)

	var builder strings.Builder
	builder.WriteString(m.headerBar(width))
	builder.WriteString("\n\n")

	for index, row := range m.transcript {
		if index > 0 && startsTurn(row.kind) {
			builder.WriteString("\n")
		}
		builder.WriteString(renderRow(row, width))
		builder.WriteString("\n")
	}

	if m.pending {
		builder.WriteString("\n")
		builder.WriteString(zeroTheme.zero.Render("◇ zero") + "  " + zeroTheme.muted.Render("working…"))
		builder.WriteString("\n")
	}

	builder.WriteString("\n")
	builder.WriteString(borderedBlock(width, []string{m.input.View()}))
	builder.WriteString("\n")
	builder.WriteString(m.statusLine(width))

	return builder.String()
}

// startsTurn reports whether a row begins a new conversational turn and therefore
// gets a blank line of separation above it (tool rows stay grouped together).
func startsTurn(kind rowKind) bool {
	switch kind {
	case rowUser, rowAssistant, rowSystem, rowError:
		return true
	default:
		return false
	}
}

func (m model) handleSubmit() (tea.Model, tea.Cmd) {
	command := parseCommand(m.input.Value())
	if command.kind == commandPrompt && m.pending {
		return m, nil
	}
	m.input.SetValue("")

	switch command.kind {
	case commandEmpty:
		return m, nil
	case commandHelp:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: helpText()})
		return m, nil
	case commandClear:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionClear})
		m.showSplash = true
		return m, nil
	case commandExit:
		m.exiting = true
		return m, tea.Quit
	case commandTools:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.toolsText()})
		return m, nil
	case commandPermissions:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.permissionsText()})
		return m, nil
	case commandProvider:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.providerText()})
		return m, nil
	case commandModel:
		m.showSplash = false
		text := ""
		m, text = m.handleModelCommand(command.text)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		return m, nil
	case commandContext:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.contextText()})
		return m, nil
	case commandConfig:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.configText()})
		return m, nil
	case commandDebug:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.debugText()})
		return m, nil
	case commandPlan:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.planText()})
		return m, nil
	case commandDoctor:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.doctorText()})
		return m, nil
	case commandSearch:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.searchText(command.text)})
		return m, nil
	case commandResume:
		m.showSplash = false
		if m.pending {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{
				kind: actionAppendError,
				text: "Cannot resume sessions while a run is active.",
			})
			return m, nil
		}
		text := ""
		m, text = m.handleResumeCommand(command.text)
		if text != "" {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		}
		return m, nil
	case commandCompact:
		m.showSplash = false
		text := ""
		m, text = m.handleCompactCommand(command.text)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		return m, nil
	case commandEffort:
		m.showSplash = false
		text := ""
		m, text = m.handleEffortCommand(command.text)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		return m, nil
	case commandStyle:
		m.showSplash = false
		text := ""
		m, text = m.handleStyleCommand(command.text)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		return m, nil
	case commandTheme, commandInputStyle:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: shellOnlyCommandText(command.name),
		})
		return m, nil
	case commandUnknown:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendError,
			text: "unknown command: " + command.text,
		})
		return m, nil
	case commandPrompt:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: command.text})
		if m.provider == nil {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{
				kind: actionAppendAssistant,
				text: "No provider configured.",
			})
			return m, nil
		}
		var err error
		m, err = m.ensureActiveSession(command.text)
		if err != nil {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{
				kind: actionAppendError,
				text: "session create error: " + err.Error(),
			})
		} else {
			agentPrompt := m.sessionPrompt(command.text)
			m, err = m.appendSessionEvent(sessions.EventMessage, map[string]any{
				"role":    "user",
				"content": command.text,
			})
			if err != nil {
				m.transcript = reduceTranscript(m.transcript, transcriptAction{
					kind: actionAppendError,
					text: "session record error: " + err.Error(),
				})
			}
			command.text = agentPrompt
		}
		runCtx, cancel := context.WithCancel(m.ctx)
		m.runID++
		m.activeRunID = m.runID
		m.runCancel = cancel
		m.pending = true
		return m, m.runAgent(m.activeRunID, runCtx, command.text)
	default:
		return m, nil
	}
}

func (m *model) cancelRun() {
	if m.runCancel != nil {
		m.runCancel()
	}
	if m.pending && m.activeSession.SessionID != "" {
		if next, err := (*m).appendSessionEvent(sessions.EventError, map[string]any{
			"message": "Run cancelled.",
		}); err == nil {
			*m = next
		}
	}
	m.pending = false
	m.runCancel = nil
	m.activeRunID = 0
}

func (m model) runAgent(runID int, runCtx context.Context, prompt string) tea.Cmd {
	return func() tea.Msg {
		rows := []transcriptRow{}
		usageEvents := []zeroruntime.Usage{}
		sessionEvents := []pendingSessionEvent{}
		usageModelID := m.modelName
		options := m.agentOptions
		options.Registry = m.registry
		options.PermissionMode = m.permissionMode

		onToolCall := options.OnToolCall
		options.OnToolCall = func(call agent.ToolCall) {
			row := transcriptRow{
				kind:   rowToolCall,
				id:     call.ID,
				text:   "tool call: " + call.Name,
				tool:   call.Name,
				detail: argHint(call.Arguments),
			}
			rows = append(rows, row)
			m.sendAgentRow(runID, row)
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type: sessions.EventToolCall,
				Payload: map[string]any{
					"id":        call.ID,
					"name":      call.Name,
					"arguments": call.Arguments,
				},
			})
			if onToolCall != nil {
				onToolCall(call)
			}
		}

		onToolResult := options.OnToolResult
		options.OnToolResult = func(result agent.ToolResult) {
			row := transcriptRow{
				kind:   rowToolResult,
				id:     result.ToolCallID,
				text:   toolResultRowText(result),
				tool:   result.Name,
				status: result.Status,
				detail: result.Output,
			}
			rows = append(rows, row)
			m.sendAgentRow(runID, row)
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type: sessions.EventToolResult,
				Payload: map[string]any{
					"toolCallId": result.ToolCallID,
					"name":       result.Name,
					"status":     string(result.Status),
					"output":     result.Output,
				},
			})
			if onToolResult != nil {
				onToolResult(result)
			}
		}

		onPermission := options.OnPermission
		options.OnPermission = func(event agent.PermissionEvent) {
			row := permissionTranscriptRow(event)
			rows = append(rows, row)
			m.sendAgentRow(runID, row)
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type:    sessions.EventPermission,
				Payload: event,
			})
			if onPermission != nil {
				onPermission(event)
			}
		}

		onUsage := options.OnUsage
		options.OnUsage = func(event zeroruntime.Usage) {
			usageEvents = append(usageEvents, event)
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type: sessions.EventUsage,
				Payload: map[string]any{
					"promptTokens":     event.EffectiveInputTokens(),
					"completionTokens": event.EffectiveOutputTokens(),
					"totalTokens":      event.TotalTokens(),
				},
			})
			if onUsage != nil {
				onUsage(event)
			}
		}

		result, err := agent.Run(runCtx, prompt, m.provider, options)
		if err != nil {
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type:    sessions.EventError,
				Payload: map[string]any{"message": err.Error()},
			})
			return agentResponseMsg{runID: runID, rows: rows, usageEvents: usageEvents, usageModelID: usageModelID, sessionEvents: sessionEvents, err: err}
		}
		rows = append(rows, transcriptRow{kind: rowAssistant, text: result.FinalAnswer})
		sessionEvents = append(sessionEvents, pendingSessionEvent{
			Type: sessions.EventMessage,
			Payload: map[string]any{
				"role":    "assistant",
				"content": result.FinalAnswer,
			},
		})
		return agentResponseMsg{runID: runID, rows: rows, usageEvents: usageEvents, usageModelID: usageModelID, sessionEvents: sessionEvents}
	}
}

func (m model) sendAgentRow(runID int, row transcriptRow) {
	if m.runtimeMessageSink == nil {
		return
	}
	m.runtimeMessageSink(agentRowMsg{runID: runID, row: row})
}

func toolResultRowText(result agent.ToolResult) string {
	status := result.Status
	if status == "" {
		status = tools.StatusOK
	}
	return fmt.Sprintf("tool result: %s %s %s", result.Name, status, truncateTUIOutput(result.Output, tuiToolOutputLimit))
}

func (m model) providerStatus() string {
	provider := m.providerName
	if provider == "" {
		provider = "provider:none"
	}

	if m.modelName == "" {
		return provider
	}
	return provider + "/" + m.modelName
}

func (m model) toolsText() string {
	registered := m.registry.All()
	if len(registered) == 0 {
		return "No tools registered."
	}

	names := make([]string, 0, len(registered))
	for _, tool := range registered {
		names = append(names, tool.Name())
	}
	sort.Strings(names)
	return "Tools: " + strings.Join(names, ", ")
}

func (m model) permissionsText() string {
	lines := []string{"Permission mode: " + string(m.permissionMode)}
	if m.sandboxStore == nil {
		lines = append(lines, "Sandbox grants: unavailable")
		return strings.Join(lines, "\n")
	}

	grants, err := m.sandboxStore.List()
	if err != nil {
		lines = append(lines, "Sandbox grants: error: "+err.Error())
		return strings.Join(lines, "\n")
	}
	snapshots := zerocommands.SandboxGrantSnapshots(grants)
	if len(snapshots) == 0 {
		lines = append(lines, "Sandbox grants: none")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "Sandbox grants:")
	for _, grant := range snapshots {
		line := fmt.Sprintf("- %s [%s/%s]", grant.ToolName, grant.Decision, grant.MaxAutonomy)
		if grant.ApprovedAt != "" {
			line += " approved " + grant.ApprovedAt
		}
		if grant.Reason != "" {
			line += " - " + grant.Reason
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func (m model) providerText() string {
	return strings.Join([]string{
		"Provider",
		"provider: " + displayValue(m.providerName, "none"),
		"model: " + displayValue(m.modelName, "none"),
	}, "\n")
}

func (m model) modelText(args string) string {
	lines := []string{
		"Model",
		"Active model: " + displayValue(m.modelName, "none"),
		"provider: " + displayValue(m.providerName, "none"),
	}
	lines = append(lines, "Use /model list to inspect models or /model <id> to switch this TUI session.")
	return strings.Join(lines, "\n")
}

func (m model) contextText() string {
	return strings.Join([]string{
		"Context",
		"cwd: " + displayValue(m.cwd, "unknown"),
		"provider: " + displayValue(m.providerName, "none"),
		"model: " + displayValue(m.modelName, "none"),
		"permission mode: " + string(m.permissionMode),
		"effort: " + m.effortDisplay(),
		"style: " + m.responseStyle,
		"usage: " + m.usageSummaryText(),
		"compaction: " + m.compactionStatus(),
		fmt.Sprintf("max turns: %d", m.agentOptions.MaxTurns),
		"active session: " + displayValue(m.activeSession.SessionID, "none"),
		"session root: " + displayValue(m.sessionStore.RootDir, "unknown"),
		fmt.Sprintf("tools: %d", len(m.registry.All())),
	}, "\n")
}

func (m model) configText() string {
	return strings.Join([]string{
		"Config",
		"provider: " + displayValue(m.providerName, "none"),
		"model: " + displayValue(m.modelName, "none"),
		fmt.Sprintf("max turns: %d", m.agentOptions.MaxTurns),
		"permission mode: " + string(m.permissionMode),
		"api key: " + apiKeyState(strings.TrimSpace(m.providerProfile.APIKey) != ""),
	}, "\n")
}

func (m model) debugText() string {
	state := "idle"
	if m.pending {
		state = "running"
	}
	return strings.Join([]string{
		"Debug",
		"run state: " + state,
		"active run: " + fmt.Sprint(m.activeRunID),
		"Debug mode is not wired in Go TUI yet.",
	}, "\n")
}
