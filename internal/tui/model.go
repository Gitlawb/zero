package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/doctor"
	"github.com/Gitlawb/zero/internal/lsp"
	internalmcp "github.com/Gitlawb/zero/internal/mcp"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/notify"
	"github.com/Gitlawb/zero/internal/providerhealth"
	"github.com/Gitlawb/zero/internal/providermodeldiscovery"
	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/tools"
	"github.com/Gitlawb/zero/internal/usage"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

const tuiToolOutputLimit = 240
const defaultResponseStyle = "balanced"
const chatWheelScrollLines = 5

type model struct {
	ctx                    context.Context
	cwd                    string
	userConfigPath         string
	doctorUserConfigPath   string
	projectConfigPath      string
	gitBranch              string
	providerName           string
	modelName              string
	providerProfile        config.ProviderProfile
	provider               zeroruntime.Provider
	newProvider            func(config.ProviderProfile) (zeroruntime.Provider, error)
	probeProviderHealth    func(context.Context, providerhealth.Options) providerhealth.Result
	discoverProviderModels func(context.Context, config.ProviderProfile) ([]providermodeldiscovery.Model, error)
	registry               *tools.Registry
	sessionStore           *sessions.Store
	sandboxStore           *sandbox.GrantStore
	mcpConfig              config.MCPConfig
	mcpPermissionStore     *internalmcp.PermissionStore
	mcpTokenStore          *internalmcp.TokenStore
	mcpCommand             func(context.Context, []string) MCPCommandResult
	mcpViewStateCache      MCPViewState
	mcpViewStateReady      bool
	mcpCommandSeq          int
	mcpCommandCancel       context.CancelFunc
	doctorCommandSeq       int
	doctorInFlight         bool
	doctorFrame            int
	activeSession          sessions.Metadata
	sessionEvents          []sessions.Event
	usageTracker           *usage.Tracker
	sessionCompactor       SessionCompactor
	prService              *PrService
	prState                PrState
	prWatcherStop          func()
	runtimeMessageSink     func(tea.Msg)
	agentOptions           agent.Options
	notifier               *notify.Notifier
	permissionMode         agent.PermissionMode
	selfCorrectTests       bool
	reasoningEffort        modelregistry.ReasoningEffort
	responseStyle          string
	userAgent              string
	compactRequests        int
	compactInFlight        bool
	compactFrame           int
	lastCompactResult      *CompactResult
	lastCompactError       string
	unpricedRequests       int
	unpricedTokens         int
	transcript             []transcriptRow
	transcriptDetailed     bool
	input                  textinput.Model
	composer               composerState
	composerActive         bool
	composerPastePreviews  []composerPastePreview
	altScreen              bool
	setup                  setupState
	setupSave              func(SetupSelection) (SetupResult, error)
	// spinner animates the running-tool glyph in card heads. Its tick is started
	// with each run and stops itself once pending clears (the TickMsg is simply
	// not forwarded), so an idle UI schedules no timers.
	spinner       spinner.Model
	pending       bool
	queuedMessage string
	exiting       bool
	runCancel     context.CancelFunc
	runID         int
	activeRunID   int
	// flushRunIDs holds the ids of runs cancelled while still in flight, mapped
	// to the session they were recording into AT CANCEL TIME. Each cancelled
	// agent goroutine keeps running to completion and returns its accumulated
	// sessionEvents (including EventSessionCheckpoint payloads captured before
	// each mutating tool) in a final agentResponseMsg. activeRunID is already
	// zeroed by then, so without this the message would be dropped and the
	// checkpoint blobs already written to disk would be orphaned (breaking
	// /rewind). It is a MAP (not a single id) so a second cancel before the
	// first goroutine returns doesn't overwrite/lose the first run's pending
	// flush; the recorded session id keeps the late flush out of whatever
	// session is active by then (e.g. after /resume), which would otherwise
	// contaminate the new session's log with the old run's events. The
	// agentResponseMsg handler persists each such run's session events (only) so
	// the checkpoints stay referenced, then removes the id.
	flushRunIDs       map[int]string
	pendingPermission *pendingPermissionPrompt
	pendingAskUser    *pendingAskUserPrompt
	pendingSpecReview *pendingSpecReviewPrompt
	width             int
	height            int
	now               func() time.Time
	chatScrollOffset  int
	// chatBodyLines is the live body's line count at the last update; used to pin
	// the viewport (hold the read position) when content streams in while the user
	// has scrolled up. 0 means "at the bottom / not pinned".
	chatBodyLines int

	// Flush-frontier state (see flush.go). In inline mode, transcript[:flushed]
	// is already in native scrollback; in alt-screen mode this frontier stays
	// idle so history cannot reveal prior shell output.
	// flushedAny gates the first turn-separator blank line; flushQueue/
	// printInFlight serialize ordered scrollback prints; headerPrinted records
	// the one-time title-bar print at startup.
	flushed       int
	flushedAny    bool
	flushQueue    []string
	printInFlight bool
	headerPrinted bool

	// Composer input history (shell-style ↑/↓ recall of submitted inputs).
	// historyIdx == len(inputHistory) means "not navigating"; historyDraft
	// preserves whatever was typed before recall started.
	inputHistory []string
	historyIdx   int
	historyDraft string

	streamingText              string // live assistant text for the current segment
	streamingReasoning         string // live provider reasoning for the current segment
	streamingReasoningExpanded bool

	// Slash-command autocomplete (purely additive UI state). suggestions is the
	// live match list for the current "/token"; suggestionIdx is the highlighted
	// row. commandPaletteOpen keeps a zero-match command search active so invalid
	// query text stays in the palette instead of leaking into the composer.
	// filePaletteOpen does the same for a trailing "@token" file search.
	suggestions        []commandSuggestion
	suggestionIdx      int
	commandPaletteOpen bool
	filePaletteOpen    bool
	// suggestionsAreFiles is true when the overlay is showing "@file" matches
	// rather than "/command" matches, so completion inserts a path token instead
	// of replacing the whole input.
	suggestionsAreFiles bool
	lastMouseSelection  mouseSelectionTarget
	mouseCapture        bool
	transcriptSelection transcriptSelectionState
	copyStatus          string
	copyStatusSeq       int

	// picker, when non-nil, is an open interactive selector overlay (/model,
	// /effort, /mode with no argument). It captures ↑/↓/Enter/Esc and applies
	// the chosen value through the existing command handlers.
	picker                       *commandPicker
	providerWizard               *providerWizardState
	mcpManager                   *mcpManagerState
	mcpAddWizard                 *mcpAddWizardState
	favoriteModels               map[string]bool
	modelPickerLoading           bool
	modelPickerLoadingProviderID string
	modelPickerLoadError         string
	modelPickerLiveProviderID    string
	modelPickerLiveModels        []providermodeldiscovery.Model

	// pendingImages holds image attachments staged by /image for the next user
	// turn; pendingImageLabels are their display names (base(path)) for the chip
	// row. Both are cleared after a prompt is submitted (or /image clear). nil =
	// no attachments = today's text-only behavior exactly.
	pendingImages      []zeroruntime.ImageBlock
	pendingImageLabels []string

	// pendingDocuments holds PDF text layers staged by /image for the next user
	// turn; the text is prepended to the prompt as a preamble at submit time and
	// the slice is cleared (or by /image clear). nil = no documents staged.
	pendingDocuments []pendingDocument

	// captureRunImages, when set, is invoked with the images a run is launched
	// with. Nil in production; used by tests to assert image threading without a
	// real provider round-trip.
	captureRunImages func([]zeroruntime.ImageBlock)
}

type agentTextMsg struct {
	runID int
	delta string
}

type agentReasoningMsg struct {
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
	// Turn metadata for settled rows that do not otherwise carry it.
	turnTools   int
	turnElapsed time.Duration
}

type agentRowMsg struct {
	runID int
	row   transcriptRow
}

type mcpCommandOrigin int

const (
	mcpCommandOriginTranscript mcpCommandOrigin = iota
	mcpCommandOriginManager
	mcpCommandOriginWizard
)

type mcpCommandRequest struct {
	id              int
	origin          mcpCommandOrigin
	args            []string
	raw             string
	managerSelected int
	managerQuery    string
	wizardDisabled  bool
}

type mcpCommandResultMsg struct {
	request mcpCommandRequest
	result  MCPCommandResult
}

type doctorCommandResultMsg struct {
	id   int
	text string
}

type prStateMsg struct {
	state PrState
}

type prWatcherStartedMsg struct {
	stop func()
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
	prService := options.PrService
	if prService == nil {
		prService = NewPrService(cwd)
	}
	doctorUserConfigPath := options.DoctorUserConfigPath
	if doctorUserConfigPath == "" {
		doctorUserConfigPath = options.UserConfigPath
	}

	permissionMode := options.PermissionMode
	if permissionMode == "" {
		permissionMode = options.AgentOptions.PermissionMode
	}
	if permissionMode == "" {
		permissionMode = agent.PermissionModeAuto
	}

	input := textinput.New()
	input.Prompt = "❯ "
	input.Placeholder = composerPlaceholder
	// Bubble's Ctrl+V binding reads the clipboard itself. Keep it disabled so
	// terminal bracketed paste (Paste: true) is the single paste path.
	input.KeyMap.Paste.SetEnabled(false)
	input.Focus()

	runSpinner := spinner.New(spinner.WithSpinner(spinner.MiniDot))

	notifier := notify.New(os.Stderr, notify.Config{
		Mode:      notify.Mode(strings.TrimSpace(options.Notify.Mode)),
		FocusMode: notify.FocusMode(strings.TrimSpace(options.Notify.FocusMode)),
	})
	// Opt-in webhook fan-out (ZERO_NOTIFY_WEBHOOK_URL). Delivery failures stay
	// silent here: the TUI owns the alt-screen, so writing to stderr would
	// corrupt the display.
	notify.MaybeAddWebhookSink(notifier, os.Getenv, nil)
	notifier.SetFocused(true)

	m := model{
		ctx:                    ctx,
		cwd:                    cwd,
		userConfigPath:         options.UserConfigPath,
		doctorUserConfigPath:   doctorUserConfigPath,
		projectConfigPath:      options.ProjectConfigPath,
		gitBranch:              gitBranch(cwd),
		providerName:           options.ProviderName,
		modelName:              options.ModelName,
		providerProfile:        options.ProviderProfile,
		favoriteModels:         favoriteModelSet(options.FavoriteModels),
		provider:               options.Provider,
		newProvider:            options.NewProvider,
		probeProviderHealth:    options.ProbeProviderHealth,
		discoverProviderModels: options.DiscoverProviderModels,
		registry:               registry,
		sessionStore:           sessionStore,
		sandboxStore:           sandboxStore,
		mcpConfig:              options.MCPConfig,
		mcpPermissionStore:     options.MCPPermissionStore,
		mcpTokenStore:          options.MCPTokenStore,
		mcpCommand:             options.MCPCommand,
		agentOptions:           options.AgentOptions,
		sessionCompactor:       options.SessionCompactor,
		runtimeMessageSink:     options.RuntimeMessageSink,
		permissionMode:         permissionMode,
		reasoningEffort:        options.ReasoningEffort,
		responseStyle:          defaultedResponseStyle(options.ResponseStyle),
		userAgent:              options.UserAgent,
		usageTracker:           usageTracker,
		transcript:             initialTranscript(),
		prService:              prService,
		prState:                prService.GetState(),
		input:                  input,
		spinner:                runSpinner,
		now:                    time.Now,
		notifier:               notifier,
		altScreen:              options.AltScreen,
		setup:                  newSetupState(options.Setup),
		setupSave:              options.Setup.Save,
	}
	m.refreshMCPViewState()
	return m
}

func (m model) doctorOptions(connectivity bool) doctor.Options {
	var health *providerhealth.Result
	if connectivity && m.probeProviderHealth != nil && config.HasProviderProfile(m.providerProfile) {
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		result := m.probeProviderHealth(ctx, providerhealth.Options{
			Profile:      m.providerProfile,
			Connectivity: true,
			UserAgent:    m.userAgent,
		})
		health = &result
	}

	return doctor.Options{
		Now:            m.now,
		Runtime:        "go",
		UserConfig:     m.doctorUserConfigPath,
		ProjectConfig:  m.projectConfigPath,
		Provider:       m.providerProfile,
		Connectivity:   connectivity,
		ProviderHealth: health,
	}
}

const (
	composerPlaceholder     = "describe a task for zero…"
	composerMaxVisibleLines = 4
)

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink}
	if m.prService != nil && m.runtimeMessageSink != nil {
		service := m.prService
		sink := m.runtimeMessageSink
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		cmds = append(cmds, func() tea.Msg {
			stop := WatchPRStateContext(ctx, service, func(state PrState) {
				sink(prStateMsg{state: state})
			})
			return prWatcherStartedMsg{stop: stop}
		})
	}
	return tea.Batch(cmds...)
}

func (m *model) stopPRWatcher() {
	if m.prWatcherStop == nil {
		return
	}
	m.prWatcherStop()
	m.prWatcherStop = nil
}

func (m model) quit() (tea.Model, tea.Cmd) {
	m.stopPRWatcher()
	return m, tea.Quit
}

// Update routes every message through updateModel, then advances the flush
// frontier for inline rendering. Alt-screen runs keep rows in the managed view
// instead of printing into terminal scrollback (see flush.go).
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(flushedMsg); ok {
		m.printInFlight = false
		return m.drainFlushQueue()
	}
	next, cmd := m.updateModel(msg)
	nm, ok := next.(model)
	if !ok {
		return next, cmd
	}
	nm = nm.syncChatScroll()
	nm, mouseCmd := nm.syncMouseCapture()
	nm, flushCmd := nm.settleTranscript()
	return nm, batchCommands(cmd, mouseCmd, flushCmd)
}

func batchCommands(cmds ...tea.Cmd) tea.Cmd {
	filtered := make([]tea.Cmd, 0, len(cmds))
	for _, cmd := range cmds {
		if cmd != nil {
			filtered = append(filtered, cmd)
		}
	}
	switch len(filtered) {
	case 0:
		return nil
	case 1:
		return filtered[0]
	default:
		return tea.Batch(filtered...)
	}
}

func (m model) updateModel(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseMsg:
		if m.setup.visible {
			return m.handleSetupMouse(msg)
		}
		return m.handleMouse(msg)
	case transcriptCopiedMsg:
		m.copyStatusSeq++
		if msg.err != nil {
			// Keep the selection so the user can retry; just surface the failure.
			m.copyStatus = "Copy failed"
		} else {
			m.transcriptSelection = transcriptSelectionState{}
			m.copyStatus = "Copied!"
		}
		seq := m.copyStatusSeq
		return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
			return transcriptCopyStatusExpiredMsg{seq: seq}
		})
	case transcriptCopyStatusExpiredMsg:
		if msg.seq == m.copyStatusSeq {
			m.copyStatus = ""
		}
		return m, nil
	case providerWizardOAuthMsg:
		return m.applyProviderWizardOAuth(msg)
	case providerWizardDeviceCodeMsg:
		return m.applyProviderWizardDeviceCode(msg)
	case tea.PasteMsg:
		if m.setup.visible || m.pendingAskUser != nil {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
		if m.transcriptDetailed || m.pendingSpecReview != nil || m.pendingPermission != nil || m.providerWizard != nil || m.mcpAddWizard != nil || m.mcpManager != nil || m.picker != nil {
			return m, nil
		}
		state := m.currentComposerState()
		m = m.applyComposerText(state, msg.Content, true)
		m.recomputeSuggestions()
		return m, nil
	case tea.KeyPressMsg:
		if m.setup.visible {
			return m.handleSetupKey(msg)
		}
		m.transcriptSelection = transcriptSelectionState{}
		m.clearMouseSelection()
		switch {
		case keyCtrl(msg, 'c'):
			// cancelRun records the in-flight run into flushRunIDs and writes the
			// "Run cancelled." marker, exactly like the Esc path. While ANY cancelled
			// run is still flushing we must NOT quit yet: each cancelled goroutine
			// returns its accumulated session events (including the
			// EventSessionCheckpoint blobs it already wrote to disk before each
			// mutating tool) in a final agentResponseMsg, and quitting now would drop
			// that message, orphaning the checkpoints and breaking /rewind. This
			// covers both a run cancelled BY this Ctrl+C and one cancelled by an
			// earlier Esc whose flush hasn't landed (m.pending is already false then,
			// but flushRunIDs is not empty). The agentResponseMsg handler fires
			// tea.Quit once flushRunIDs drains.
			m.cancelRun()
			m.exiting = true
			if len(m.flushRunIDs) > 0 {
				return m, nil
			}
			return m.quit()
		case keyCtrl(msg, 'o'):
			return m.toggleDetailedTranscript(), nil
		case keyIs(msg, tea.KeyEsc):
			if m.mcpCommandCancel != nil {
				m.cancelMCPCommand()
				if m.mcpAddWizard != nil {
					m.mcpAddWizard.result = mcpAddWizardResult{Title: "MCP setup cancelled", State: "cancelled", Message: "MCP action was cancelled.", ActionHint: "Edit config"}
					m.mcpAddWizard.step = mcpAddWizardStepResult
					return m, nil
				}
				m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "mcp", text: "MCP action cancelled"})
				return m, nil
			}
			if m.transcriptDetailed {
				m.transcriptDetailed = false
				return m, nil
			}
			// An active questionnaire is cancelled (not the whole run): deliver
			// whatever answers were collected so the agent loop unblocks and
			// degrades to its best-assumption path.
			if m.pendingAskUser != nil {
				return m.resolveAskUser(true)
			}
			if m.pendingSpecReview != nil {
				return m.cancelSpecReview()
			}
			if m.providerWizard != nil {
				m.providerWizard = nil
				return m, nil
			}
			if m.mcpAddWizard != nil {
				m.mcpAddWizard = nil
				return m, nil
			}
			if m.mcpManager != nil {
				m.mcpManager = nil
				return m, nil
			}
			// An open picker cancels first; then an active suggestion overlay is
			// dismissed. Neither cancels the run or clears the input.
			if m.picker != nil {
				if m.picker.kind == pickerModel {
					m.clearModelPickerLoadState()
				}
				m.picker = nil
				return m, nil
			}
			if m.suggestionsActive() {
				return m.dismissSuggestions(), nil
			}
			if m.hasQueuedMessage() {
				return m.clearQueuedMessage(), nil
			}
			m.clearComposer()
			m.clearSuggestions()
			if m.pending {
				m.cancelRun()
			}
			return m, nil
		case keyIs(msg, tea.KeyEnter):
			if m.transcriptDetailed {
				if command := parseCommand(m.input.Value()); command.kind == commandTranscript {
					m.input.SetValue("")
					return m.toggleDetailedTranscript(), nil
				}
				return m, nil
			}
			if m.pendingPermission != nil {
				return m, nil
			}
			if m.pendingAskUser != nil {
				return m.submitAskUserAnswer()
			}
			if m.pendingSpecReview != nil {
				return m, nil
			}
			if m.providerWizard != nil {
				return m.handleProviderWizardKey(msg)
			}
			if m.mcpAddWizard != nil {
				return m.handleMCPAddWizardKey(msg)
			}
			if m.mcpManager != nil {
				return m.handleMCPManagerKey(msg)
			}
			if m.picker != nil {
				return m.choosePicker()
			}
			if keyAlt(msg) {
				if next, ok := m.applyComposerKey(msg); ok {
					return next, nil
				}
			}
			// Enter on file suggestions inserts the @file token for continued
			// composing. Command suggestions execute only when the selected command
			// is self-contained; commands that require a value are inserted so the
			// user can finish the argument first.
			if m.suggestionsActive() {
				return m.chooseSuggestion()
			}
			return m.handleSubmit()
		case keyIs(msg, tea.KeyTab) && keyShift(msg):
			if m.transcriptDetailed {
				return m, nil
			}
			// shift+tab toggles the permission mode between Auto and Ask (Unsafe
			// is intentionally not reachable by a casual keypress — see
			// nextPermissionMode), but only when nothing modal is up: a permission
			// prompt, ask_user questionnaire, or open picker all take precedence
			// and let the key fall through to their own handlers below.
			if m.pendingPermission == nil && m.pendingAskUser == nil && m.pendingSpecReview == nil && m.providerWizard == nil && m.mcpAddWizard == nil && m.mcpManager == nil && m.picker == nil {
				m.permissionMode = nextPermissionMode(m.permissionMode)
				return m, nil
			}
		case keyCtrl(msg, 'f'):
			if m.picker != nil && m.picker.kind == pickerModel {
				if m.modelPickerIsLoading() {
					return m, nil
				}
				return m.toggleModelFavorite(), nil
			}
		case keyBackspace(msg):
			if m.picker != nil {
				if m.modelPickerIsLoading() {
					return m, nil
				}
				m.picker.deleteQueryRune()
				return m, nil
			}
		case keyIs(msg, tea.KeyTab):
			if m.transcriptDetailed {
				return m, nil
			}
			if m.providerWizard != nil {
				return m.handleProviderWizardKey(msg)
			}
			if m.mcpAddWizard != nil {
				return m.handleMCPAddWizardKey(msg)
			}
			if m.mcpManager != nil {
				return m.handleMCPManagerKey(msg)
			}
			if m.picker == nil && m.suggestionsActive() {
				m.moveSuggestion(1)
				return m, nil
			}
		case keyIs(msg, tea.KeyPgUp):
			if m.transcriptDetailed {
				return m, nil
			}
			return m.scrollChat(m.chatPageScrollLines()), nil
		case keyIs(msg, tea.KeyPgDown):
			if m.transcriptDetailed {
				return m, nil
			}
			return m.scrollChat(-m.chatPageScrollLines()), nil
		case keyIs(msg, tea.KeyDown):
			if m.transcriptDetailed {
				return m, nil
			}
			if m.providerWizard != nil {
				return m.handleProviderWizardKey(msg)
			}
			if m.mcpAddWizard != nil {
				return m.handleMCPAddWizardKey(msg)
			}
			if m.mcpManager != nil {
				return m.handleMCPManagerKey(msg)
			}
			if m.picker != nil {
				if m.modelPickerIsLoading() {
					return m, nil
				}
				m.picker.move(1)
				return m, nil
			}
			if m.suggestionsActive() {
				m.moveSuggestion(1)
				return m, nil
			}
			if next, ok := m.moveComposerVisualCursor(1); ok {
				return next, nil
			}
			if m.historyRecallActive() {
				return m.recallHistory(1), nil
			}
		case keyIs(msg, tea.KeyUp):
			if m.transcriptDetailed {
				return m, nil
			}
			if m.providerWizard != nil {
				return m.handleProviderWizardKey(msg)
			}
			if m.mcpAddWizard != nil {
				return m.handleMCPAddWizardKey(msg)
			}
			if m.mcpManager != nil {
				return m.handleMCPManagerKey(msg)
			}
			if m.picker != nil {
				if m.modelPickerIsLoading() {
					return m, nil
				}
				m.picker.move(-1)
				return m, nil
			}
			if m.suggestionsActive() {
				m.moveSuggestion(-1)
				return m, nil
			}
			if next, ok := m.moveComposerVisualCursor(-1); ok {
				return next, nil
			}
			if m.historyRecallActive() {
				return m.recallHistory(-1), nil
			}
		}
		if m.transcriptDetailed {
			return m, nil
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
		if m.providerWizard != nil {
			return m.handleProviderWizardKey(msg)
		}
		if m.mcpAddWizard != nil {
			return m.handleMCPAddWizardKey(msg)
		}
		if m.mcpManager != nil {
			return m.handleMCPManagerKey(msg)
		}
		// An open picker is modal over the input: swallow remaining keys so they
		// don't type into the field. ↑/↓/Enter/Esc were already handled above.
		if m.picker != nil {
			if m.modelPickerIsLoading() {
				return m, nil
			}
			if keyPrintable(msg) {
				m.picker.appendQuery(keyRunes(msg))
			}
			return m, nil
		}
		if next, ok := m.applyComposerKey(msg); ok {
			return next, nil
		}
		if m.composerActive && strings.Contains(m.composer.text, "\n") {
			return m, nil
		}
		// The key fell through to the text input: let it update, then refresh the
		// autocomplete match list from the new value.
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.resetComposerFromInput()
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
	case agentTextMsg:
		if msg.runID != m.activeRunID {
			return m, nil
		}
		m.streamingText += msg.delta
		return m, nil
	case agentReasoningMsg:
		if msg.runID != m.activeRunID {
			return m, nil
		}
		m.streamingReasoning += msg.delta
		return m, nil
	case spinner.TickMsg:
		// Not forwarding the tick while idle stops the spinner's self-scheduling,
		// so no timer fires between runs.
		if !m.pending && !m.compactInFlight && !m.doctorInFlight {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.compactInFlight {
			m.compactFrame++
			m = m.setCompactStatusRow(m.compactText(true))
		}
		if m.doctorInFlight {
			m.doctorFrame++
			m = m.setDoctorStatusRow(m.doctorConnectivityRunningText())
		}
		return m, cmd
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Size the composer so long input scrolls horizontally with the cursor
		// visible instead of being clipped invisibly past the right edge.
		m.input.SetWidth(maxInt(20, chatWidth(msg.Width)-14))
		// The title bar prints once into native scrollback when the inline
		// renderer is active. In alt-screen mode tea.Println is ignored, so the
		// title stays managed inside View.
		if !m.altScreen && !m.headerPrinted && msg.Width > 0 {
			m.headerPrinted = true
			m.flushQueue = append(m.flushQueue, m.titleBar(chatWidth(msg.Width)))
		}
		return m, nil
	case permissionRequestMsg:
		if msg.runID != m.activeRunID {
			return m, nil
		}
		promptRow := permissionTranscriptRow(permissionEventFromRequest(msg.request))
		promptRow.runID = msg.runID
		m.transcript = appendTranscriptRow(m.transcript, promptRow)
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
		m.clearComposer()
		m.clearSuggestions()
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
			if flushSessionID, flushing := m.flushRunIDs[msg.runID]; flushing {
				delete(m.flushRunIDs, msg.runID)
				// The cancelled run still consumed tokens; record them so the usage
				// readout doesn't undercount interrupted turns.
				for _, event := range msg.usageEvents {
					var usageRows []transcriptRow
					m, usageRows = m.recordUsageEvent(msg.usageModelID, event)
					for _, row := range usageRows {
						m.transcript = appendTranscriptRow(m.transcript, row)
					}
				}
				// Events are persisted into the session the run was recording into AT
				// CANCEL TIME — the active session may have changed since (/resume),
				// and writing there would contaminate its log with checkpoint payloads
				// whose blobs live under the original session. appendSessionEvents*
				// only returns rows for persist FAILURES; surface them so a failed
				// checkpoint/tool flush (which would silently degrade /rewind) is
				// visible rather than swallowed.
				var flushRows []transcriptRow
				events := flushableSessionEvents(msg.sessionEvents)
				if flushSessionID == m.activeSession.SessionID {
					m, flushRows = m.appendSessionEvents(events)
				} else {
					flushRows = m.appendSessionEventsTo(flushSessionID, events)
				}
				for _, row := range flushRows {
					m.transcript = appendTranscriptRow(m.transcript, row)
				}
				// A Ctrl+C during an in-flight run defers its quit until the run's
				// checkpoint session events have been flushed (above). Now that the
				// last pending flush is drained, fire the deferred quit.
				if m.exiting && len(m.flushRunIDs) == 0 {
					return m.quit()
				}
			}
			return m, nil
		}
		m.pending = false
		// The run is complete: release its context now instead of waiting for the
		// parent context — every prompt leaked a CancelFunc (and its timer
		// resources) until app exit otherwise.
		if m.runCancel != nil {
			m.runCancel()
		}
		m.runCancel = nil
		m.activeRunID = 0
		m.pendingPermission = nil
		m.pendingAskUser = nil
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
			if row.kind == rowReasoning {
				m.streamingReasoning = ""
				m.streamingReasoningExpanded = false
			}
			m.transcript = appendTranscriptRow(m.transcript, row)
		}
		if msg.err != nil {
			// A failed turn has no final answer row to supersede the streamed
			// text the user already watched — keep the partial answer instead of
			// letting it vanish from history.
			if row, ok := reasoningTranscriptRow("", msg.runID, m.streamingReasoning); ok {
				m.transcript = appendTranscriptRow(m.transcript, row)
			}
			if text := strings.TrimRight(m.streamingText, "\n"); strings.TrimSpace(text) != "" {
				m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowAssistant, text: text})
			}
			// The error row terminates the turn, so it carries the done-line
			// metadata a final assistant row would have carried.
			m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
				kind:        rowError,
				text:        msg.err.Error(),
				final:       true,
				turnTools:   msg.turnTools,
				turnElapsed: msg.turnElapsed,
			})
		}
		m.streamingText = ""
		m.streamingReasoning = ""
		m.streamingReasoningExpanded = false
		if msg.specReview != nil {
			m = m.activateSpecReview(*msg.specReview)
		}
		if m.notifier != nil {
			m.notifier.Notify(notify.Completion, notify.DefaultMessage(notify.Completion))
		}
		return m.launchQueuedMessageIfReady()
	case compactResultMsg:
		if !m.compactInFlight {
			return m, nil
		}
		m.compactInFlight = false
		m.compactFrame = 0
		m.lastCompactResult = nil
		m.lastCompactError = ""
		if msg.err != nil {
			m.lastCompactError = msg.err.Error()
			m = m.setCompactStatusRow(m.compactText(true))
			return m, nil
		}
		if msg.hasSessionSnapshot {
			m.activeSession = msg.activeSession
			m.sessionEvents = append([]sessions.Event{}, msg.sessionEvents...)
			m.transcript = append([]transcriptRow{}, msg.transcript...)
			m.resetFlushFrontier("· compacted ·")
		}
		m.lastCompactResult = &msg.result
		m = m.setCompactStatusRow(m.compactText(true))
		return m, nil
	case agentRowMsg:
		if msg.runID != m.activeRunID {
			return m, nil
		}
		if msg.row.kind == rowReasoning {
			m.streamingReasoning = ""
			m.streamingReasoningExpanded = false
		}
		// A tool call ends the current streamed text segment. The segment is the
		// assistant's working narration ("Let me check X…") — append it as a
		// non-final assistant row so it stays in history instead of silently
		// vanishing when the tool card replaces the interim block.
		if msg.row.kind == rowToolCall {
			if row, ok := reasoningTranscriptRow("", msg.runID, m.streamingReasoning); ok {
				m.transcript = appendTranscriptRow(m.transcript, row)
				m.streamingReasoning = ""
				m.streamingReasoningExpanded = false
			}
			if text := strings.TrimRight(m.streamingText, "\n"); strings.TrimSpace(text) != "" {
				m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowAssistant, text: text})
			}
			m.streamingText = ""
		}
		m.transcript = appendTranscriptRow(m.transcript, msg.row)
		return m, nil
	case doctorCommandResultMsg:
		if msg.id == 0 || msg.id == m.doctorCommandSeq {
			m.doctorInFlight = false
			m.doctorFrame = 0
			m = m.setDoctorStatusRow(msg.text)
		}
		return m, nil
	case prStateMsg:
		m.prState = msg.state
		return m, nil
	case prWatcherStartedMsg:
		if msg.stop == nil {
			return m, nil
		}
		if m.prWatcherStop != nil {
			m.prWatcherStop()
		}
		m.prWatcherStop = msg.stop
		return m, nil
	case bashResultMsg:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: msg.output})
		return m, nil
	case providerModelsDiscoveredMsg:
		return m.applyProviderModelsDiscovered(msg), nil
	case setupModelsDiscoveredMsg:
		return m.applySetupModelsDiscovered(msg), nil
	case setupOAuthMsg:
		return m.applySetupOAuth(msg)
	case setupOAuthDeviceMsg:
		return m.applySetupOAuthDeviceCode(msg)
	case modelPickerModelsDiscoveredMsg:
		return m.applyModelPickerModelsDiscovered(msg), nil
	case mcpCommandResultMsg:
		return m.applyMCPCommandResultMessage(msg), nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) View() tea.View {
	var content string
	if m.setup.visible {
		content = m.setupView(chatWidth(m.width))
	} else if m.transcriptDetailed {
		content = m.detailedTranscriptView()
	} else {
		content = m.transcriptView()
	}

	view := tea.NewView(content)
	view.AltScreen = m.altScreen
	view.ReportFocus = m.notifier != nil
	if m.wantsMouseCapture() {
		view.MouseMode = tea.MouseModeCellMotion
	}
	return view
}

// transcriptEmpty reports whether the chat surface has no real content yet
// (only the welcome row), which is when the empty state renders.
func (m model) transcriptEmpty() bool {
	for _, row := range m.transcript {
		if row.kind != rowWelcome {
			return false
		}
	}
	return true
}

// transcriptView renders the visible chat surface: in inline mode this is the
// live tail not yet settled into native scrollback; in alt-screen mode it is
// the managed conversation view. Streaming/modal blocks and composer chrome are
// always rendered here.
func (m model) transcriptView() string {
	width := chatWidth(m.width)

	suggestionOverlay := m.suggestionOverlay(width)
	providerOverlay := m.providerWizardOverlay(width)
	mcpAddOverlay := m.mcpAddWizardOverlay(width)
	mcpOverlay := m.mcpManagerOverlay(width)
	pickerOverlay := m.pickerOverlay(width)
	viewportOverlay := ""
	switch {
	case providerOverlay != "":
		viewportOverlay = providerOverlay
	case mcpAddOverlay != "":
		viewportOverlay = mcpAddOverlay
	case mcpOverlay != "":
		viewportOverlay = mcpOverlay
	case pickerOverlay != "":
		viewportOverlay = pickerOverlay
	case suggestionOverlay != "":
		viewportOverlay = suggestionOverlay
	}
	emptyOverlay := ""
	if m.transcriptEmpty() && !m.pending && viewportOverlay != "" {
		emptyOverlay = viewportOverlay
	}
	body, _ := m.transcriptBody(width, emptyOverlay)

	footer := m.footerView(width)

	overlayForViewport := viewportOverlay
	if m.transcriptEmpty() && !m.pending && viewportOverlay != "" {
		overlayForViewport = ""
	}

	if m.altScreen && m.height > 0 {
		return m.scrollableTranscriptView(body, footer, width, overlayForViewport)
	}

	if overlayForViewport != "" {
		body += "\n" + overlayForViewport + "\n"
	}
	return body + footer
}

func (m model) footerView(width int) string {
	var footer strings.Builder
	if copyStatus := strings.TrimSpace(m.copyStatus); copyStatus != "" {
		footer.WriteString(rightAlignedLine(zeroTheme.ink.Render(copyStatus), width))
		footer.WriteString("\n")
	} else {
		footer.WriteString("\n")
	}
	if chips := renderAttachmentChips(m.pendingImageLabels, m.pendingDocuments); chips != "" {
		footer.WriteString(fitStyledLine(zeroTheme.muted.Render(chips), width))
		footer.WriteString("\n")
	}
	footer.WriteString(m.composerBox(width))
	if queued := renderQueuedMessagePreview(m.queuedMessage, width); queued != "" {
		footer.WriteString("\n")
		footer.WriteString(queued)
	}
	footer.WriteString("\n")
	footer.WriteString(m.statusLine(width))
	return footer.String()
}

func (m model) scrollableTranscriptView(body string, footer string, width int, overlay string) string {
	bodyLines := viewLines(body)
	footerLines := viewLines(footer)
	maxFooterLines := maxInt(0, m.height-1)
	if len(footerLines) > maxFooterLines {
		footerLines = footerLines[len(footerLines)-maxFooterLines:]
	}
	available := m.height - len(footerLines)
	if available < 1 {
		available = 1
	}
	maxOffset := maxInt(0, len(bodyLines)-available)
	offset := clamp(m.chatScrollOffset, 0, maxOffset)
	start := maxInt(0, len(bodyLines)-available-offset)
	end := minInt(len(bodyLines), start+available)

	lines := make([]string, 0, available+len(footerLines))
	if start < end {
		lines = append(lines, bodyLines[start:end]...)
	}
	for len(lines) < available {
		lines = append(lines, "")
	}
	lines = overlayViewportLines(lines, overlay, width)
	lines = append(lines, footerLines...)
	for index, line := range lines {
		lines[index] = fitStyledLine(line, width)
	}
	return strings.Join(lines, "\n")
}

func overlayViewportLines(lines []string, overlay string, width int) []string {
	if strings.TrimSpace(overlay) == "" || len(lines) == 0 {
		return lines
	}
	overlayLines := viewLines(overlay)
	if len(overlayLines) == 0 {
		return lines
	}
	left, overlayLines, overlayWidth := normalizeOverlayBlock(overlayLines, width)
	if overlayWidth <= 0 {
		return lines
	}
	start := maxInt(0, (len(lines)-len(overlayLines))/2)
	for offset, line := range overlayLines {
		target := start + offset
		if target >= len(lines) {
			break
		}
		lines[target] = overlayViewportLine(lines[target], line, left, overlayWidth, width)
	}
	return lines
}

func normalizeOverlayBlock(lines []string, width int) (int, []string, int) {
	left := -1
	for _, line := range lines {
		if strings.TrimSpace(ansi.Strip(line)) == "" {
			continue
		}
		spaces := leadingPlainSpaces(line)
		if left < 0 || spaces < left {
			left = spaces
		}
	}
	if left < 0 {
		left = 0
	}
	left = minInt(left, maxInt(0, width-1))

	trimmed := make([]string, 0, len(lines))
	blockWidth := 0
	for _, line := range lines {
		if left > 0 && len(line) >= left {
			line = line[left:]
		}
		trimmed = append(trimmed, line)
		blockWidth = maxInt(blockWidth, lipgloss.Width(line))
	}
	blockWidth = minInt(blockWidth, maxInt(0, width-left))
	return left, trimmed, blockWidth
}

func leadingPlainSpaces(line string) int {
	spaces := 0
	for spaces < len(line) && line[spaces] == ' ' {
		spaces++
	}
	return spaces
}

func overlayViewportLine(base string, overlay string, left int, overlayWidth int, width int) string {
	if width <= 0 {
		return ""
	}
	left = clampInt(left, 0, width)
	overlayWidth = minInt(overlayWidth, width-left)
	rightStart := minInt(width, left+overlayWidth)

	base = fitStyledLine(base, width)
	prefix := padStyledLine(ansi.Cut(base, 0, left), left)
	panel := padStyledLine(overlay, overlayWidth)
	suffix := padStyledLine(ansi.Cut(base, rightStart, width), width-rightStart)
	return prefix + panel + suffix
}

func padStyledLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = fitStyledLine(line, width)
	if pad := width - lipgloss.Width(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line
}

func viewLines(value string) []string {
	if value == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(value, "\n"), "\n")
}

func (m model) scrollChat(delta int) model {
	if !m.altScreen || delta == 0 {
		return m
	}
	maxOffset := m.chatMaxScrollOffset()
	current := clampInt(m.chatScrollOffset, 0, maxOffset)
	m.chatScrollOffset = clampInt(current+delta, 0, maxOffset)
	if m.chatScrollOffset == 0 {
		m.chatBodyLines = 0
	}
	return m
}

func (m model) chatMaxScrollOffset() int {
	_, maxOffset := m.chatScrollMetrics()
	return maxOffset
}

func (m model) chatScrollMetrics() (int, int) {
	if !m.altScreen || m.height <= 0 {
		return 0, 0
	}
	width := chatWidth(m.width)
	body, _ := m.transcriptBody(width, "")
	bodyLines := len(viewLines(body))
	footerLines := viewLines(m.footerView(width))
	maxFooterLines := maxInt(0, m.height-1)
	if len(footerLines) > maxFooterLines {
		footerLines = footerLines[len(footerLines)-maxFooterLines:]
	}
	available := m.height - len(footerLines)
	if available < 1 {
		available = 1
	}
	return bodyLines, maxInt(0, bodyLines-available)
}

// syncChatScroll pins the viewport to what the user is reading. The scroll offset
// is measured from the bottom, so when the transcript grows (streaming) the window
// would otherwise follow the new bottom and drag the user off their spot. While
// the user has scrolled up, shift the offset by however many lines the body changed
// so the absolute view holds; at the bottom (offset 0) it follows normally. Only the
// scrolled-up path renders the body, so the common case stays cheap.
func (m model) syncChatScroll() model {
	if !m.altScreen || m.chatScrollOffset <= 0 {
		// At the bottom (or inline mode): follow the tail; reset the pin baseline.
		m.chatBodyLines = 0
		return m
	}
	current, maxOffset := m.chatScrollMetrics()
	m.chatScrollOffset = clampInt(m.chatScrollOffset, 0, maxOffset)
	if m.chatScrollOffset <= 0 {
		m.chatBodyLines = 0
		return m
	}
	if m.chatBodyLines == 0 {
		// Just scrolled up: establish the baseline, no adjustment this frame.
		m.chatBodyLines = current
		return m
	}
	// Shift by the signed delta so the absolute view holds whether the body grew
	// (streaming appended lines) or shrank (a tool card collapsed, transcript
	// cleared). Clamp at zero so a large shrink lands the user back at the tail
	// rather than underflowing past it.
	m.chatScrollOffset = clampInt(m.chatScrollOffset+current-m.chatBodyLines, 0, maxOffset)
	m.chatBodyLines = current
	return m
}

func (m model) chatPageScrollLines() int {
	if m.height <= 0 {
		return 10
	}
	return maxInt(3, m.height-8)
}

// interimBlock renders the live assistant text while a turn streams. It uses
// the same lightweight markdown renderer as completed assistant rows, so
// tables and simple formatting stabilize as soon as enough tokens arrive.
// Before the first delta arrives it falls back to the spinner so the surface
// still shows liveness. The cursor needs no ticker — it appears exactly while
// pending.
func (m model) interimBlock(width int) string {
	text := strings.TrimRight(m.streamingText, "\n")
	reasoning := strings.TrimRight(m.streamingReasoning, "\n")
	blocks := []string{}
	if strings.TrimSpace(reasoning) != "" {
		blocks = append(blocks, renderReasoningBlock(reasoning, m.streamingReasoningExpanded, width, true, 0))
	}
	if strings.TrimSpace(text) == "" {
		if len(blocks) > 0 {
			return strings.Join(blocks, "\n")
		}
		return zeroTheme.accent.Render(m.spinner.View()) + " " + zeroTheme.muted.Render("working…")
	}
	lines := renderAssistantMarkdownText(text, assistantMeasure(width), width)
	for index, line := range lines {
		lines[index] = styleAssistantMarkdownLine(line, zeroTheme.ink)
	}
	lines = appendStreamingCursor(lines, width)
	blocks = append(blocks, strings.Join(lines, "\n"))
	return strings.Join(blocks, "\n")
}

func appendStreamingCursor(lines []string, width int) []string {
	cursor := zeroTheme.accent.Render("▌")
	if len(lines) == 0 {
		return []string{cursor}
	}
	last := len(lines) - 1
	if width > 0 && lipgloss.Width(lines[last])+1 > width {
		return append(lines, cursor)
	}
	lines[last] += cursor
	return lines
}

// composerLine renders the borderless composer.
func (m model) composerLine(width int) string {
	input := m.input
	hideInputForSuggestions := m.suggestionsActive() && (!m.suggestionsAreFiles || fileSuggestionOnlyInput(m.input.Value()))
	if hideInputForSuggestions {
		input.SetValue("")
		input.Placeholder = ""
		input.CursorEnd()
	}
	state := composerState{text: input.Value(), cursor: input.Position()}
	if m.composerActive {
		state = m.composer
	}
	if hideInputForSuggestions {
		state = composerState{}
	}
	argumentHint := commandArgumentHintForInput(input.Value())
	if argumentHint != "" && input.Position() != len([]rune(input.Value())) {
		argumentHint = ""
	}
	if argumentHint != "" {
		input.SetWidth(0)
		return fitStyledLine(commandArgumentHintComposerLine(input, argumentHint), width)
	}
	previews := validComposerPastePreviews(state, m.composerPastePreviews)
	displayState := composerDisplayStateForPastePreviews(state, previews)
	return renderComposerInput(input, displayState, width)
}

type composerVisualLine struct {
	first bool
	start int
	end   int
}

func renderComposerInput(input textinput.Model, state composerState, width int) string {
	state = normalizeComposerState(state)
	if width <= 0 {
		return ""
	}
	if state.text == "" {
		return fitStyledLine(composerVisualLinePrefix(input, true)+zeroTheme.faint.Render(input.Placeholder), width)
	}

	segments := composerWrappedVisualLines(input, state, width)
	cursorLine := composerCursorVisualLine(segments, state.cursor)
	if len(segments) > composerMaxVisibleLines {
		start := clamp(cursorLine-composerMaxVisibleLines+1, 0, len(segments)-composerMaxVisibleLines)
		end := start + composerMaxVisibleLines
		cursorLine -= start
		segments = segments[start:end]
		if len(segments) > 0 {
			segments[0].first = true
		}
	}

	lines := make([]string, 0, len(segments))
	for index, segment := range segments {
		lines = append(lines, fitStyledLine(renderComposerVisualLine(input, state, segment, index == cursorLine), width))
	}
	return strings.Join(lines, "\n")
}

func composerWrappedVisualLines(input textinput.Model, state composerState, width int) []composerVisualLine {
	runes := []rune(state.text)
	segments := []composerVisualLine{}
	first := true
	start := 0
	for index, r := range runes {
		if r != '\n' {
			continue
		}
		segments = appendComposerWrappedVisualLines(segments, input, runes, start, index, width, &first)
		start = index + 1
	}
	segments = appendComposerWrappedVisualLines(segments, input, runes, start, len(runes), width, &first)
	return segments
}

func appendComposerWrappedVisualLines(segments []composerVisualLine, input textinput.Model, runes []rune, start int, end int, width int, first *bool) []composerVisualLine {
	if start >= end {
		segments = append(segments, composerVisualLine{first: *first, start: start, end: end})
		*first = false
		return segments
	}
	for start < end {
		lineFirst := *first
		measure := maxInt(1, width-lipgloss.Width(composerVisualLinePrefix(input, lineFirst)))
		split := start
		used := 0
		for split < end {
			nextWidth := lipgloss.Width(string(runes[split]))
			if used+nextWidth > measure {
				break
			}
			used += nextWidth
			split++
		}
		if split == start {
			split++
		}
		segments = append(segments, composerVisualLine{first: lineFirst, start: start, end: split})
		*first = false
		start = split
	}
	return segments
}

func composerCursorVisualLine(segments []composerVisualLine, cursor int) int {
	if len(segments) == 0 {
		return 0
	}
	for index, segment := range segments {
		if cursor < segment.start || cursor > segment.end {
			continue
		}
		if cursor == segment.end && index+1 < len(segments) && segments[index+1].start == cursor {
			continue
		}
		return index
	}
	return len(segments) - 1
}

func renderComposerVisualLine(input textinput.Model, state composerState, segment composerVisualLine, hasCursor bool) string {
	runes := []rune(state.text)
	prefix := composerVisualLinePrefix(input, segment.first)
	textStyle := zeroTheme.ink.Inline(true)
	if !hasCursor {
		return prefix + textStyle.Render(string(runes[segment.start:segment.end]))
	}

	offset := clamp(state.cursor-segment.start, 0, segment.end-segment.start)
	cursorIndex := segment.start + offset
	before := string(runes[segment.start:cursorIndex])
	if cursorIndex < segment.end {
		after := string(runes[cursorIndex+1 : segment.end])
		return prefix + textStyle.Render(before) + composerCursor(string(runes[cursorIndex])) + textStyle.Render(after)
	}
	return prefix + textStyle.Render(before) + composerCursor(" ")
}

func composerVisualLinePrefix(input textinput.Model, first bool) string {
	if first {
		return zeroTheme.userPrompt.Render(input.Prompt)
	}
	return "  "
}

func composerDisplayStateForPastePreviews(state composerState, previews []composerPastePreview) composerState {
	state = normalizeComposerState(state)
	valid := validComposerPastePreviews(state, previews)
	if len(valid) == 0 {
		return state
	}
	runes := []rune(state.text)
	display := make([]rune, 0, len(runes))
	last := 0
	for _, preview := range valid {
		display = append(display, runes[last:preview.start]...)
		display = append(display, []rune(preview.label)...)
		last = preview.end
	}
	display = append(display, runes[last:]...)
	return composerState{
		text:   string(display),
		cursor: composerDisplayCursorForPastePreviews(state.cursor, valid),
	}
}

func composerDisplayCursorForPastePreviews(cursor int, previews []composerPastePreview) int {
	delta := 0
	for _, preview := range previews {
		labelLen := len([]rune(preview.label))
		hiddenLen := preview.end - preview.start
		displayStart := preview.start + delta
		switch {
		case cursor <= preview.start:
			return cursor + delta
		case cursor <= preview.end:
			return displayStart + labelLen
		default:
			delta += labelLen - hiddenLen
		}
	}
	return cursor + delta
}

func (m model) moveComposerVisualCursor(direction int) (model, bool) {
	if direction == 0 {
		return m, false
	}
	width := chatWidth(m.width)
	if width < 8 {
		return m, false
	}
	input := m.input
	state := m.currentComposerState()
	state = normalizeComposerState(state)
	if state.text == "" {
		return m, false
	}
	previews := validComposerPastePreviews(state, m.composerPastePreviews)
	displayState := composerDisplayStateForPastePreviews(state, previews)
	segments := composerWrappedVisualLines(input, displayState, maxInt(1, width-4))
	if len(segments) <= 1 {
		return m, false
	}
	cursorLine := composerCursorVisualLine(segments, displayState.cursor)
	targetLine := clamp(cursorLine+direction, 0, len(segments)-1)
	if targetLine == cursorLine {
		return m, true
	}
	column := composerVisualCursorColumn(displayState, segments[cursorLine])
	displayState.cursor = composerCursorForVisualColumn(displayState, segments[targetLine], column)
	state.cursor = composerOriginalCursorForPastePreviews(displayState.cursor, previews)
	m.setComposerState(state)
	return m, true
}

func composerOriginalCursorForPastePreviews(displayCursor int, previews []composerPastePreview) int {
	if len(previews) == 0 {
		return displayCursor
	}
	delta := 0
	for _, preview := range previews {
		labelLen := len([]rune(preview.label))
		hiddenLen := preview.end - preview.start
		displayStart := preview.start + delta
		displayEnd := displayStart + labelLen
		switch {
		case displayCursor <= displayStart:
			return displayCursor - delta
		case displayCursor <= displayEnd:
			return preview.end
		default:
			delta += labelLen - hiddenLen
		}
	}
	return displayCursor - delta
}

func composerVisualCursorColumn(state composerState, segment composerVisualLine) int {
	state = normalizeComposerState(state)
	runes := []rune(state.text)
	cursor := clamp(state.cursor, segment.start, segment.end)
	column := 0
	for index := segment.start; index < cursor && index < len(runes); index++ {
		column += lipgloss.Width(string(runes[index]))
	}
	return column
}

func composerCursorForVisualColumn(state composerState, segment composerVisualLine, column int) int {
	state = normalizeComposerState(state)
	runes := []rune(state.text)
	used := 0
	for index := segment.start; index < segment.end && index < len(runes); index++ {
		width := lipgloss.Width(string(runes[index]))
		if used+width > column {
			return index
		}
		used += width
	}
	return segment.end
}

func commandArgumentHintComposerLine(input textinput.Model, argumentHint string) string {
	hintRunes := []rune(argumentHint)
	if len(hintRunes) == 0 {
		return input.View()
	}
	displayValue := strings.TrimRightFunc(input.Value(), unicode.IsSpace)
	return zeroTheme.userPrompt.Render(input.Prompt) +
		zeroTheme.ink.Inline(true).Render(displayValue) +
		zeroTheme.faint.Render(" ") +
		composerCursor(zeroTheme.faint.Render(string(hintRunes[0]))) +
		zeroTheme.faint.Render(string(hintRunes[1:]))
}

func composerCursor(char string) string {
	return zeroTheme.selection.Render(char)
}

func commandArgumentHintForInput(value string) string {
	command := parseCommand(value)
	if command.name == "" || strings.TrimSpace(command.text) != "" {
		return ""
	}
	return commandRequiredInputHint(command.name)
}

func (m model) composerBox(width int) string {
	if width < 8 {
		return fitStyledLine(m.composerLine(width), width)
	}
	innerWidth := maxInt(1, width-4)
	content := m.composerLine(innerWidth)
	lines := strings.Split(content, "\n")

	rendered := make([]string, 0, len(lines)+2)
	rendered = append(rendered, zeroTheme.lineStrong.Render("╭"+strings.Repeat("─", width-2)+"╮"))
	for _, line := range lines {
		fitted := fitStyledLine(line, innerWidth)
		pad := strings.Repeat(" ", maxInt(0, innerWidth-lipgloss.Width(fitted)))
		rendered = append(rendered, zeroTheme.lineStrong.Render("│ ")+fitted+pad+zeroTheme.lineStrong.Render(" │"))
	}
	rendered = append(rendered, m.composerDividerLine(width))
	return strings.Join(rendered, "\n")
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
	m.clearSuggestions()
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
// or "/mode <name>".
func (m model) choosePicker() (tea.Model, tea.Cmd) {
	if m.modelPickerIsLoading() {
		return m, nil
	}
	picker := m.picker
	if picker != nil && picker.kind == pickerModel {
		m.clearModelPickerLoadState()
	}
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
		text := ""
		m, text = m.handleModelCommand(item.Value)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
	case pickerEffort:
		text := ""
		m, text = m.handleEffortCommand(item.Value)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
	case pickerMode:
		text := ""
		m, text = m.handleModeCommand(item.Value)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
	}
	return m, nil
}

func (m model) chooseSuggestion() (tea.Model, tea.Cmd) {
	if !m.suggestionsActive() || len(m.suggestions) == 0 {
		return m, nil
	}
	wasFiles := m.suggestionsAreFiles
	wasDirectory := m.selectedSuggestionIsDirectory()
	requiresInput := m.selectedCommandSuggestionRequiresInput()
	next := m.completeSuggestion()
	if !wasFiles {
		next.resetComposerFromInput()
	}
	if wasFiles && wasDirectory {
		next.recomputeSuggestions()
		return next, nil
	}
	if !wasFiles {
		if requiresInput {
			return next, nil
		}
		return next.handleSubmit()
	}
	return next, nil
}

func (m model) handleSubmit() (tea.Model, tea.Cmd) {
	input := m.composerValue()
	command := parseCommand(input)
	// While exiting (Ctrl+C waiting on the cancelled run's checkpoint flush) a
	// new run must not start: the deferred tea.Quit would abort it mid-flight
	// and orphan its checkpoint blobs — the exact loss flushRunIDs prevents.
	if command.kind == commandPrompt && m.exiting {
		return m, nil
	}
	if command.kind == commandPrompt && m.pending {
		return m.queueMessage(command.text), nil
	}
	if command.kind == commandPrompt && m.compactInFlight {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: "Compact\nstatus: warning\nCompaction is running. Your next prompt will use the compacted context when this finishes.",
		})
		return m, nil
	}
	m.rememberInput(input)
	m.clearComposer()
	m.clearSuggestions()
	// Snap the viewport back to the bottom for a real submission, but not for an
	// empty Enter (a no-op) — that would yank the user away from wherever they
	// had scrolled without anything actually being submitted.
	if command.kind != commandEmpty {
		m.chatScrollOffset = 0
	}

	switch command.kind {
	case commandEmpty:
		return m, nil
	case commandHelp:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: helpText()})
		return m, nil
	case commandClear:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionClear})
		// Scrollback above can't be un-printed; a faint divider marks where the
		// cleared surface ended and the frontier restarts for the fresh transcript.
		m.resetFlushFrontier("· cleared ·")
		return m, nil
	case commandExit:
		// /exit gets the same protection as Ctrl+C: cancel any in-flight run and
		// defer the quit until its checkpoint session events flush — quitting
		// immediately would orphan the blobs and break /rewind.
		m.cancelRun()
		m.exiting = true
		if len(m.flushRunIDs) > 0 {
			return m, nil
		}
		return m.quit()
	case commandTools:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.toolsText()})
		return m, nil
	case commandMCP:
		if strings.TrimSpace(command.text) == "" {
			return m.openMCPManager(), nil
		}
		return m.startMCPTranscriptCommand(command.text)
	case commandPermissions:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.permissionsText()})
		return m, nil
	case commandProvider:
		if strings.TrimSpace(command.text) == "" {
			if m.pending {
				m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: pickerBusyText(command.name)})
				return m, nil
			}
			m.providerWizard = m.newProviderWizard()
			m.clearSuggestions()
			return m, nil
		}
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.providerText()})
		return m, nil
	case commandModel:
		if strings.TrimSpace(command.text) == "" {
			if m.pending {
				m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: pickerBusyText(command.name)})
				return m, nil
			}
			next, cmd := m.openModelPicker()
			if next.picker != nil {
				return next, cmd
			}
		}
		text := ""
		m, text = m.handleModelCommand(command.text)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		return m, nil
	case commandMode:
		if strings.TrimSpace(command.text) == "" {
			if m.pending {
				m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: pickerBusyText(command.name)})
				return m, nil
			}
			if picker := m.newModePicker(); picker != nil {
				m.picker = picker
				return m, nil
			}
		}
		text := ""
		m, text = m.handleModeCommand(command.text)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
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
	case commandDoctor:
		return m.startDoctorCommand(command.text)
	case commandSearch:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: m.searchText(command.text)})
		return m, nil
	case commandResume:
		if m.pending {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{
				kind: actionAppendError,
				text: "Cannot resume sessions while a run is active.",
			})
			return m, nil
		}
		text := ""
		m, text = m.handleResumeCommand(command.text)
		if strings.HasPrefix(text, sessionsCardsPrefix) {
			// The list payload renders as stacked session cards, not a note.
			m.transcript = appendTranscriptRow(m.transcript, transcriptRow{
				kind: rowSystem,
				tool: "sessions",
				text: strings.TrimPrefix(text, sessionsCardsPrefix),
			})
		} else if text != "" {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		}
		return m, nil
	case commandSpec:
		return m.handleSpecCommand(command.text)
	case commandCompact:
		text := ""
		var compactCmd tea.Cmd
		m, text, compactCmd = m.handleCompactCommand(command.text)
		m = m.setCompactStatusRow(text)
		return m, compactCmd
	case commandTranscript:
		return m.toggleDetailedTranscript(), nil
	case commandRewind:
		text := ""
		m, text = m.handleRewindCommand(command.text)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		return m, nil
	case commandEffort:
		if strings.TrimSpace(command.text) == "" {
			if m.pending {
				m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: pickerBusyText(command.name)})
				return m, nil
			}
			if picker := m.newEffortPicker(); picker != nil {
				m.picker = picker
				return m, nil
			}
		}
		text := ""
		m, text = m.handleEffortCommand(command.text)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		return m, nil
	case commandStyle:
		text := ""
		m, text = m.handleStyleCommand(command.text)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		return m, nil
	case commandSelfCorrect:
		text := ""
		m, text = m.handleSelfCorrectCommand(command.text)
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: text})
		return m, nil
	case commandTheme:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: shellOnlyCommandText(command.name),
		})
		return m, nil
	case commandInputStyle:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendSystem,
			text: shellOnlyCommandText(command.name),
		})
		return m, nil
	case commandImage:
		m = m.handleImageCommand(command.text)
		return m, nil
	case commandAddDir:
		m = m.handleAddDirCommand(command.text)
		return m, nil
	case commandUnknown:
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendError,
			text: "unknown command: " + command.text,
		})
		return m, nil
	case commandBash:
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
		if intent, ok := detectMCPSetupIntent(command.text); ok {
			return m.openMCPAddWizardFromIntent(intent), nil
		}
		return m.launchPrompt(command.text)
	default:
		return m, nil
	}
}

// launchPrompt starts a normal agent turn from text already accepted by the
// composer. Queued prompts use this path too, so session and image behavior
// stays identical to immediate submissions.
func (m model) launchPrompt(prompt string) (model, tea.Cmd) {
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendUser, text: prompt})
	if m.provider == nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendAssistant,
			text: "No provider configured.",
		})
		return m, nil
	}
	// Prepend any staged PDF document text as a model-facing preamble. The
	// visible transcript above keeps the user's clean prompt; the agent (and the
	// recorded session, for resume fidelity) sees the document text first.
	if preamble := m.consumePendingDocuments(); preamble != "" {
		prompt = preamble + prompt
	}
	var err error
	m, err = m.ensureActiveSession(prompt)
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{
			kind: actionAppendError,
			text: "session create error: " + err.Error(),
		})
	} else {
		agentPrompt := m.sessionPrompt(prompt)
		m, err = m.appendSessionEvent(sessions.EventMessage, map[string]any{
			"role":    "user",
			"content": prompt,
		})
		if err != nil {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{
				kind: actionAppendError,
				text: "session record error: " + err.Error(),
			})
		}
		prompt = agentPrompt
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
	return m, tea.Batch(m.runAgent(m.activeRunID, runCtx, prompt, turnImages), m.spinner.Tick)
}

func (m model) launchQueuedMessageIfReady() (model, tea.Cmd) {
	if !m.hasQueuedMessage() || m.pending || m.exiting || m.pendingPermission != nil || m.pendingAskUser != nil || m.pendingSpecReview != nil {
		return m, nil
	}
	prompt := m.queuedMessage
	m.queuedMessage = ""
	return m.launchPrompt(prompt)
}

// historyRecallActive reports whether ↑/↓ should navigate previously submitted
// inputs: history exists and no modal surface owns the arrow keys.
func (m model) historyRecallActive() bool {
	return len(m.inputHistory) > 0 &&
		m.pendingAskUser == nil && m.pendingPermission == nil && m.pendingSpecReview == nil
}

// recallHistory steps through submitted inputs (-1 = older, +1 = newer),
// stashing the in-progress draft so stepping back past the newest recalled
// entry restores whatever was being typed.
func (m model) recallHistory(direction int) model {
	if m.historyIdx == len(m.inputHistory) {
		if direction > 0 {
			return m
		}
		m.historyDraft = m.composerValue()
	}
	next := clamp(m.historyIdx+direction, 0, len(m.inputHistory))
	if next == m.historyIdx {
		return m
	}
	m.historyIdx = next
	if next == len(m.inputHistory) {
		m.input.SetValue(m.historyDraft)
	} else {
		m.input.SetValue(m.inputHistory[next])
	}
	m.input.CursorEnd()
	m.resetComposerFromInput()
	m.recomputeSuggestions()
	return m
}

// rememberInput records a submitted composer value for ↑ recall and resets the
// navigation cursor past the newest entry.
func (m *model) rememberInput(value string) {
	trimmed := strings.TrimSpace(value)
	if trimmed != "" && (len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != trimmed) {
		m.inputHistory = append(m.inputHistory, trimmed)
	}
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = ""
}

func (m *model) cancelRun() {
	if m.runCancel != nil {
		m.runCancel()
	}
	// Remember the in-flight run — and the session it was recording into — so
	// its final agentResponseMsg is still drained for session-event persistence
	// after activeRunID is cleared. Otherwise the checkpoint blobs it captured
	// before each mutating tool are orphaned on disk and /rewind can't reference
	// them; without the session id, a /resume before the flush lands would
	// append the old run's events into the newly active session.
	if m.pending && m.activeRunID != 0 {
		if m.flushRunIDs == nil {
			m.flushRunIDs = make(map[int]string)
		}
		m.flushRunIDs[m.activeRunID] = m.activeSession.SessionID
	}
	if m.pending {
		// A cancelled run must terminate visibly in the transcript: first the
		// partial streamed answer (if any), then the cancellation marker — the
		// session log gets the same marker below.
		if row, ok := reasoningTranscriptRow("", m.activeRunID, m.streamingReasoning); ok {
			m.transcript = appendTranscriptRow(m.transcript, row)
		}
		if text := strings.TrimRight(m.streamingText, "\n"); strings.TrimSpace(text) != "" {
			m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowAssistant, text: text})
		}
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, text: "Run cancelled."})
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
	// The interim block renders streamingText live; a cancelled run's partial
	// answer must not leak into (and concatenate with) the next turn's stream.
	m.streamingText = ""
	m.streamingReasoning = ""
	m.streamingReasoningExpanded = false
}

func (m model) runAgent(runID int, runCtx context.Context, prompt string, images []zeroruntime.ImageBlock) tea.Cmd {
	return m.runAgentWithOptions(runID, runCtx, prompt, images, tuiAgentRunOptions{})
}

// selfCorrectAutonomyForMode maps the active permission mode to the self-correct
// autonomy gate: more autonomous modes auto-fix after a failed verification,
// while restrictive modes only surface the failure. Mirrors exec's --auto levels.
func selfCorrectAutonomyForMode(mode agent.PermissionMode) string {
	switch mode {
	case agent.PermissionModeUnsafe:
		return "high"
	case agent.PermissionModeAuto:
		return "medium"
	default: // ask, etc. — report the failure without starting an auto-fix round
		return "low"
	}
}

func (m model) runAgentWithOptions(runID int, runCtx context.Context, prompt string, images []zeroruntime.ImageBlock, runOptions tuiAgentRunOptions) tea.Cmd {
	return func() tea.Msg {
		started := m.now()
		toolCalls := 0
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
		options.ProviderName = m.providerName
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

		// Post-edit self-correction is on by default in the TUI but kept FAST: it
		// runs LSP diagnostics over the changed files only — cheap, change-scoped,
		// and a no-op when no language server is installed. The project test plan
		// (`go test ./...`, whole-repo) is NOT run per edit by default — that would
		// add the full suite's latency to every turn and let a pre-existing failure
		// hijack the agent — so the test half is opt-in via `/selfcorrect on`
		// (m.selfCorrectTests). The spec-draft (planning) path never wires it,
		// matching exec; the per-turn lsp.Manager is torn down when this run
		// returns; auto-fix vs report-only follows the active permission mode.
		if !runOptions.specDraft && options.Cwd != "" {
			lspManager := lsp.NewManager(options.Cwd)
			defer func() {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_ = lspManager.Shutdown(shutdownCtx)
			}()
			options.SelfCorrect = agent.NewSelfCorrector(options.Cwd, agent.NewLSPDiagnosticsChecker(lspManager), agent.NewProjectVerifier(options.Cwd), agent.SelfCorrectConfig{
				Enabled:      true,
				IncludeTests: m.selfCorrectTests,
				IncludeLSP:   true,
				Autonomy:     selfCorrectAutonomyForMode(options.PermissionMode),
			})
		}

		// Some providers synthesize tool-call ids that repeat within a run (e.g.
		// Gemini restarts its gemini_tool_N numbering on every provider turn).
		// Transcript rows need distinct ids for dedup and call→result collapse,
		// so repeats get an ordinal suffix; session payloads keep the provider's
		// original ids.
		callSeq := map[string]int{}
		reasoningText := ""
		reasoningSeq := 0
		var reasoningStarted time.Time
		var reasoningLast time.Time
		flushReasoning := func(closedAt time.Time) {
			if row, ok := reasoningTranscriptRow(fmt.Sprintf("reasoning_%d", reasoningSeq+1), runID, reasoningText); ok {
				if !reasoningStarted.IsZero() {
					if closedAt.IsZero() {
						closedAt = reasoningLast
					}
					if !reasoningLast.IsZero() && closedAt.Before(reasoningLast) {
						closedAt = reasoningLast
					}
					if elapsed := closedAt.Sub(reasoningStarted); elapsed > 0 {
						row.turnElapsed = elapsed
					}
				}
				reasoningSeq++
				rows = append(rows, row)
				m.sendAgentRow(runID, row)
			}
			reasoningText = ""
			reasoningStarted = time.Time{}
			reasoningLast = time.Time{}
		}

		onText := options.OnText
		options.OnText = func(delta string) {
			if strings.TrimSpace(reasoningText) != "" {
				flushReasoning(m.now())
			}
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
				// Persist the answers next to the question event so the exchange
				// is complete on /resume; rehydration renders them as a system note.
				sessionEvents = append(sessionEvents, pendingSessionEvent{
					Type: sessions.EventMessage,
					Payload: map[string]any{
						"role":       "ask_user_answers",
						"toolCallId": request.ToolCallID,
						"answers":    answers,
					},
				})
				return agent.AskUserResponse{Answers: answers}, nil
			case <-ctx.Done():
				return agent.AskUserResponse{}, ctx.Err()
			}
		}

		onReasoning := options.OnReasoning
		options.OnReasoning = func(delta string) {
			now := m.now()
			if strings.TrimSpace(reasoningText) == "" && strings.TrimSpace(delta) != "" {
				reasoningStarted = now
			}
			if strings.TrimSpace(delta) != "" {
				reasoningLast = now
			}
			reasoningText += delta
			m.sendAgentReasoning(runID, delta)
			if onReasoning != nil {
				onReasoning(delta)
			}
		}

		onToolCall := options.OnToolCall
		options.OnToolCall = func(call agent.ToolCall) {
			flushReasoning(m.now())
			toolCalls++
			callSeq[call.ID]++
			row := transcriptRow{
				kind:   rowToolCall,
				id:     effectiveToolRowID(call.ID, callSeq[call.ID]),
				text:   "tool call: " + call.Name,
				tool:   call.Name,
				detail: argHint(call.Arguments),
				arg:    argHintSecondary(call.Arguments),
				runID:  runID,
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
				id:     effectiveToolRowID(result.ToolCallID, callSeq[result.ToolCallID]),
				text:   toolResultRowText(result),
				tool:   result.Name,
				status: result.Status,
				detail: result.Output,
				runID:  runID,
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
			row.runID = runID
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
			flushReasoning(m.now())
			sessionEvents = append(sessionEvents, pendingSessionEvent{
				Type:    sessions.EventError,
				Payload: map[string]any{"message": err.Error()},
			})
			return agentResponseMsg{runID: runID, rows: rows, usageEvents: usageEvents, usageModelID: usageModelID, sessionEvents: sessionEvents, err: err, turnTools: toolCalls, turnElapsed: m.now().Sub(started)}
		}
		if runOptions.specDraft {
			if result.StopReason != agent.StopReasonSpecReviewRequired || specReview == nil || specReview.SpecID == "" || specReview.SpecFilePath == "" {
				err := fmt.Errorf("spec draft ended without submit_spec")
				flushReasoning(m.now())
				sessionEvents = append(sessionEvents, pendingSessionEvent{
					Type:    sessions.EventError,
					Payload: map[string]any{"message": err.Error()},
				})
				return agentResponseMsg{runID: runID, rows: rows, usageEvents: usageEvents, usageModelID: usageModelID, sessionEvents: sessionEvents, err: err, turnTools: toolCalls, turnElapsed: m.now().Sub(started)}
			}
			flushReasoning(m.now())
			return agentResponseMsg{runID: runID, rows: rows, usageEvents: usageEvents, usageModelID: usageModelID, sessionEvents: sessionEvents, specReview: specReview, turnTools: toolCalls, turnElapsed: m.now().Sub(started)}
		}
		flushReasoning(m.now())
		elapsed := m.now().Sub(started)
		rows = append(rows, transcriptRow{
			kind:        rowAssistant,
			text:        result.FinalAnswer,
			final:       true,
			turnTools:   toolCalls,
			turnElapsed: elapsed,
		})
		if notice := result.TruncationNotice(); notice != "" {
			rows = append(rows, transcriptRow{kind: rowSystem, text: notice})
		}
		sessionEvents = append(sessionEvents, pendingSessionEvent{
			Type: sessions.EventMessage,
			Payload: map[string]any{
				"role":    "assistant",
				"content": result.FinalAnswer,
			},
		})
		return agentResponseMsg{runID: runID, rows: rows, usageEvents: usageEvents, usageModelID: usageModelID, sessionEvents: sessionEvents, turnTools: toolCalls, turnElapsed: elapsed}
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

func (m model) sendAgentReasoning(runID int, delta string) {
	if m.runtimeMessageSink == nil {
		return
	}
	m.runtimeMessageSink(agentReasoningMsg{runID: runID, delta: delta})
}

func toolResultRowText(result agent.ToolResult) string {
	status := result.Status
	if status == "" {
		status = tools.StatusOK
	}
	return fmt.Sprintf("tool result: %s %s %s", result.Name, status, truncateTUIOutput(result.Output, tuiToolOutputLimit))
}
