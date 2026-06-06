package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	"github.com/Gitlawb/zero/internal/zenline"
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
	pendingPermission  *pendingPermissionPrompt
	pendingAskUser     *pendingAskUserPrompt
	width              int
	height             int
	now                func() time.Time

	skin             string // "" default shell, "zenline" reskin
	themeVariant     int    // zenline color theme (0-4)
	themeDark        bool   // zenline light/dark
	frame            int    // animation frame counter (zenline spinner)
	booted           bool   // zenline boot splash finished
	streamingText    string // live assistant text for the current segment
	streamStartFrame int    // frame the current stream segment began (tok/s)
}

type agentTextMsg struct {
	runID int
	delta string
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

type permissionDecision = agent.PermissionDecisionAction

const (
	permissionDecisionAllow       permissionDecision = agent.PermissionDecisionAllow
	permissionDecisionDeny        permissionDecision = agent.PermissionDecisionDeny
	permissionDecisionAlwaysAllow permissionDecision = agent.PermissionDecisionAlwaysAllow
)

type permissionRequestMsg struct {
	runID   int
	request agent.PermissionRequest
	decide  func(agent.PermissionDecision)
}

type pendingPermissionPrompt struct {
	request agent.PermissionRequest
	decide  func(agent.PermissionDecision)
}

// askUserRequestMsg is the TUI-loop equivalent of permissionRequestMsg: the
// agent goroutine sends it (via the runtime sink) and blocks until the model
// hands answers back through the answer callback.
type askUserRequestMsg struct {
	runID   int
	request agent.AskUserRequest
	answer  func([]string)
}

// pendingAskUserPrompt tracks an in-progress questionnaire. Answers are collected
// one question at a time; once every question has an answer (or the user cancels)
// the answer callback is invoked exactly once.
type pendingAskUserPrompt struct {
	request agent.AskUserRequest
	answer  func([]string)
	index   int
	answers []string
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
	if options.Skin == "zenline" {
		input.Prompt = "❯ "
		input.Placeholder = "message zero — / commands · @ files · ! bash"
	}
	input.Focus()

	return model{
		skin:         options.Skin,
		themeVariant: options.ThemeVariant,
		themeDark:    options.ThemeDark,
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
	if m.skin == "zenline" {
		return tea.Batch(textinput.Blink, zenlineTick())
	}
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
			// An active questionnaire is cancelled (not the whole run): deliver
			// whatever answers were collected so the agent loop unblocks and
			// degrades to its best-assumption path.
			if m.pendingAskUser != nil {
				return m.resolveAskUser(true)
			}
			m.input.SetValue("")
			if m.pending {
				m.cancelRun()
			}
			return m, nil
		case tea.KeyEnter:
			if m.pendingPermission != nil {
				return m, nil
			}
			if m.pendingAskUser != nil {
				return m.submitAskUserAnswer()
			}
			return m.handleSubmit()
		}
		if m.pendingAskUser != nil {
			// While a questionnaire is active, all other keys feed the text input
			// (the answer field); nothing else should react.
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		if m.pendingPermission != nil {
			return m.handlePermissionKey(msg)
		}
		if m.skin == "zenline" {
			m.booted = true // any key dismisses the boot splash
			if nm, handled := m.handleZenlineKeys(msg); handled {
				return nm, nil
			}
		}
	case zenlineTickMsg:
		if m.skin != "zenline" {
			return m, nil
		}
		m.frame++
		if m.frame >= bootFrames {
			m.booted = true
		}
		return m, zenlineTick()
	case agentTextMsg:
		if msg.runID != m.activeRunID {
			return m, nil
		}
		if m.streamingText == "" {
			m.streamStartFrame = m.frame
		}
		m.streamingText += msg.delta
		m.showSplash = false
		return m, nil
	case tea.MouseMsg:
		if m.skin == "zenline" && m.pendingPermission != nil &&
			msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			switch zenline.PermLayout(m.width, m.height).Hit(msg.X, msg.Y) {
			case "allow":
				return m.resolvePermission(permissionDecisionAllow)
			case "always":
				return m.resolvePermission(permissionDecisionAlwaysAllow)
			case "deny":
				return m.resolvePermission(permissionDecisionDeny)
			}
		}
		return m, nil
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case permissionRequestMsg:
		if msg.runID != m.activeRunID {
			return m, nil
		}
		m.showSplash = false
		m.transcript = appendTranscriptRow(m.transcript, permissionTranscriptRow(permissionEventFromRequest(msg.request)))
		if msg.request.Action == agent.PermissionActionPrompt {
			m.pendingPermission = &pendingPermissionPrompt{
				request: msg.request,
				decide:  msg.decide,
			}
		}
		return m, nil
	case askUserRequestMsg:
		if msg.runID != m.activeRunID {
			return m, nil
		}
		m.showSplash = false
		m.transcript = appendTranscriptRow(m.transcript, askUserTranscriptRow(msg.request))
		m.pendingAskUser = &pendingAskUserPrompt{
			request: msg.request,
			answer:  msg.answer,
			answers: make([]string, 0, len(msg.request.Questions)),
		}
		m.input.SetValue("")
		return m, nil
	case agentResponseMsg:
		if msg.runID != m.activeRunID {
			return m, nil
		}
		m.pending = false
		m.runCancel = nil
		m.activeRunID = 0
		m.pendingPermission = nil
		m.pendingAskUser = nil
		m.streamingText = ""
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
		// a tool call ends the current streamed text segment
		if msg.row.kind == rowToolCall {
			m.streamingText = ""
		}
		m.transcript = appendTranscriptRow(m.transcript, msg.row)
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.skin == "zenline" {
		return m.zenlineView()
	}
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
		switch {
		case m.pendingPermission != nil:
			builder.WriteString(renderFocusedPermissionPrompt(m.pendingPermission.request, width))
		case m.pendingAskUser != nil:
			builder.WriteString(renderFocusedAskUserPrompt(*m.pendingAskUser, m.input.Value(), width))
		default:
			builder.WriteString(zeroTheme.zero.Render("◇ zero") + "  " + zeroTheme.muted.Render("working…"))
		}
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

func (m model) handlePermissionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch strings.ToLower(msg.String()) {
	case "a":
		return m.resolvePermission(permissionDecisionAllow)
	case "d":
		return m.resolvePermission(permissionDecisionDeny)
	case "y":
		return m.resolvePermission(permissionDecisionAlwaysAllow)
	default:
		return m, nil
	}
}

func (m model) resolvePermission(decision permissionDecision) (tea.Model, tea.Cmd) {
	pending := m.pendingPermission
	if pending == nil {
		return m, nil
	}

	if pending.decide != nil {
		pending.decide(agent.PermissionDecision{
			Action: decision,
			Reason: permissionDecisionReason(decision),
		})
	}
	m.pendingPermission = nil
	return m, nil
}

// submitAskUserAnswer records the answer to the current question and advances to
// the next one; once every question is answered it delivers the full answer set.
func (m model) submitAskUserAnswer() (tea.Model, tea.Cmd) {
	pending := m.pendingAskUser
	if pending == nil {
		return m, nil
	}
	pending.answers = append(pending.answers, strings.TrimSpace(m.input.Value()))
	pending.index++
	m.input.SetValue("")
	if pending.index >= len(pending.request.Questions) {
		return m.resolveAskUser(false)
	}
	return m, nil
}

// resolveAskUser delivers the collected answers (padding to one-per-question when
// cancelled early) and clears the prompt. cancelled answers stay empty so the
// loop can degrade to its best-assumption path without deadlocking.
func (m model) resolveAskUser(cancelled bool) (tea.Model, tea.Cmd) {
	pending := m.pendingAskUser
	if pending == nil {
		return m, nil
	}
	answers := pending.answers
	if cancelled {
		// Record the question currently on screen as unanswered too.
		m.input.SetValue("")
	}
	for len(answers) < len(pending.request.Questions) {
		answers = append(answers, "")
	}
	if pending.answer != nil {
		pending.answer(answers)
	}
	m.pendingAskUser = nil
	return m, nil
}

func permissionDecisionReason(decision permissionDecision) string {
	switch decision {
	case permissionDecisionAllow:
		return "approved in TUI"
	case permissionDecisionAlwaysAllow:
		return "persistently approved in TUI"
	case permissionDecisionDeny:
		return "denied in TUI"
	default:
		return "denied in TUI"
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
	case commandMode:
		m.showSplash = false
		text := ""
		m, text = m.handleModeCommand(command.text)
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
	case commandRewind:
		m.showSplash = false
		text := ""
		m, text = m.handleRewindCommand(command.text)
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
	m.pendingPermission = nil
	m.pendingAskUser = nil
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
		// Enable agent-loop compaction sized to the active model's context
		// window. An unknown/custom model resolves to 0, leaving compaction off.
		options.ContextWindow = modelContextWindow(m.modelName)

		onText := options.OnText
		options.OnText = func(delta string) {
			m.sendAgentText(runID, delta)
			if onText != nil {
				onText(delta)
			}
		}

		onPermissionRequest := options.OnPermissionRequest
		options.OnPermissionRequest = func(ctx context.Context, request agent.PermissionRequest) (agent.PermissionDecision, error) {
			if onPermissionRequest != nil {
				return onPermissionRequest(ctx, request)
			}
			if m.runtimeMessageSink == nil {
				return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: "permission prompt unavailable"}, nil
			}
			decisionCh := make(chan agent.PermissionDecision, 1)
			m.sendPermissionRequest(runID, request, func(decision agent.PermissionDecision) {
				select {
				case decisionCh <- decision:
				default:
				}
			})
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type:    sessions.EventPermissionRequest,
				Payload: request,
			})
			select {
			case decision := <-decisionCh:
				if strings.TrimSpace(decision.Reason) == "" {
					decision.Reason = permissionDecisionReason(permissionDecision(decision.Action))
				}
				return decision, nil
			case <-ctx.Done():
				return agent.PermissionDecision{Action: agent.PermissionDecisionDeny, Reason: ctx.Err().Error()}, ctx.Err()
			}
		}

		onAskUser := options.OnAskUser
		options.OnAskUser = func(ctx context.Context, request agent.AskUserRequest) (agent.AskUserResponse, error) {
			if onAskUser != nil {
				return onAskUser(ctx, request)
			}
			if m.runtimeMessageSink == nil {
				// No interactive surface: let the loop degrade gracefully.
				return agent.AskUserResponse{}, fmt.Errorf("ask_user prompt unavailable")
			}
			answerCh := make(chan []string, 1)
			m.sendAskUserRequest(runID, request, func(answers []string) {
				select {
				case answerCh <- answers:
				default:
				}
			})
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type:    sessions.EventMessage,
				Payload: askUserSessionPayload(request),
			})
			select {
			case answers := <-answerCh:
				return agent.AskUserResponse{Answers: answers}, nil
			case <-ctx.Done():
				return agent.AskUserResponse{}, ctx.Err()
			}
		}

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
			// Snapshot before-state of files this call will mutate, NOW (before the
			// mutation runs), then batch the checkpoint event with the rest.
			if m.sessionStore != nil && m.activeSession.SessionID != "" {
				var args map[string]any
				if call.Arguments != "" {
					_ = json.Unmarshal([]byte(call.Arguments), &args)
				}
				if targets := tools.MutationTargets(m.cwd, call.Name, args); len(targets) > 0 {
					if payload, ok := m.sessionStore.SnapshotForCheckpoint(m.activeSession.SessionID, m.cwd, call.Name, targets); ok {
						sessionEvents = append(sessionEvents, pendingSessionEvent{
							Type:    sessions.EventSessionCheckpoint,
							Payload: payload,
						})
					}
				}
			}
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
			toolPayload := map[string]any{
				"toolCallId": result.ToolCallID,
				"name":       result.Name,
				"status":     string(result.Status),
				"output":     result.Output,
			}
			if result.Redacted {
				toolPayload["redacted"] = true
			}
			if len(result.ChangedFiles) > 0 {
				toolPayload["changedFiles"] = result.ChangedFiles
			}
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type:    sessions.EventToolResult,
				Payload: toolPayload,
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
				Type:    tuiPermissionEventType(event),
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

func (m model) sendPermissionRequest(runID int, request agent.PermissionRequest, decide func(agent.PermissionDecision)) {
	if m.runtimeMessageSink == nil {
		return
	}
	m.runtimeMessageSink(permissionRequestMsg{runID: runID, request: request, decide: decide})
}

func (m model) sendAskUserRequest(runID int, request agent.AskUserRequest, answer func([]string)) {
	if m.runtimeMessageSink == nil {
		return
	}
	m.runtimeMessageSink(askUserRequestMsg{runID: runID, request: request, answer: answer})
}

func tuiPermissionEventType(event agent.PermissionEvent) sessions.EventType {
	if event.Action == agent.PermissionActionPrompt {
		return sessions.EventPermissionRequest
	}
	if event.Action == agent.PermissionActionAllow || event.Action == agent.PermissionActionDeny {
		return sessions.EventPermissionDecision
	}
	return sessions.EventPermission
}

func (m model) sendAgentRow(runID int, row transcriptRow) {
	if m.runtimeMessageSink == nil {
		return
	}
	m.runtimeMessageSink(agentRowMsg{runID: runID, row: row})
}

func (m model) sendAgentText(runID int, delta string) {
	if m.runtimeMessageSink == nil {
		return
	}
	m.runtimeMessageSink(agentTextMsg{runID: runID, delta: delta})
}

func toolResultRowText(result agent.ToolResult) string {
	status := result.Status
	if status == "" {
		status = tools.StatusOK
	}
	return fmt.Sprintf("tool result: %s %s %s", result.Name, status, truncateTUIOutput(result.Output, tuiToolOutputLimit))
}
