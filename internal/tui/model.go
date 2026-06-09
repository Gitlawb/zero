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
	"github.com/Gitlawb/zero/internal/notify"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/usage"
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
	notifier           *notify.Notifier
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
	// flushRunIDs holds the ids of runs cancelled while still in flight. Each
	// cancelled agent goroutine keeps running to completion and returns its
	// accumulated sessionEvents (including EventSessionCheckpoint payloads captured
	// before each mutating tool) in a final agentResponseMsg. activeRunID is
	// already zeroed by then, so without this the message would be dropped and the
	// checkpoint blobs already written to disk would be orphaned (breaking
	// /rewind). It is a SET (not a single id) so a second cancel before the first
	// goroutine returns doesn't overwrite/lose the first run's pending flush. The
	// agentResponseMsg handler persists each such run's session events (only) so
	// the checkpoints stay referenced, then removes the id.
	flushRunIDs       map[int]struct{}
	pendingPermission *pendingPermissionPrompt
	pendingAskUser    *pendingAskUserPrompt
	pendingSpecReview *pendingSpecReviewPrompt
	width             int
	height            int
	now               func() time.Time

	skin             string // "" default shell, "zeroline" reskin
	themeVariant     int    // zeroline color theme (0-4)
	themeDark        bool   // zeroline light/dark
	frame            int    // animation frame counter (zeroline spinner)
	booted           bool   // zeroline boot splash finished
	streamingText    string // live assistant text for the current segment
	streamStartFrame int    // frame the current stream segment began (tok/s)

	// Slash-command autocomplete (purely additive UI state). suggestions is the
	// live match list for the current "/token"; suggestionIdx is the highlighted
	// row. Active only when suggestionsActive() (no modal, non-empty matches).
	suggestions   []commandSuggestion
	suggestionIdx int
	// suggestionsAreFiles is true when the overlay is showing "@file" matches
	// rather than "/command" matches, so completion inserts a path token instead
	// of replacing the whole input.
	suggestionsAreFiles bool

	// picker, when non-nil, is an open interactive selector overlay (/model,
	// /theme, /effort, /mode with no argument). It captures ↑/↓/Enter/Esc and
	// applies the chosen value through the existing command handlers.
	picker *commandPicker

	// pendingImages holds image attachments staged by /image for the next user
	// turn; pendingImageLabels are their display names (base(path)) for the chip
	// row. Both are cleared after a prompt is submitted (or /image clear). nil =
	// no attachments = today's text-only behavior exactly.
	pendingImages      []zeroruntime.ImageBlock
	pendingImageLabels []string

	// captureRunImages, when set, is invoked with the images a run is launched
	// with. Nil in production; used by tests to assert image threading without a
	// real provider round-trip.
	captureRunImages func([]zeroruntime.ImageBlock)
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
	specReview    *pendingSpecReviewPrompt
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

type pendingSpecReviewPrompt struct {
	SpecID         string
	SpecTitle      string
	SpecFilePath   string
	RelativePath   string
	DraftSessionID string
}

type tuiAgentRunOptions struct {
	registry       *tools.Registry
	permissionMode agent.PermissionMode
	systemPrompt   string
	specDraft      bool
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
	if options.Skin == "zeroline" {
		input.Prompt = "❯ "
		input.Placeholder = "message zero — / commands · @ files · ! bash"
	}
	input.Focus()

	notifier := notify.New(os.Stderr, notify.Config{
		Mode:      notify.Mode(strings.TrimSpace(options.Notify.Mode)),
		FocusMode: notify.FocusMode(strings.TrimSpace(options.Notify.FocusMode)),
	})
	notifier.SetFocused(true)

	return model{
		skin:               options.Skin,
		themeVariant:       options.ThemeVariant,
		themeDark:          options.ThemeDark,
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
		notifier:           notifier,
	}
}

func (m model) Init() tea.Cmd {
	if m.skin == "zeroline" {
		return tea.Batch(textinput.Blink, zerolineTick())
	}
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			// cancelRun records the in-flight run into flushRunIDs and writes the
			// "Run cancelled." marker, exactly like the Esc path. If a run was still
			// in flight we must NOT quit yet: the cancelled goroutine returns its
			// accumulated session events (including the EventSessionCheckpoint blobs it
			// already wrote to disk before each mutating tool) in a final
			// agentResponseMsg, and quitting now would drop that message, orphaning the
			// checkpoints and breaking /rewind for Ctrl+C'd runs. Defer the quit until
			// that flush lands; the agentResponseMsg handler fires tea.Quit once
			// flushRunIDs drains. With no run in flight there is nothing to flush, so
			// quit immediately as before.
			pendingFlush := false
			if m.pending && m.activeRunID != 0 {
				pendingFlush = true
			}
			m.cancelRun()
			m.exiting = true
			if pendingFlush && len(m.flushRunIDs) > 0 {
				return m, nil
			}
			return m, tea.Quit
		case tea.KeyEsc:
			// An active questionnaire is cancelled (not the whole run): deliver
			// whatever answers were collected so the agent loop unblocks and
			// degrades to its best-assumption path.
			if m.pendingAskUser != nil {
				return m.resolveAskUser(true)
			}
			if m.pendingSpecReview != nil {
				return m.cancelSpecReview()
			}
			// An open picker cancels first; then an active suggestion overlay is
			// dismissed. Neither cancels the run or clears the input.
			if m.picker != nil {
				m.picker = nil
				return m, nil
			}
			if m.suggestionsActive() {
				return m.dismissSuggestions(), nil
			}
			m.input.SetValue("")
			m.suggestions = nil
			m.suggestionIdx = 0
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
			if m.pendingSpecReview != nil {
				return m, nil
			}
			if m.picker != nil {
				return m.choosePicker()
			}
			// Enter on a highlighted suggestion completes the input rather than
			// submitting; Enter with no active suggestion submits as today.
			if m.suggestionsActive() {
				return m.completeSuggestion(), nil
			}
			return m.handleSubmit()
		case tea.KeyShiftTab:
			// shift+tab toggles the permission mode between Auto and Ask (Unsafe
			// is intentionally not reachable by a casual keypress — see
			// nextPermissionMode), but only when nothing modal is up: a permission
			// prompt, ask_user questionnaire, or open picker all take precedence
			// and let the key fall through to their own handlers below.
			if m.pendingPermission == nil && m.pendingAskUser == nil && m.pendingSpecReview == nil && m.picker == nil {
				m.permissionMode = nextPermissionMode(m.permissionMode)
				return m, nil
			}
		case tea.KeyTab:
			if m.picker == nil && m.suggestionsActive() {
				m.moveSuggestion(1)
				return m, nil
			}
		case tea.KeyDown:
			if m.picker != nil {
				m.picker.move(1)
				return m, nil
			}
			if m.suggestionsActive() {
				m.moveSuggestion(1)
				return m, nil
			}
		case tea.KeyUp:
			if m.picker != nil {
				m.picker.move(-1)
				return m, nil
			}
			if m.suggestionsActive() {
				m.moveSuggestion(-1)
				return m, nil
			}
		}
		if m.pendingAskUser != nil {
			// While a questionnaire is active, all other keys feed the text input
			// (the answer field); nothing else should react.
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		if m.pendingSpecReview != nil {
			return m.handleSpecReviewKey(msg)
		}
		if m.pendingPermission != nil {
			return m.handlePermissionKey(msg)
		}
		// An open picker is modal over the input: swallow remaining keys so they
		// neither type into the field nor trigger zeroline theme shortcuts.
		// ↑/↓/Enter/Esc were already handled above.
		if m.picker != nil {
			return m, nil
		}
		if m.skin == "zeroline" {
			m.booted = true // any key dismisses the boot splash
			if nm, handled := m.handleZerolineKeys(msg); handled {
				return nm, nil
			}
		}
		// The key fell through to the text input: let it update, then refresh the
		// autocomplete match list from the new value.
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.recomputeSuggestions()
		return m, cmd
	case tea.FocusMsg:
		if m.notifier != nil {
			m.notifier.SetFocused(true)
		}
		return m, nil
	case tea.BlurMsg:
		if m.notifier != nil {
			m.notifier.SetFocused(false)
		}
		return m, nil
	case zerolineTickMsg:
		if m.skin != "zeroline" {
			return m, nil
		}
		m.frame++
		if m.frame >= bootFrames {
			m.booted = true
		}
		return m, zerolineTick()
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
		// A request with no questions has nothing to answer — resolve it
		// immediately so the run isn't stalled waiting on manual input. Mirror the
		// normal flow: record the (empty) request in the transcript and answer with
		// an empty slice (not nil) so downstream sees the same Answers shape.
		if len(msg.request.Questions) == 0 {
			m.transcript = appendTranscriptRow(m.transcript, askUserTranscriptRow(msg.request))
			if msg.answer != nil {
				msg.answer([]string{})
			}
			return m, nil
		}
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
			// A run cancelled while in flight still finishes in its goroutine and
			// returns its accumulated session events here. Persist ONLY those events
			// (notably the EventSessionCheckpoint payloads captured before each
			// mutating tool) so the checkpoint blobs stay referenced and /rewind
			// works; the cancel path already wrote the "Run cancelled." marker, so
			// skip transcript rows, the trailing cancellation error, and any pending
			// state changes.
			if _, flushing := m.flushRunIDs[msg.runID]; flushing {
				delete(m.flushRunIDs, msg.runID)
				// appendSessionEvents only returns rows for persist FAILURES; surface
				// them so a failed checkpoint/tool flush (which would silently degrade
				// /rewind) is visible rather than swallowed.
				var flushRows []transcriptRow
				m, flushRows = m.appendSessionEvents(flushableSessionEvents(msg.sessionEvents))
				for _, row := range flushRows {
					m.transcript = appendTranscriptRow(m.transcript, row)
				}
				// A Ctrl+C during an in-flight run defers its quit until the run's
				// checkpoint session events have been flushed (above). Now that the
				// last pending flush is drained, fire the deferred quit.
				if m.exiting && len(m.flushRunIDs) == 0 {
					return m, tea.Quit
				}
			}
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
		if msg.specReview != nil {
			m = m.activateSpecReview(*msg.specReview)
		}
		if m.notifier != nil {
			m.notifier.Notify(notify.Completion, notify.DefaultMessage(notify.Completion))
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
	case bashResultMsg:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: msg.output})
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.skin == "zeroline" {
		return m.zerolineView()
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
	if m.pendingSpecReview != nil {
		builder.WriteString("\n")
		builder.WriteString(renderFocusedSpecReviewPrompt(*m.pendingSpecReview, width))
		builder.WriteString("\n")
	}

	builder.WriteString("\n")
	if chips := renderImageChips(m.pendingImageLabels); chips != "" {
		builder.WriteString(zeroTheme.muted.Render(chips))
		builder.WriteString("\n")
	}
	builder.WriteString(borderedBlock(width, []string{m.input.View()}))
	if overlay := m.suggestionOverlay(width); overlay != "" {
		builder.WriteString("\n")
		builder.WriteString(overlay)
	}
	if picker := m.pickerOverlay(width); picker != "" {
		builder.WriteString("\n")
		builder.WriteString(picker)
	}
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

// choosePicker applies the highlighted picker item through the same handler the
// typed command would have used, appends the resulting status text, and closes
// the picker. Behavior is identical to running "/model <id>", "/effort <v>",
// "/mode <name>", or selecting a zeroline theme by key.
func (m model) choosePicker() (tea.Model, tea.Cmd) {
	picker := m.picker
	m.picker = nil
	if picker == nil {
		return m, nil
	}
	item, ok := picker.current()
	if !ok {
		return m, nil
	}
	switch picker.kind {
	case pickerModel:
		m.showSplash = false
		text := ""
		m, text = m.handleModelCommand(item.Value)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
	case pickerEffort:
		m.showSplash = false
		text := ""
		m, text = m.handleEffortCommand(item.Value)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
	case pickerMode:
		m.showSplash = false
		text := ""
		m, text = m.handleModeCommand(item.Value)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
	case pickerTheme:
		// Theme selection mirrors the zeroline number-key shortcut: set the active
		// variant by its catalog index.
		m.themeVariant = picker.selected
	}
	return m, nil
}

func (m model) handleSubmit() (tea.Model, tea.Cmd) {
	command := parseCommand(m.input.Value())
	if command.kind == commandPrompt && m.pending {
		return m, nil
	}
	m.input.SetValue("")
	m.suggestions = nil
	m.suggestionIdx = 0

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
		if strings.TrimSpace(command.text) == "" {
			if m.pending {
				m.showSplash = false
				m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: pickerBusyText(command.name)})
				return m, nil
			}
			if picker := m.newModelPicker(); picker != nil {
				m.picker = picker
				return m, nil
			}
		}
		m.showSplash = false
		text := ""
		m, text = m.handleModelCommand(command.text)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		return m, nil
	case commandMode:
		if strings.TrimSpace(command.text) == "" {
			if m.pending {
				m.showSplash = false
				m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: pickerBusyText(command.name)})
				return m, nil
			}
			if picker := m.newModePicker(); picker != nil {
				m.picker = picker
				return m, nil
			}
		}
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
	case commandSpec:
		m.showSplash = false
		return m.handleSpecCommand(command.text)
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
		if strings.TrimSpace(command.text) == "" {
			if m.pending {
				m.showSplash = false
				m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: pickerBusyText(command.name)})
				return m, nil
			}
			if picker := m.newEffortPicker(); picker != nil {
				m.picker = picker
				return m, nil
			}
		}
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
	case commandTheme:
		// Only the zeroline skin renders themes; there a no-argument /theme opens
		// the picker. The default skin keeps its existing shell-only message.
		if m.skin == "zeroline" && strings.TrimSpace(command.text) == "" {
			if m.pending {
				m.showSplash = false
				m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: pickerBusyText(command.name)})
				return m, nil
			}
			if picker := m.newThemePicker(); picker != nil {
				m.picker = picker
				return m, nil
			}
		}
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: shellOnlyCommandText(command.name),
		})
		return m, nil
	case commandInputStyle:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: shellOnlyCommandText(command.name),
		})
		return m, nil
	case commandImage:
		m.showSplash = false
		m = m.handleImageCommand(command.text)
		return m, nil
	case commandUnknown:
		m.showSplash = false
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendError,
			text: "unknown command: " + command.text,
		})
		return m, nil
	case commandBash:
		m.showSplash = false
		cmdText := strings.TrimSpace(command.text)
		if cmdText == "" {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "Usage: !<shell command>"})
			return m, nil
		}
		// A "!cmd" shell escape runs OUTSIDE the agent sandbox, so gate it behind
		// the explicit unsafe permission mode. In auto/ask mode it is not executed;
		// the user is told how to enable it. This keeps a sandbox-bypassing exec
		// from running without a deliberate safety posture.
		if m.permissionMode != agent.PermissionModeUnsafe {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{
				kind: actionAppendSystem,
				text: "Shell escape (!) is disabled in " + string(m.permissionMode) + " mode — it bypasses the sandbox. Relaunch with --skip-permissions-unsafe to run shell commands directly.",
			})
			return m, nil
		}
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: "$ " + cmdText})
		return m, runBashEscape(m.cwd, cmdText)
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
		// Re-check vision support against the CURRENT effective model at submit
		// time, not just at /image attach time: the user may have attached on a
		// vision model and then /model-switched to a non-vision one. If the active
		// model can't accept images, drop them (with an inline notice mirroring
		// exec's drop+warn wording) rather than sending them to a model that
		// rejects them. Pending state is cleared either way below.
		turnImages := m.pendingImages
		if len(turnImages) > 0 && !modelSupportsVisionTUI(m.modelName) {
			name := m.modelName
			if name == "" {
				name = "the active model"
			}
			m.transcript = reduceTranscript(m.transcript, transcriptAction{
				kind: actionAppendSystem,
				text: fmt.Sprintf("Model %s does not support image input; ignoring %d image(s).", name, len(turnImages)),
			})
			turnImages = nil
		}
		m.pendingImages = nil
		m.pendingImageLabels = nil
		runCtx, cancel := context.WithCancel(m.ctx)
		m.runID++
		m.activeRunID = m.runID
		m.runCancel = cancel
		m.pending = true
		return m, m.runAgent(m.activeRunID, runCtx, command.text, turnImages)
	default:
		return m, nil
	}
}

func (m *model) cancelRun() {
	if m.runCancel != nil {
		m.runCancel()
	}
	// Remember the in-flight run so its final agentResponseMsg is still drained
	// for session-event persistence after activeRunID is cleared — otherwise the
	// checkpoint blobs it captured before each mutating tool are orphaned on disk
	// and /rewind can't reference them.
	if m.pending && m.activeRunID != 0 {
		if m.flushRunIDs == nil {
			m.flushRunIDs = make(map[int]struct{})
		}
		m.flushRunIDs[m.activeRunID] = struct{}{}
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

func (m model) runAgent(runID int, runCtx context.Context, prompt string, images []zeroruntime.ImageBlock) tea.Cmd {
	return m.runAgentWithOptions(runID, runCtx, prompt, images, tuiAgentRunOptions{})
}

func (m model) runAgentWithOptions(runID int, runCtx context.Context, prompt string, images []zeroruntime.ImageBlock, runOptions tuiAgentRunOptions) tea.Cmd {
	return func() tea.Msg {
		rows := []transcriptRow{}
		usageEvents := []zeroruntime.Usage{}
		sessionEvents := []pendingSessionEvent{}
		usageModelID := m.modelName
		var specReview *pendingSpecReviewPrompt
		options := m.agentOptions
		options.Registry = m.registry
		if runOptions.registry != nil {
			options.Registry = runOptions.registry
		}
		options.PermissionMode = m.permissionMode
		if runOptions.permissionMode != "" {
			options.PermissionMode = runOptions.permissionMode
		}
		if runOptions.systemPrompt != "" {
			options.SystemPrompt = runOptions.systemPrompt
		}
		options.SessionID = m.activeSession.SessionID
		options.Model = m.modelName
		options.ReasoningEffort = string(m.reasoningEffort)
		options.Cwd = m.cwd
		options.Images = images
		if m.captureRunImages != nil {
			m.captureRunImages(images)
		}
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
			if m.notifier != nil {
				m.notifier.Notify(notify.AwaitingInput, notify.DefaultMessage(notify.AwaitingInput))
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
			// Only notify when there is actually something to answer — a request
			// with no questions auto-resolves without ever prompting the user.
			if m.notifier != nil && len(request.Questions) > 0 {
				m.notifier.Notify(notify.AwaitingInput, notify.DefaultMessage(notify.AwaitingInput))
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
			// mutation runs), then batch the checkpoint event IN ORDER with the other
			// session events so the recorded sequence matches execution (recording it
			// out-of-band would reorder it ahead of the batched tool_call/result).
			// SnapshotForCheckpoint writes the blobs; the batched event referencing
			// them is flushed at end-of-run AND on cancel (flushRunIDs), so the blobs
			// never stay orphaned — see its contract in internal/sessions.
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
			if runOptions.specDraft {
				if info, ok := tuiSpecReviewFromToolResult(result, m.activeSession.SessionID); ok {
					specReview = &info
				}
			}
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
			if len(result.Meta) > 0 {
				toolPayload["meta"] = result.Meta
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
		if runOptions.specDraft {
			if result.StopReason != agent.StopReasonSpecReviewRequired || specReview == nil || specReview.SpecID == "" || specReview.SpecFilePath == "" {
				err := fmt.Errorf("spec draft ended without submit_spec")
				sessionEvents = append(sessionEvents, pendingSessionEvent{
					Type:    sessions.EventError,
					Payload: map[string]any{"message": err.Error()},
				})
				return agentResponseMsg{runID: runID, rows: rows, usageEvents: usageEvents, usageModelID: usageModelID, sessionEvents: sessionEvents, err: err}
			}
			return agentResponseMsg{runID: runID, rows: rows, usageEvents: usageEvents, usageModelID: usageModelID, sessionEvents: sessionEvents, specReview: specReview}
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
