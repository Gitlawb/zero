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
}

type agentResponseMsg struct {
	rows []transcriptRow
	err  error
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
			m.exiting = true
			return m, tea.Quit
		case tea.KeyEsc:
			m.input.SetValue("")
			m.pending = false
			return m, nil
		case tea.KeyEnter:
			return m.handleSubmit()
		}
	case agentResponseMsg:
		m.pending = false
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
	builder.WriteString(footerStyle.Render("/help  /clear  /exit  /tools  /permissions  Esc clear  Ctrl+C quit"))

	return builder.String()
}

func (m model) handleSubmit() (tea.Model, tea.Cmd) {
	command := parseCommand(m.input.Value())
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
		m.pending = true
		return m, m.runAgent(command.text)
	default:
		return m, nil
	}
}

func (m model) runAgent(prompt string) tea.Cmd {
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
				text: fmt.Sprintf("tool result: %s %s %s", result.Name, result.Status, result.Output),
			})
			if onToolResult != nil {
				onToolResult(result)
			}
		}

		result, err := agent.Run(m.ctx, prompt, m.provider, options)
		if err != nil {
			return agentResponseMsg{rows: rows, err: err}
		}
		rows = append(rows, transcriptRow{kind: rowAssistant, text: result.FinalAnswer})
		return agentResponseMsg{rows: rows}
	}
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

func helpText() string {
	return "Commands: /help, /clear, /exit, /tools, /permissions. Submit text to ask the assistant."
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
