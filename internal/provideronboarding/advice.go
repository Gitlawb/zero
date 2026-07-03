package provideronboarding

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/Gitlawb/zero/internal/agentcli"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
)

type Action struct {
	Label   string
	Command string
	Detail  string
}

type ProviderState struct {
	Profile config.ProviderProfile
	Active  bool
	// Detections are the machine's detected agent-CLI harnesses (agentcli.Detect),
	// optional. Nil/empty just means "no CLI advice available" — Actions() then
	// behaves exactly as it did before AuthCLI existed.
	Detections []agentcli.Detection
}

func (state ProviderState) Actions() []Action {
	return ProviderActionsWithDetections(state.Profile, state.Active, state.Detections)
}

func SetupCommand(descriptor providercatalog.Descriptor, name string, setActive bool) string {
	parts := []string{"zero", "providers", "add", strings.TrimSpace(descriptor.ID)}
	if name = strings.TrimSpace(name); name != "" {
		parts = append(parts, "--name", name)
	}
	if descriptor.RequiresAuth && len(descriptor.AuthEnvVars) > 0 {
		if env := strings.TrimSpace(descriptor.AuthEnvVars[0]); env != "" {
			parts = append(parts, "--api-key-env", env)
		}
	}
	if setActive {
		parts = append(parts, "--set-active")
	}
	return joinCommand(parts)
}

func UseCommand(name string) string {
	parts := []string{"zero", "providers", "use"}
	if name = strings.TrimSpace(name); name != "" {
		parts = append(parts, name)
	}
	return joinCommand(parts)
}

func CheckCommand(name string, connectivity bool) string {
	parts := []string{"zero", "providers", "check"}
	if name = strings.TrimSpace(name); name != "" {
		parts = append(parts, name)
	}
	if connectivity {
		parts = append(parts, "--connectivity")
	}
	return joinCommand(parts)
}

func MissingCredentialAction(profile config.ProviderProfile) (Action, bool) {
	return MissingCredentialActionWithDetections(profile, nil)
}

// MissingCredentialActionWithDetections is MissingCredentialAction plus a hint
// that a detected, logged-in agent-CLI harness could supply this provider's
// credentials instead of an API key (see cliCredentialHint). detections is
// typically agentcli.Detect's result; nil behaves exactly like
// MissingCredentialAction.
func MissingCredentialActionWithDetections(profile config.ProviderProfile, detections []agentcli.Detection) (Action, bool) {
	advice := credentialAdviceForProfile(profile)
	if !advice.requiresAuth || providerProfileHasCredential(profile) {
		return Action{}, false
	}

	detail := "Set an API key before using this provider."
	command := "set API_KEY in your shell"
	if advice.envVar != "" {
		detail = "Set " + advice.envVar + " to your provider API key before using this provider."
		command = "set " + advice.envVar + " in your shell"
	}
	if hint := cliCredentialHint(profile.CatalogID, detections); hint != "" {
		detail += " " + hint
	}
	return Action{
		Label:   "Set API key",
		Command: command,
		Detail:  detail,
	}, true
}

// cliCredentialHint returns an aside noting that a detected, logged-in agent
// CLI (e.g. Claude Code) could supply this catalog id's credentials instead of
// an API key — surfaced so a user who already has that CLI installed sees the
// CLI connect method as an alternative to pasting a key. Empty when no
// detection matches catalogID or none of the matches are logged in.
func cliCredentialHint(catalogID string, detections []agentcli.Detection) string {
	catalogID = strings.TrimSpace(catalogID)
	if catalogID == "" {
		return ""
	}
	for _, detection := range detections {
		if detection.Harness.CatalogID == catalogID && detection.Login == agentcli.LoggedIn {
			return "Or reuse your " + detection.Harness.DisplayName +
				" login instead — open /provider and choose \"Use " + detection.Harness.DisplayName + " login\"."
		}
	}
	return ""
}

func ProviderActions(profile config.ProviderProfile, active bool) []Action {
	return ProviderActionsWithDetections(profile, active, nil)
}

// ProviderActionsWithDetections is ProviderActions plus agent-CLI detections
// threaded through to MissingCredentialActionWithDetections.
func ProviderActionsWithDetections(profile config.ProviderProfile, active bool, detections []agentcli.Detection) []Action {
	name := strings.TrimSpace(profile.Name)
	actions := make([]Action, 0, 3)
	if name != "" && !active {
		actions = append(actions, Action{
			Label:   "Use provider",
			Command: UseCommand(name),
			Detail:  "Make " + name + " the active provider.",
		})
	}
	if name != "" {
		actions = append(actions, Action{
			Label:   "Check provider",
			Command: CheckCommand(name, false),
			Detail:  "Validate the provider profile without probing network connectivity.",
		})
	}
	if action, ok := MissingCredentialActionWithDetections(profile, detections); ok {
		actions = append(actions, action)
	}
	return actions
}

type credentialAdvice struct {
	requiresAuth bool
	envVar       string
}

func credentialAdviceForProfile(profile config.ProviderProfile) credentialAdvice {
	profileEnv := strings.TrimSpace(profile.APIKeyEnv)
	if catalogID := strings.TrimSpace(profile.CatalogID); catalogID != "" {
		if descriptor, err := providercatalog.Require(catalogID); err == nil {
			return credentialAdvice{
				requiresAuth: descriptor.RequiresAuth,
				envVar:       firstNonEmpty(profileEnv, firstAuthEnvVar(descriptor)),
			}
		}
	}

	switch effectiveProviderKind(profile) {
	case config.ProviderKindOpenAI:
		return credentialAdvice{requiresAuth: true, envVar: firstNonEmpty(profileEnv, "OPENAI_API_KEY")}
	case config.ProviderKindAnthropic:
		return credentialAdvice{requiresAuth: true, envVar: firstNonEmpty(profileEnv, "ANTHROPIC_API_KEY")}
	case config.ProviderKindGoogle:
		return credentialAdvice{requiresAuth: true, envVar: firstNonEmpty(profileEnv, "GEMINI_API_KEY")}
	default:
		return credentialAdvice{requiresAuth: profileEnv != "", envVar: profileEnv}
	}
}

func effectiveProviderKind(profile config.ProviderProfile) config.ProviderKind {
	if kind := strings.TrimSpace(string(profile.ProviderKind)); kind != "" {
		return config.ProviderKind(strings.ToLower(kind))
	}
	if provider := strings.TrimSpace(profile.Provider); provider != "" {
		return config.ProviderKind(strings.ToLower(provider))
	}
	return ""
}

// providerProfileHasCredential delegates to the canonical check so a future
// credential type added there (as APIKeyStored already was) flows into
// onboarding advice automatically. Verified equivalent for the dimensions this
// package cares about — it also does not fold in APIKeyEnv resolution.
func providerProfileHasCredential(profile config.ProviderProfile) bool {
	return profile.HasConfiguredCredential()
}

func firstAuthEnvVar(descriptor providercatalog.Descriptor) string {
	for _, env := range descriptor.AuthEnvVars {
		if env = strings.TrimSpace(env); env != "" {
			return env
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func joinCommand(parts []string) string {
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			quoted = append(quoted, commandArg(part))
		}
	}
	return strings.Join(quoted, " ")
}

func commandArg(value string) string {
	if value == "" {
		return strconv.Quote(value)
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		switch r {
		case '-', '_', '.', '/', ':', '@':
			continue
		default:
			return strconv.Quote(value)
		}
	}
	return value
}
