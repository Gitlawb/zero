package tui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

var (
	headerStyle = lipgloss.NewStyle().Bold(true)
	footerStyle = lipgloss.NewStyle().Faint(true)
)

type model struct {
	ctx            context.Context
	cwd            string
	providerName   string
	modelName      string
	provider       zeroruntime.Provider
	registry       *tools.Registry
	agentOptions   agent.Options
	permissionMode agent.PermissionMode
	transcript     []transcriptRow
	input          textinput.Model
	pending        bool
	exiting        bool
	runCancel      context.CancelFunc
	runID          int
	activeRunID    int
}

type agentResponseMsg struct {
	runID int
	rows  []transcriptRow
	err   error
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

	permissionMode := options.PermissionMode
	if permissionMode == "" {
		permissionMode = options.AgentOptions.PermissionMode
	}
	if permissionMode == "" {
		permissionMode = agent.PermissionModeAuto
	}

	input := textinput.New()
	input.Prompt = "zero > "
	input.Placeholder = "type a prompt or /help"
	input.Focus()

	return model{
		ctx:            ctx,
		cwd:            cwd,
		providerName:   options.ProviderName,
		modelName:      options.ModelName,
		provider:       options.Provider,
		registry:       registry,
		agentOptions:   options.AgentOptions,
		permissionMode: permissionMode,
		transcript:     initialTranscript(),
		input:          input,
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
	case agentResponseMsg:
		if msg.runID != m.activeRunID {
			return m, nil
		}
		m.pending = false
		m.runCancel = nil
		m.activeRunID = 0
		for _, row := range msg.rows {
			m.transcript = appendRow(m.transcript, row.kind, row.text)
		}
		if msg.err != nil {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{
				kind: actionAppendError,
				text: msg.err.Error(),
			})
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() string {
	var builder strings.Builder

	builder.WriteString(headerStyle.Render(fmt.Sprintf("ZERO  %s  %s", m.cwd, m.providerStatus())))
	builder.WriteString("\n\n")

	for _, row := range m.transcript {
		builder.WriteString(renderRow(row))
		builder.WriteString("\n")
	}

	if m.pending {
		builder.WriteString("assistant: working...\n")
	}

	builder.WriteString("\n")
	builder.WriteString(m.input.View())
	builder.WriteString("\n\n")
	builder.WriteString(footerStyle.Render(commandFooterText()))

	return builder.String()
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
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: helpText()})
		return m, nil
	case commandClear:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionClear})
		return m, nil
	case commandExit:
		m.exiting = true
		return m, tea.Quit
	case commandTools:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.toolsText()})
		return m, nil
	case commandPermissions:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.permissionsText()})
		return m, nil
	case commandProvider:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.providerText()})
		return m, nil
	case commandModel:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.modelText(command.text)})
		return m, nil
	case commandContext:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.contextText()})
		return m, nil
	case commandConfig:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.configText()})
		return m, nil
	case commandDebug:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.debugText()})
		return m, nil
	case commandPlan:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.planText()})
		return m, nil
	case commandDoctor, commandSearch, commandTheme, commandInputStyle:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: shellOnlyCommandText(command.name),
		})
		return m, nil
	case commandUnknown:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendError,
			text: "unknown command: " + command.text,
		})
		return m, nil
	case commandPrompt:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: command.text})
		if m.provider == nil {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{
				kind: actionAppendAssistant,
				text: "No provider configured.",
			})
			return m, nil
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
	m.pending = false
	m.runCancel = nil
	m.activeRunID = 0
}

func (m model) runAgent(runID int, runCtx context.Context, prompt string) tea.Cmd {
	return func() tea.Msg {
		rows := []transcriptRow{}
		options := m.agentOptions
		options.Registry = m.registry
		options.PermissionMode = m.permissionMode

		onToolCall := options.OnToolCall
		options.OnToolCall = func(call agent.ToolCall) {
			rows = append(rows, transcriptRow{kind: rowToolCall, text: "tool call: " + call.Name})
			if onToolCall != nil {
				onToolCall(call)
			}
		}

		onToolResult := options.OnToolResult
		options.OnToolResult = func(result agent.ToolResult) {
			rows = append(rows, transcriptRow{
				kind: rowToolResult,
				text: toolResultRowText(result),
			})
			if onToolResult != nil {
				onToolResult(result)
			}
		}

		result, err := agent.Run(runCtx, prompt, m.provider, options)
		if err != nil {
			return agentResponseMsg{runID: runID, rows: rows, err: err}
		}
		rows = append(rows, transcriptRow{kind: rowAssistant, text: result.FinalAnswer})
		return agentResponseMsg{runID: runID, rows: rows}
	}
}

func toolResultRowText(result agent.ToolResult) string {
	status := result.Status
	if status == "" {
		status = tools.StatusOK
	}
	return fmt.Sprintf("tool result: %s %s %s", result.Name, status, result.Output)
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
	return "Permission mode: " + string(m.permissionMode)
}

func (m model) providerText() string {
	return strings.Join([]string{
		"Provider",
		"provider: " + displayValue(m.providerName, "none"),
		"model: " + displayValue(m.modelName, "none"),
	}, "\n")
}

func (m model) modelText(args string) string {
	switch strings.ToLower(strings.TrimSpace(args)) {
	case "list", "ls":
		return m.modelListText()
	}

	lines := []string{
		"Model",
		"Active model: " + displayValue(m.modelName, "none"),
		"provider: " + displayValue(m.providerName, "none"),
	}
	if args != "" {
		lines = append(lines, "Model switching is not wired in Go TUI yet.")
	} else {
		lines = append(lines, "Use /model list to inspect the current model shell.")
	}
	return strings.Join(lines, "\n")
}

func (m model) contextText() string {
	return strings.Join([]string{
		"Context",
		"cwd: " + displayValue(m.cwd, "unknown"),
		"provider: " + displayValue(m.providerName, "none"),
		"model: " + displayValue(m.modelName, "none"),
		"permission mode: " + string(m.permissionMode),
		fmt.Sprintf("tools: %d", len(m.registry.All())),
	}, "\n")
}

func (m model) configText() string {
	return strings.Join([]string{
		"Config",
		"provider: " + displayValue(m.providerName, "none"),
		"model: " + displayValue(m.modelName, "none"),
		"permission mode: " + string(m.permissionMode),
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

func displayValue(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func shellOnlyCommandText(name string) string {
	return fmt.Sprintf("%s is registered in the Go TUI shell but is not wired yet.", name)
}

func helpText() string {
	return "Commands:\n" + strings.Join(formatCommandHelpLines(), "\n") + "\nSubmit text to ask the assistant."
}

const defaultCommandFooterText = "/help  /model  /provider  /context  /tools  /permissions  /clear  /exit  Esc clear  Ctrl+C quit"

func commandFooterText() string {
	return formatCommandFooterText(commandDefinitions)
}

func formatCommandFooterText(commands []commandDefinition) string {
	if len(commands) == 0 {
		return defaultCommandFooterText
	}

	namesByKind := make(map[commandKind]string, len(commands))
	for _, command := range commands {
		namesByKind[command.kind] = command.name
	}

	featured := []commandKind{
		commandHelp,
		commandModel,
		commandProvider,
		commandContext,
		commandTools,
		commandPermissions,
		commandClear,
		commandExit,
	}
	parts := make([]string, 0, len(featured)+2)
	for _, kind := range featured {
		name := namesByKind[kind]
		if name != "" {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return defaultCommandFooterText
	}

	parts = append(parts, "Esc clear", "Ctrl+C quit")
	return strings.Join(parts, "  ")
}

func renderRow(row transcriptRow) string {
	switch row.kind {
	case rowWelcome:
		return row.text
	case rowUser:
		return "user: " + row.text
	case rowAssistant:
		return "assistant: " + row.text
	case rowToolCall:
		return row.text
	case rowToolResult:
		return row.text
	case rowError:
		return "error: " + row.text
	default:
		return row.text
	}
}
