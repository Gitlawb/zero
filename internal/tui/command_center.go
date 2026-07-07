package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/credstore"
	"github.com/Gitlawb/zero/internal/doctor"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/oauth"
	"github.com/Gitlawb/zero/internal/providermodelcatalog"
	"github.com/Gitlawb/zero/internal/providers"
	"github.com/Gitlawb/zero/internal/redaction"
	zsearch "github.com/Gitlawb/zero/internal/search"
)

const doctorStatusRowID = "doctor/status"

func (m model) startDoctorCommand(args string) (model, tea.Cmd) {
	connectivity, fix, help, err := parseDoctorCommandArgs(args)
	if err != nil {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: doctorUsageText(commandStatusBlocked, err.Error())})
		return m, nil
	}
	if help {
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: doctorUsageText(commandStatusInfo, "Show local diagnostics for provider, model, sandbox, LSP, and backend setup.")})
		return m, nil
	}
	if fix {
		return m.startDoctorFixCommand()
	}
	if !connectivity {
		m = m.setDoctorStatusRow(m.doctorText(false))
		return m, nil
	}

	m.doctorCommandSeq++
	id := m.doctorCommandSeq
	snapshot := m
	m.doctorInFlight = true
	m.doctorFrame = 0
	m = m.setDoctorStatusRow(m.doctorConnectivityRunningText())
	return m, tea.Batch(func() tea.Msg {
		return doctorCommandResultMsg{id: id, text: snapshot.doctorText(true)}
	}, m.spinner.Tick)
}

func (m model) startDoctorFixCommand() (model, tea.Cmd) {
	report := doctor.Run(m.doctorOptions(false))
	if doctorReportNeedsProviderSetup(report) {
		if m.pending {
			m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: doctorFixBusyText()})
			return m, nil
		}
		m.providerWizard = m.newProviderWizard()
		m.clearSuggestions()
		m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: doctorFixProviderSetupText()})
		return m, nil
	}
	if doctorReportCanProbeConnectivity(report) {
		return m.startDoctorCommand("--connectivity")
	}
	m.transcript = reduceTranscript(m.transcript, transcriptAction{kind: actionAppendSystem, text: doctorFixPlanText(report)})
	return m, nil
}

func (m model) doctorText(connectivity bool) string {
	report := doctor.Run(m.doctorOptions(connectivity))
	return renderCommandOutput(doctorCommandOutput(report, nil))
}

func parseDoctorCommandArgs(args string) (connectivity bool, fix bool, help bool, err error) {
	for _, field := range strings.Fields(args) {
		switch strings.ToLower(field) {
		case "--connectivity", "connectivity":
			connectivity = true
		case "--fix", "fix":
			fix = true
		case "-h", "--help", "help":
			help = true
		default:
			return false, false, false, fmt.Errorf("unknown doctor flag %q", field)
		}
	}
	if connectivity && fix {
		return false, false, false, fmt.Errorf("choose either %q or %q, not both", "fix", "--connectivity")
	}
	return connectivity, fix, help, nil
}

func doctorReportNeedsProviderSetup(report doctor.Report) bool {
	for _, id := range []string{"provider.config", "provider.model"} {
		if check := report.Check(id); check != nil && check.Status == doctor.StatusFail {
			return true
		}
	}
	return false
}

func doctorReportCanProbeConnectivity(report doctor.Report) bool {
	check := report.Check("provider.connectivity")
	return check != nil && check.Status != doctor.StatusPass
}

func doctorUsageText(status commandStatus, message string) string {
	return renderCommandOutput(commandOutput{
		Title:  "Diagnostics",
		Status: status,
		Sections: []commandSection{{
			Title: "Usage",
			Lines: []string{
				message,
				"/doctor",
				"/doctor fix",
				"/doctor --connectivity",
				"/health",
			},
		}},
	})
}

func doctorFixProviderSetupText() string {
	return renderCommandOutput(commandOutput{
		Title:  "Diagnostics fix",
		Status: commandStatusInfo,
		Sections: []commandSection{{
			Title: "Provider",
			Lines: []string{"Opening provider setup. Choose a provider, add credentials, then select a model."},
		}},
		Hints: []string{"Esc closes setup"},
	})
}

func doctorFixBusyText() string {
	return renderCommandOutput(commandOutput{
		Title:  "Diagnostics fix",
		Status: commandStatusWarning,
		Sections: []commandSection{{
			Title: "Provider",
			Lines: []string{"Cannot open provider setup while a run is active."},
		}},
		Hints: []string{"stop the current run, then retry /doctor fix"},
	})
}

func doctorFixPlanText(report doctor.Report) string {
	return renderCommandOutput(commandOutput{
		Title:  "Diagnostics fix",
		Status: doctorCommandStatus(report),
		Sections: []commandSection{{
			Title: "Next actions",
			Lines: doctorFixLines(report),
		}},
		Hints: []string{"run /doctor --connectivity to recheck provider health"},
	})
}

func doctorFixLines(report doctor.Report) []string {
	lines := []string{}
	hasIssue := false
	for _, check := range report.Checks {
		if check.Status == doctor.StatusPass {
			continue
		}
		hasIssue = true
		switch check.ID {
		case "sandbox.backend":
			if remedy := doctorCheckDetailString(check, "remedy"); remedy != "" {
				lines = append(lines, "native sandbox: "+remedy)
			} else {
				lines = append(lines, "native sandbox: run zero sandbox policy --effective to inspect backend status")
			}
		case "lsp.servers":
			lines = append(lines, "language servers: install missing LSP binaries on PATH")
		case "provider.connectivity":
			lines = append(lines, "provider connectivity: run /doctor --connectivity")
		case "config.files", "config.validation":
			lines = append(lines, "config: run /provider to create or repair provider config")
		}
	}
	if len(lines) == 0 {
		if hasIssue {
			return []string{"No automatic fixes are available for the detected diagnostics."}
		}
		return []string{"No automatic fixes are available because diagnostics are already clean."}
	}
	return lines
}

func (m model) doctorConnectivityRunningText() string {
	return strings.Join([]string{
		"Checking provider",
		"Zero is probing the active endpoint. Keep typing; messages will queue until the check finishes.",
		m.doctorAnimationLine(),
		"provider: " + displayValue(m.providerName, displayValue(m.providerProfile.Name, "unknown")),
		"model: " + displayValue(m.modelName, displayValue(m.providerProfile.Model, "unknown")),
	}, "\n")
}

func (m model) doctorAnimationLine() string {
	frame := compactFrames[m.doctorFrame%len(compactFrames)]
	return frame + " checking provider connectivity..."
}

func (m model) setDoctorStatusRow(text string) model {
	row := transcriptRow{kind: rowSystem, id: doctorStatusRowID, tool: "doctor", text: text}
	for i := len(m.transcript) - 1; i >= 0; i-- {
		if m.transcript[i].id == doctorStatusRowID {
			m.transcript[i] = row
			return m
		}
	}
	m.transcript = appendTranscriptRow(m.transcript, row)
	return m
}

func (m model) searchText(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return "Search\nusage: /search <query>"
	}
	result, err := zsearch.Sessions(query, zsearch.Options{
		Store:        m.sessionStore,
		Limit:        5,
		ContextChars: 120,
		Now:          m.now,
	})
	if err != nil {
		return "Search\nerror: " + err.Error()
	}
	return zsearch.FormatResult(zsearch.RedactResult(result))
}

// resumeText is the text fallback for /resume — the no-session "none" message and
// the store-error message (the interactive picker handles the populated case). It
// also still backs the stacked-card list rendering as a defensive fallback.
func (m model) resumeText() string {
	// Defensive: a model without a session store (some fallback/test paths) must
	// render a safe message rather than panic on the nil dereference below.
	if m.sessionStore == nil {
		return renderCommandOutput(commandOutput{
			Title:  "Sessions",
			Status: commandStatusBlocked,
			Sections: []commandSection{{
				Title: "Store",
				Lines: []string{"session store unavailable"},
			}},
		})
	}
	// Only standalone conversations — not child/spec sub-runs, which an agent
	// spawns by the dozen and would otherwise flood the picker (the "… N more").
	sessions, err := m.sessionStore.ListResumable()
	if err != nil {
		return renderCommandOutput(commandOutput{
			Title:  "Sessions",
			Status: commandStatusBlocked,
			Sections: []commandSection{{
				Title: "Store",
				Lines: []string{"error: " + err.Error()},
			}},
		})
	}
	if len(sessions) == 0 {
		return renderCommandOutput(commandOutput{
			Title:  "Sessions",
			Status: commandStatusInfo,
			Sections: []commandSection{{
				Title: "Recent",
				Lines: []string{"none"},
			}},
		})
	}
	limit := len(sessions)
	if limit > 8 {
		limit = 8
	}
	// The list renders as stacked cards (renderSessionsCards); each record is
	// one session's fields joined by the unit separator so the renderer can
	// restyle them at the current width. Flow and data are unchanged.
	records := make([]string, 0, limit+1)
	for index := 0; index < limit; index++ {
		session := sessions[index]
		meta := strings.Join([]string{
			sanitizeCardField(displayValue(session.ModelID, "no model")),
			sanitizeCardField(displayValue(session.Provider, "no provider")),
			fmt.Sprintf("%d events", session.EventCount),
		}, " · ")
		records = append(records, strings.Join([]string{
			sanitizeCardField(session.SessionID),
			relativeAge(session.UpdatedAt, m.now()),
			sanitizeCardField(displayValue(session.Title, "untitled")),
			meta,
		}, sessionsCardFieldSep))
	}
	if len(sessions) > limit {
		records = append(records, fmt.Sprintf("… %d more · /resume <id>", len(sessions)-limit))
	} else {
		records = append(records, "use /resume latest or /resume <id> to load a session")
	}
	return sessionsCardsPrefix + strings.Join(records, "\n")
}

const (
	// sessionsCardsPrefix marks a resumeText payload that renders as stacked
	// session cards instead of a plain system note.
	sessionsCardsPrefix = "\x00sessions\x00"
	// sessionsCardFieldSep separates the id/age/title/meta fields of one card.
	sessionsCardFieldSep = "\x1f"
)

type modelSwitchCompactionRequest struct {
	CurrentModel         string
	TargetModel          string
	CurrentProvider      string
	TargetProvider       string
	CurrentContextWindow int
	TargetContextWindow  int
	EstimatedTokens      int
	SessionEventCount    int
	CompactRequests      int
}

type modelSwitchCompactionDecision struct {
	RequestCompaction bool
	Reason            string
}

type modelSwitchCompactionPolicy interface {
	BeforeModelSwitch(modelSwitchCompactionRequest) modelSwitchCompactionDecision
}

type defaultModelSwitchCompactionPolicy struct{}

func (defaultModelSwitchCompactionPolicy) BeforeModelSwitch(request modelSwitchCompactionRequest) modelSwitchCompactionDecision {
	if request.CompactRequests > 0 || request.SessionEventCount <= tuiCompactionPreserveLast {
		return modelSwitchCompactionDecision{}
	}
	if request.TargetContextWindow <= 0 || request.EstimatedTokens <= 0 {
		return modelSwitchCompactionDecision{}
	}
	threshold := int(float64(request.TargetContextWindow) * 0.8)
	if request.EstimatedTokens < threshold {
		return modelSwitchCompactionDecision{}
	}
	return modelSwitchCompactionDecision{
		RequestCompaction: true,
		Reason:            fmt.Sprintf("estimated context %s tokens is near target context %s tokens", formatContextWindow(request.EstimatedTokens), formatContextWindow(request.TargetContextWindow)),
	}
}

var modelSwitchCompactionGuard modelSwitchCompactionPolicy = defaultModelSwitchCompactionPolicy{}

// sanitizeCardField strips the card protocol's separator bytes from
// user-controlled values (titles can legally contain anything --session-title
// was given), so a hostile or accidental \x1f / newline cannot shift fields
// or leak control characters into the transcript.
func sanitizeCardField(value string) string {
	value = strings.ReplaceAll(value, sessionsCardFieldSep, " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.ReplaceAll(value, "\x00", "")
}

// relativeAge renders an RFC3339 timestamp as a short age ("2h ago"); ""
// when the timestamp does not parse, so the card simply omits it.
func relativeAge(timestamp string, now time.Time) string {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(timestamp))
	if err != nil {
		return ""
	}
	age := now.Sub(parsed)
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(age.Hours()/24))
	}
}

func (m model) handleModelCommand(args string) (model, string) {
	args = strings.TrimSpace(args)
	switch strings.ToLower(args) {
	case "":
		return m, m.modelText(args)
	case "list", "ls":
		return m, m.modelListText()
	}
	if m.pending {
		return m, "Model\nCannot switch models while a run is active."
	}

	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return m, "Model\nFailed to load model catalog: " + err.Error()
	}
	target, ok := m.resolveModelSwitchTarget(registry, args)
	if !ok {
		return m, "Model\nunknown Zero model " + strconv.Quote(args)
	}
	if !config.HasProviderProfile(m.providerProfile) {
		return m, "Model\nNo provider profile is available for TUI model switching."
	}
	if m.newProvider == nil {
		return m, "Model\nProvider rebuild is not available for this TUI session."
	}

	nextProfile := m.providerProfile
	if provider, ok := m.activeProviderDescriptor(); ok {
		nextProfile = m.normalizeProfileForProvider(provider)
	}
	nextProfile.Model = target.modelID
	// Reload the credential: a stored-key provider's profile carries an empty APIKey
	// (the resolver is pure), so without this the rebuilt provider would send no key.
	nextProfile = m.profileWithCredential(nextProfile)
	metadata, err := providers.ResolveRuntimeMetadata(nextProfile, providers.Options{})
	if err != nil {
		return m, "Model\n" + err.Error()
	}

	if guarded, text, requested := m.requestCompactionBeforeModelSwitch(modelSwitchCompactionRequest{
		TargetModel:         target.modelID,
		TargetProvider:      string(metadata.ProviderKind),
		TargetContextWindow: modelregistry.AgentContextWindow(m.modelContextWindow(target.modelID)),
	}, "Model"); requested {
		return guarded, text
	}

	nextProvider, err := m.newProvider(nextProfile)
	if err != nil {
		return m, "Model\n" + err.Error()
	}
	persisted, persistErr := m.persistSelectedModel(nextProfile)

	m.providerProfile = nextProfile
	m.provider = nextProvider
	m.providerName = displayValue(nextProfile.Name, string(metadata.ProviderKind))
	// Keep sub-agent child processes on the same provider we just switched to.
	config.SetActiveProviderEnv(nextProfile.Name)
	m.modelName = target.modelID
	resetEffort := false
	if m.reasoningEffort != "" && !reasoningEffortAllowed(target.reasoningEfforts, m.reasoningEffort) {
		// Drop an unsupported carry-over preference and fall back to the
		// model's effective default for the new model.
		m.reasoningEffort = ""
		resetEffort = true
	}
	effortLine := "effort: " + m.effortDisplay()
	if resetEffort {
		// Preference was dropped: show "auto" (model default applies), not a
		// concrete value that would read as an explicit setting.
		effortLine += " (unsupported preference reset)"
	} else if target.entry != nil {
		if effective := modelregistry.EffectiveReasoningEffort(*target.entry, m.reasoningEffort); effective != modelregistry.ReasoningEffortNone {
			effortLine = "effort: " + string(effective)
		}
	}
	// Compact one-line summary — "<model> · <provider>[ · effort …][ · saved]" —
	// instead of a multi-line model/provider/api/effort/saved block.
	summary := target.modelID + " · " + displayValue(nextProfile.Name, string(metadata.ProviderKind))
	effort := strings.TrimSpace(strings.TrimPrefix(effortLine, "effort: "))
	if resetEffort {
		summary += " · effort auto (reset)"
	} else if effort != "" && effort != "auto" {
		summary += " · effort " + effort
	}
	if persisted {
		summary += " · saved"
	} else if persistErr != nil {
		// Keep the failure reason (e.g. an unwritable config path) so persistence
		// problems stay debuggable from the status line.
		summary += " · not saved (" + persistErr.Error() + ")"
	}
	lines := []string{"Model"}
	if target.notice != "" {
		lines = append(lines, target.notice)
	}
	lines = append(lines, summary)
	if warn := m.visionDropWarning(); warn != "" {
		lines = append(lines, warn)
	}
	return m, strings.Join(lines, "\n")
}

// switchProviderModel switches the active provider to providerName (one of the
// saved providers) and sets modelID, rebuilding the provider client. The /model
// picker calls this when a model from a non-active provider is chosen, so the
// picker can list every saved provider and switch across them (like a unified
// provider+model selector). The key is loaded from the encrypted store / env.
// The returned bool reports whether the switch actually committed — callers
// that branch on the outcome (the provider manager) must use it, never the
// display text: UI copy is not a control-flow contract (a refusal quoting a
// provider name could contain any substring, and rewording the success line
// must not change behavior).
func (m model) switchProviderModel(providerName, modelID string) (model, string, bool, tea.Cmd) {
	if m.pending {
		return m, "Model\nCannot switch providers while a run is active.", false, nil
	}
	if m.newProvider == nil {
		return m, "Model\nProvider rebuild is not available for this TUI session.", false, nil
	}
	target, ok := m.savedProviderByName(providerName)
	if !ok {
		return m, "Model\nunknown provider " + strconv.Quote(providerName), false, nil
	}
	target = m.profileWithCredential(target)
	target.Model = strings.TrimSpace(modelID)
	descriptor, hasDescriptor := m.descriptorForProfile(target)
	// Gate on the resolved credential, not the APIKeyStored marker: if the stored key
	// was deleted/unreadable the marker can still be set, and building a keyless
	// provider would only fail later with a 401. Local/no-auth providers need no key,
	// and a stored OAuth login (e.g. ChatGPT) is a credential too — the profile stays
	// keyless on purpose so newProvider attaches the bearer resolver + login key.
	if strings.TrimSpace(target.APIKey) == "" && strings.TrimSpace(target.AuthHeaderValue) == "" &&
		!(hasDescriptor && descriptor.Local) && !oauthLoginAvailable(target) {
		return m, "Model\nprovider " + strconv.Quote(providerName) + " has no usable credential — run setup or `zero auth login " + providerName + "`.", false, nil
	}
	next, err := m.newProvider(target)
	if err != nil {
		return m, "Model\n" + redaction.RedactString(err.Error(), redaction.Options{ExtraSecretValues: []string{target.APIKey}}), false, nil
	}
	m.provider = next
	m.providerProfile = target
	m.providerName = target.Name
	m.modelName = target.Model
	// Keep sub-agent child processes on the same provider we just switched to.
	config.SetActiveProviderEnv(target.Name)
	if strings.TrimSpace(m.userConfigPath) != "" {
		_, _ = config.SetActiveProvider(m.userConfigPath, target.Name)
		_, _ = config.SetProviderModel(m.userConfigPath, target.Name, target.Model)
	}
	// Warm discovery for the provider we just switched to, same as Init() does
	// for the provider active at launch — otherwise the context-usage gauge has
	// no window for this provider until /model happens to be opened separately.
	var cmds []tea.Cmd
	if hasDescriptor {
		if cmd := m.modelPickerProviderDiscoveryCmd(descriptor, target); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.ollamaContextWindowDiscoveryCmd(descriptor, target.BaseURL, target.Model); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	status := fmt.Sprintf("Model\nSwitched to %s · %s", target.Name, target.Model)
	if warn := m.visionDropWarning(); warn != "" {
		status += "\n" + warn
	}
	return m, status, true, tea.Batch(cmds...)
}

// profileWithCredential fills a profile's APIKey for provider construction the same
// way the runtime resolves it: a stored key (encrypted credstore), then an env var.
// The config resolver is pure (no secret I/O), so a stored-key profile carries an
// empty APIKey until this runs — every place that rebuilds the provider (model
// switch, provider switch) must call this or the request goes out with no key.
//
// A stored OAuth bearer is deliberately NOT copied into APIKey: an inline key makes
// HasConfiguredCredential true, which strips the OAuth login candidates at build
// time, so newProvider would construct the client without the bearer resolver and
// login key — for ChatGPT (Codex) that drops the `chatgpt-account-id` header and
// token refresh, 401-ing every call. Keyless is the correct shape for a token-login
// profile; newProvider resolves the login itself.
func (m model) profileWithCredential(profile config.ProviderProfile) config.ProviderProfile {
	if strings.TrimSpace(profile.APIKey) == "" {
		if store, err := m.providerKeyStore(); err == nil {
			profile = config.ApplyStoredAPIKey(profile, store)
		}
	}
	if strings.TrimSpace(profile.APIKey) == "" && strings.TrimSpace(profile.APIKeyEnv) != "" {
		profile.APIKey = strings.TrimSpace(os.Getenv(profile.APIKeyEnv))
	}
	return profile
}

// providerKeyStore opens the credential store co-located with THIS session's
// config path — the store every TUI write path (SecureProviderProfile capture,
// rename migration, delete) uses. Reading from the default-path store instead
// makes a stored key invisible whenever userConfigPath is non-default, so the
// switch gate rejects a provider whose key is right there beside its config.
// Ephemeral sessions without a config path fall back to the default store.
func (m model) providerKeyStore() (*credstore.Store, error) {
	return providerKeyStoreForPath(m.userConfigPath)
}

func providerKeyStoreForPath(configPath string) (*credstore.Store, error) {
	if strings.TrimSpace(configPath) != "" {
		return config.ProviderKeyStoreAt(filepath.Dir(configPath))
	}
	return config.ProviderKeyStore()
}

// oauthLoginAvailable reports whether a stored OAuth login exists for any of the
// profile's login candidates — the SAME candidate set the runtime bearer resolver
// uses (profile name, then catalog id, only for keyless profiles), so the switch
// gate and the runtime can never disagree about whether a login counts.
func oauthLoginAvailable(profile config.ProviderProfile) bool {
	_, ok := oauthLoginName(profile)
	return ok
}

// oauthLoginName resolves WHICH stored login serves this profile — the same
// FirstStored selection the runtime makes. User-facing hints must name this
// entry, not the profile: after a rename ({name:"codex", catalogID:"chatgpt"})
// the token lives under the catalog id, and `zero auth logout codex` would
// delete nothing while the real login stays behind.
func oauthLoginName(profile config.ProviderProfile) (string, bool) {
	candidates := profile.OAuthLoginCandidates()
	if len(candidates) == 0 {
		return "", false
	}
	store, err := oauth.NewStore(oauth.StoreOptions{})
	if err != nil {
		return "", false
	}
	_, key, ok := oauth.FirstStored(store, candidates)
	if !ok {
		return "", false
	}
	return strings.TrimPrefix(key, oauth.KeyPrefixProvider), true
}

func (m model) savedProviderByName(name string) (config.ProviderProfile, bool) {
	name = strings.TrimSpace(name)
	for _, profile := range m.savedProviders {
		if strings.EqualFold(strings.TrimSpace(profile.Name), name) {
			return profile, true
		}
	}
	if strings.EqualFold(strings.TrimSpace(m.providerProfile.Name), name) {
		return m.providerProfile, true
	}
	return config.ProviderProfile{}, false
}

func (m model) persistSelectedModel(profile config.ProviderProfile) (bool, error) {
	path := strings.TrimSpace(m.userConfigPath)
	if path == "" {
		return false, nil
	}
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		return false, nil
	}
	model := strings.TrimSpace(profile.Model)
	if model == "" {
		return false, nil
	}
	if _, err := config.SetProviderModel(path, name, model); err != nil {
		return false, err
	}
	return true, nil
}

type modelSwitchTarget struct {
	modelID          string
	entry            *modelregistry.ModelEntry
	notice           string
	reasoningEfforts []modelregistry.ReasoningEffort
}

func (m model) resolveModelSwitchTarget(registry modelregistry.Registry, args string) (modelSwitchTarget, bool) {
	entry, notice, ok := registry.ResolveWithFallback(args)
	if ok {
		return modelSwitchTarget{
			modelID:          entry.ID,
			entry:            &entry,
			notice:           notice,
			reasoningEfforts: entry.ReasoningEfforts,
		}, true
	}
	if provider, ok := m.activeProviderDescriptor(); ok {
		for _, model := range m.modelPickerLiveByProvider[provider.ID] {
			if strings.EqualFold(model.ID, strings.TrimSpace(args)) {
				return modelSwitchTarget{modelID: model.ID}, true
			}
		}
		for _, model := range providermodelcatalog.Models(provider) {
			if strings.EqualFold(model.ID, strings.TrimSpace(args)) {
				return modelSwitchTarget{modelID: model.ID}, true
			}
		}
		if genericProviderCatalogID(provider.ID) && strings.TrimSpace(args) != "" {
			return modelSwitchTarget{modelID: strings.TrimSpace(args)}, true
		}
	}
	return modelSwitchTarget{}, false
}

func (m model) requestCompactionBeforeModelSwitch(request modelSwitchCompactionRequest, title string) (model, string, bool) {
	if modelSwitchCompactionGuard == nil {
		return m, "", false
	}
	request.CurrentModel = m.modelName
	request.CurrentProvider = m.providerName
	request.CurrentContextWindow = m.modelContextWindow(m.modelName)
	request.EstimatedTokens = estimateTranscriptTokens(m.transcript)
	request.SessionEventCount = len(m.sessionEvents)
	request.CompactRequests = m.compactRequests

	decision := modelSwitchCompactionGuard.BeforeModelSwitch(request)
	if !decision.RequestCompaction {
		return m, "", false
	}

	m.compactRequests++
	lines := []string{
		title,
		"Context compaction requested before switching models.",
		"The active model/provider is unchanged until compaction can run.",
		"from model: " + displayValue(request.CurrentModel, "none"),
		"to model: " + displayValue(request.TargetModel, "none"),
	}
	if request.TargetProvider != "" {
		lines = append(lines, "target provider: "+request.TargetProvider)
	}
	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		lines = append(lines, "reason: "+reason)
	}
	lines = append(lines, "compaction: "+m.compactionStatus())
	return m, strings.Join(lines, "\n"), true
}

func apiKeyState(set bool) string {
	if set {
		return "set"
	}
	return "not set"
}
