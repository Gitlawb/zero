package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/credstore"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/provideronboarding"
)

type providerUseOptions struct {
	name string
	json bool
}

type providerSetupOptions struct {
	catalogID string
	name      string
	model     string
	baseURL   string
	apiKeyEnv string
	setActive bool
	json      bool
}

type providerSetupPlan struct {
	CatalogID    string `json:"catalogID"`
	Name         string `json:"name"`
	AddCommand   string `json:"addCommand"`
	CheckCommand string `json:"checkCommand"`
	UseCommand   string `json:"useCommand"`
	EnvVar       string `json:"envVar"`
}

func runProvidersUse(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseProviderUseArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeProvidersHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	configPath, err := deps.userConfigPath()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	// SetActiveProvider only ever matches profiles persisted in config.json
	// (see config.ProviderPersisted), but a provider can be visible in
	// `zero providers list`/the TUI picker purely because Resolve()
	// synthesized it in-memory from an ambient env var (e.g. OPENAI_API_KEY)
	// without ever writing a row to disk. Without this check, switching to
	// that provider by name always fails with a confusing "not found" even
	// though it is genuinely usable this session (issue #707).
	persisted, err := config.ProviderPersisted(configPath, options.name)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if !persisted {
		if exit, handled := reportUnpersistedProviderUse(stdout, stderr, deps, options, configPath); handled {
			return exit
		}
	}
	cfg, err := config.SetActiveProvider(configPath, options.name)
	if err != nil {
		return writeAppError(stderr, providerMutationError(configPath, options.name, err), exitCrash)
	}
	override := activeProviderEnvOverride(deps.getenv, cfg.ActiveProvider)
	// A case-only difference is only harmless when it demonstrably selects the
	// row just written; a separate exact-case profile from project config makes
	// it a real override of a real, different provider.
	if override != "" && config.SameProviderIdentity(override, cfg.ActiveProvider) &&
		activeProviderEnvOverrideSelectsSaved(deps, configPath, cfg.ActiveProvider) {
		override = ""
	}
	// An override only becomes the effective provider if Zero can actually
	// resolve it; a stale value names nothing and fails the next resolution.
	overrideResolution := activeProviderOverrideAbsent
	var overrideResolutionErr error
	if override != "" {
		overrideResolution, overrideResolutionErr = activeProviderEnvOverrideResolution(deps, configPath, override)
	}
	if options.json {
		payload := map[string]any{
			"activeProvider": cfg.ActiveProvider,
			"configPath":     configPath,
		}
		if override != "" {
			// A JSON consumer must not read this as an effective switch either.
			payload["overriddenByEnv"] = config.ActiveProviderEnv
			payload["envProvider"] = override
			payload["envProviderResolves"] = overrideResolution.resolves()
			if overrideResolution == activeProviderOverrideDeferred {
				payload["envProviderResolution"] = "deferred"
			}
			if overrideResolution == activeProviderOverrideConfigError {
				payload["envProviderResolution"] = "config-error"
				payload["envProviderResolutionError"] = overrideResolutionErr.Error()
			}
			if overrideResolution == activeProviderOverrideResolved {
				payload["effectiveProvider"] = override
			}
		}
		if err := writePrettyJSON(stdout, payload); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Active provider set to %s\nnext: %s\n", cfg.ActiveProvider, providerCheckCommand(cfg.ActiveProvider, false)); err != nil {
		return exitCrash
	}
	if override != "" {
		note := fmt.Sprintf("Note: %s=%s is set and overrides config.json, so %s stays the active provider until you unset %s.\n", config.ActiveProviderEnv, override, override, config.ActiveProviderEnv)
		if overrideResolution == activeProviderOverrideDeferred {
			note = fmt.Sprintf("Note: %s=%s is set and overrides config.json; %s is also set, so the effective provider will be determined when Zero next resolves configuration.\n", config.ActiveProviderEnv, override, config.ProviderCommandEnv)
		} else if overrideResolution == activeProviderOverrideConfigError {
			note = fmt.Sprintf("Note: %s=%s is set and overrides config.json, but the effective provider cannot be verified because configuration is invalid: %v\n", config.ActiveProviderEnv, override, overrideResolutionErr)
		} else if overrideResolution != activeProviderOverrideResolved {
			// Naming it "effective" would be wrong: nothing resolves under that
			// name, so the next command fails rather than using it.
			note = fmt.Sprintf("Note: %s=%s is set and overrides config.json, but no provider named %s can be resolved, so Zero cannot start until you unset %s or point it at a saved provider.\n", config.ActiveProviderEnv, override, override, config.ActiveProviderEnv)
		}
		if _, err := fmt.Fprint(stderr, note); err != nil {
			return exitCrash
		}
	}
	return exitSuccess
}

type activeProviderOverrideResolution uint8

const (
	activeProviderOverrideAbsent activeProviderOverrideResolution = iota
	activeProviderOverrideResolved
	activeProviderOverrideUnresolved
	activeProviderOverrideDeferred
	activeProviderOverrideConfigError
)

func (resolution activeProviderOverrideResolution) resolves() any {
	switch resolution {
	case activeProviderOverrideResolved:
		return true
	case activeProviderOverrideUnresolved:
		return false
	default:
		return nil
	}
}

// activeProviderEnvOverride returns the ZERO_PROVIDER value when it is set and
// names a DIFFERENT spelling than the one just selected, meaning the saved
// `providers use` selection may NOT be the effective active provider until the
// env var is unset. applyEnv (resolver.go) makes ZERO_PROVIDER win over
// config.json unconditionally, so reporting the write as a plain success reads as
// a switch that silently has no effect (issue #721). Empty when nothing overrides
// (including when getenv is nil, e.g. a test that did not inject the environment).
//
// A case-only difference is NOT assumed here to name the same provider — see
// activeProviderEnvOverrideSelectsSaved, which resolves that question instead of
// guessing at it.
func activeProviderEnvOverride(getenv func(string) string, selected string) string {
	if getenv == nil {
		return ""
	}
	override := strings.TrimSpace(getenv(config.ActiveProviderEnv))
	if override == "" || override == strings.TrimSpace(selected) {
		return ""
	}
	return override
}

// activeProviderEnvOverrideSelectsSaved reports whether a case-only ZERO_PROVIDER
// difference actually lands on the row `providers use` just wrote.
//
// Folding the comparison outright was wrong: user config resolves the active row
// case-insensitively, but project config and provider commands contribute their
// own rows, and resolution then selects an exact-case match. With a workspace
// profile literally named "WORK", ZERO_PROVIDER=WORK selects THAT row — different
// credentials, different endpoint — while `zero providers use work` reported a
// clean switch with no override at all. So the suppression is granted only when
// resolution proves the env value produces the very spelling just selected.
func activeProviderEnvOverrideSelectsSaved(deps appDeps, configPath string, selected string) bool {
	resolved, err := resolveActiveProviderWithoutProviderCommand(deps, configPath)
	if err != nil {
		return false
	}
	return resolved == strings.TrimSpace(selected)
}

// resolveActiveProviderWithoutProviderCommand resolves the effective active
// provider name without running ZERO_PROVIDER_COMMAND. Provider commands are
// arbitrary external programs and `providers use` must remain a config-only
// operation, so a configured one makes the answer unknowable here (false).
func resolveActiveProviderWithoutProviderCommand(deps appDeps, configPath string) (string, error) {
	if deps.getenv != nil && strings.TrimSpace(deps.getenv(config.ProviderCommandEnv)) != "" {
		return "", fmt.Errorf("provider command resolution is deferred")
	}
	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return "", err
	}
	options, err := config.DefaultResolveOptions(workspaceRoot)
	if err != nil {
		return "", err
	}
	options.UserConfigPath = configPath
	options.ProviderCommand = ""
	resolved, err := config.Resolve(options)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resolved.ActiveProvider), nil
}

// activeProviderEnvOverrideResolution checks whether ZERO_PROVIDER resolves
// without running ZERO_PROVIDER_COMMAND. Provider commands are arbitrary
// external programs and `providers use` must remain a config-only operation;
// when one is configured, the final provider is therefore explicitly deferred
// until the next normal resolution instead of being guessed here.
func activeProviderEnvOverrideResolution(deps appDeps, configPath string, override string) (activeProviderOverrideResolution, error) {
	if deps.getenv != nil && strings.TrimSpace(deps.getenv(config.ProviderCommandEnv)) != "" {
		return activeProviderOverrideDeferred, nil
	}
	resolved, err := resolveActiveProviderWithoutProviderCommand(deps, configPath)
	if err != nil {
		if errors.Is(err, config.ErrNoActiveProvider) || errors.Is(err, config.ErrProviderRequiresModel) {
			return activeProviderOverrideUnresolved, nil
		}
		return activeProviderOverrideConfigError, err
	}
	// Fold case: resolution selects the active row case-insensitively and reports
	// the row's canonical persisted spelling, so ZERO_PROVIDER=openrouter against
	// a saved "OpenRouter" resolves to "OpenRouter". Comparing exactly called that
	// a failed override and told the user Zero could not start with it, which was
	// the opposite of true. A fold cannot report a false success here: if the
	// active row folds to the override, the override is what selected it.
	if !config.SameProviderIdentity(resolved, override) {
		return activeProviderOverrideUnresolved, nil
	}
	return activeProviderOverrideResolved, nil
}

func runProvidersSetup(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseProviderSetupArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeProvidersHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	profile, err := providerProfileForAdd(providerAddOptions{
		catalogID: options.catalogID,
		name:      options.name,
		model:     options.model,
		baseURL:   options.baseURL,
		apiKeyEnv: options.apiKeyEnv,
		setActive: options.setActive,
	})
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	descriptor, err := providercatalog.Require(profile.CatalogID)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	plan := providerSetupPlan{
		CatalogID:    profile.CatalogID,
		Name:         profile.Name,
		AddCommand:   providerSetupAddCommand(options, profile),
		CheckCommand: providerCheckCommand(profile.Name, true),
		EnvVar:       providerSetupEnvVar(descriptor, profile),
	}
	if !options.setActive {
		plan.UseCommand = providerUseCommand(profile.Name)
	}

	if options.json {
		if err := writePrettyJSON(stdout, plan); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatProviderSetupPlan(plan)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func parseProviderUseArgs(args []string) (providerUseOptions, bool, error) {
	options := providerUseOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown flag %q", arg)}
		default:
			if options.name != "" {
				return options, false, execUsageError{fmt.Sprintf("unexpected argument %q", arg)}
			}
			options.name = arg
		}
	}
	if strings.TrimSpace(options.name) == "" {
		return options, false, execUsageError{"provider name is required"}
	}
	return options, false, nil
}

func parseProviderSetupArgs(args []string) (providerSetupOptions, bool, error) {
	options := providerSetupOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case arg == "--set-active":
			options.setActive = true
		case arg == "--name":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.name = value
			index = next
		case strings.HasPrefix(arg, "--name="):
			value, err := requiredInlineFlagValue(arg, "--name")
			if err != nil {
				return options, false, err
			}
			options.name = value
		case arg == "--model":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.model = value
			index = next
		case strings.HasPrefix(arg, "--model="):
			value, err := requiredInlineFlagValue(arg, "--model")
			if err != nil {
				return options, false, err
			}
			options.model = value
		case arg == "--base-url":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.baseURL = value
			index = next
		case strings.HasPrefix(arg, "--base-url="):
			value, err := requiredInlineFlagValue(arg, "--base-url")
			if err != nil {
				return options, false, err
			}
			options.baseURL = value
		case arg == "--api-key-env":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.apiKeyEnv = value
			index = next
		case strings.HasPrefix(arg, "--api-key-env="):
			value, err := requiredInlineFlagValue(arg, "--api-key-env")
			if err != nil {
				return options, false, err
			}
			options.apiKeyEnv = value
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown flag %q", arg)}
		default:
			if options.catalogID != "" {
				return options, false, execUsageError{fmt.Sprintf("unexpected argument %q", arg)}
			}
			options.catalogID = arg
		}
	}
	if strings.TrimSpace(options.catalogID) == "" {
		return options, false, execUsageError{"provider catalog id is required"}
	}
	return options, false, nil
}

func providerSetupAddCommand(options providerSetupOptions, profile config.ProviderProfile) string {
	parts := []string{"zero", "providers", "add", profile.CatalogID}
	if strings.TrimSpace(options.name) != "" {
		parts = append(parts, "--name", options.name)
	}
	if strings.TrimSpace(options.model) != "" {
		parts = append(parts, "--model", options.model)
	}
	if strings.TrimSpace(options.baseURL) != "" {
		parts = append(parts, "--base-url", options.baseURL)
	}
	if apiKeyEnv := firstNonEmptyCLI(options.apiKeyEnv, profile.APIKeyEnv); apiKeyEnv != "" {
		parts = append(parts, "--api-key-env", apiKeyEnv)
	}
	if options.setActive {
		parts = append(parts, "--set-active")
	}
	return providerCommand(parts...)
}

func providerSetupEnvVar(descriptor providercatalog.Descriptor, profile config.ProviderProfile) string {
	if !descriptor.RequiresAuth || descriptor.Local {
		return ""
	}
	if envVar := strings.TrimSpace(profile.APIKeyEnv); envVar != "" {
		return envVar
	}
	for _, envVar := range descriptor.AuthEnvVars {
		if envVar = strings.TrimSpace(envVar); envVar != "" {
			return envVar
		}
	}
	return ""
}

func formatProviderSetupPlan(plan providerSetupPlan) string {
	lines := []string{"Provider setup plan"}
	if plan.EnvVar != "" {
		lines = append(lines, "Set "+plan.EnvVar+" to your API key before running connectivity checks.")
	}
	lines = append(lines, "Next commands:", "  "+plan.AddCommand, "  "+plan.CheckCommand)
	if plan.UseCommand != "" {
		lines = append(lines, "  "+plan.UseCommand)
	}
	return strings.Join(lines, "\n")
}

func providerUseCommand(name string) string {
	return provideronboarding.UseCommand(name)
}

func providerCheckCommand(name string, connectivity bool) string {
	return provideronboarding.CheckCommand(name, connectivity)
}

func providerCommand(parts ...string) string {
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			quoted = append(quoted, providerCommandArg(part))
		}
	}
	return strings.Join(quoted, " ")
}

func providerCommandArg(value string) string {
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

type providerNamesOptions struct {
	names []string
	json  bool
}

func parseProviderNamesArgs(args []string, want int, usage string) (providerNamesOptions, bool, error) {
	options := providerNamesOptions{}
	for _, arg := range args {
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown flag %q", arg)}
		default:
			options.names = append(options.names, arg)
		}
	}
	if len(options.names) != want {
		return options, false, execUsageError{usage}
	}
	return options, false, nil
}

// runProvidersRemove deletes a saved provider profile and its stored API key.
// The OAuth token (if any) is kept — logins outlive profiles so re-adding the
// provider needs no new browser round-trip; `zero auth logout <name>` removes it.
func runProvidersRemove(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseProviderNamesArgs(args, 1, "usage: zero providers remove <name>")
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeProvidersHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	configPath, err := deps.userConfigPath()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	name := options.names[0]
	// RemoveProvider only ever matches profiles persisted in config.json (see
	// config.ProviderPersisted), but a provider can be visible in
	// `zero providers list`/the TUI picker purely because Resolve()
	// synthesized it in-memory from an ambient env var (e.g. OPENAI_API_KEY)
	// without ever writing a row to disk. Without this check, deleting that
	// provider by name always fails with a confusing "not found" even though
	// it is genuinely visible/usable this session (issue #707).
	persisted, err := config.ProviderPersisted(configPath, name)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if !persisted {
		if exit, handled := reportUnpersistedProviderRemove(stdout, stderr, deps, name, options.json, configPath); handled {
			return exit
		}
	}
	// Read the row BEFORE removal so we know whether it owned the stored-key
	// marker — RemoveProvider's returned cfg no longer has it, and the
	// credential store's secret is keyed case-folded, so removing a keyed row
	// while a case-variant sibling survives must carry the marker over
	// instead of silently orphaning the still-shared secret.
	before, hadBefore, err := config.ProviderRow(configPath, name)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	credentialCandidates, _, err := config.ProviderCredentialCandidates(configPath, name)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	// Decide up front whether a sibling row will survive this removal holding
	// the SAME credential-store identity, because that decides what happens to
	// the shared secret: hand the marker over, or delete the key outright.
	survivor, survivorSurvives, err := survivingCaseVariantProviderName(configPath, name)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	cfg, err := config.RemoveProvider(configPath, name)
	if err != nil {
		return writeAppError(stderr, providerMutationError(configPath, name, err), exitCrash)
	}
	// The marker handoff can only run AFTER the removal, not as a transaction
	// around it: while both case-variant rows are present the config is
	// ambiguous, and writeConfigFile rejects every write against it — removing
	// one row is precisely the repair. So the window is real, and the command
	// reports it as a partial failure (nonzero below) instead of success.
	//
	// Delete the key from the store BESIDE the config being edited — the same
	// store setup/rename write to — not the default-path store, so a
	// non-default config path cannot leave the encrypted key behind. Skipped
	// when a case variant survives: that row keeps reading the shared entry.
	keyRemoved := false
	var keyErr error
	markerTransferFailed := false
	if survivorSurvives {
		if hadBefore && before.APIKeyStored {
			if _, err := config.TransferProviderAPIKeyStoredMarker(configPath, survivor); err != nil {
				keyErr = err
				markerTransferFailed = true
			}
		}
		credentialCandidates = slices.DeleteFunc(credentialCandidates, func(candidate string) bool {
			return config.SameProviderIdentity(candidate, name)
		})
	}
	keyRemoved, cleanupErr := removeStoredProviderKeysAt(configPath, credentialCandidates)
	if cleanupErr != nil {
		if keyErr != nil {
			keyErr = fmt.Errorf("%v; credential cleanup: %w", keyErr, cleanupErr)
		} else {
			keyErr = cleanupErr
		}
	}
	if options.json {
		payload := map[string]any{
			"removed":        name,
			"keyRemoved":     keyRemoved,
			"activeProvider": cfg.ActiveProvider,
			"configPath":     configPath,
		}
		if keyErr != nil {
			// A lingering secret must not read as a clean removal.
			payload["keyError"] = keyErr.Error()
		}
		if err := writePrettyJSON(stdout, payload); err != nil {
			return exitCrash
		}
		// A failed handoff left the survivor unable to reach the shared key.
		// Scripts must not read that as a completed removal.
		if markerTransferFailed {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Removed provider %s\n", name); err != nil {
		return exitCrash
	}
	if keyErr != nil {
		warning := "its stored API key could not be deleted and remains in the credential store"
		if markerTransferFailed {
			warning = fmt.Sprintf("the stored API key marker could not be handed to %s, which shares its credential entry, so the shared key is now unreachable", survivor)
		}
		if _, err := fmt.Fprintf(stderr, "warning: %s: %v\n", warning, keyErr); err != nil {
			return exitCrash
		}
	} else if keyRemoved {
		if _, err := fmt.Fprintln(stdout, "Deleted its stored API key."); err != nil {
			return exitCrash
		}
	}
	if active := strings.TrimSpace(cfg.ActiveProvider); active != "" {
		if _, err := fmt.Fprintf(stdout, "Active provider: %s\n", active); err != nil {
			return exitCrash
		}
	} else {
		if _, err := fmt.Fprintln(stdout, "No providers remain — run zero setup to add one."); err != nil {
			return exitCrash
		}
	}
	if markerTransferFailed {
		return exitCrash
	}
	return exitSuccess
}

// removeStoredProviderKeysAt deletes provider API keys from the credential
// store co-located with configPath (the store SecureProviderProfile captured
// them into and RenameProvider migrates within).
func removeStoredProviderKeysAt(configPath string, providers []string) (bool, error) {
	store, err := config.ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		return false, err
	}
	removed := false
	for _, provider := range providers {
		candidateRemoved, err := store.Delete(provider)
		if err != nil {
			return removed, err
		}
		removed = removed || candidateRemoved
	}
	return removed, nil
}

// survivingCaseVariantProviderName returns the exact name of another persisted
// row that shares name's CREDENTIAL-STORE identity — a case-only variant that
// will still read the same stored secret once name's row is gone — and whether
// one was found. config.RemoveProvider matches its row exactly, so any row with
// a different exact spelling survives.
//
// The comparison is credstore.NormalizeProvider, not strings.EqualFold, because
// only the former is the store's own equivalence rule. Unicode case folding
// equates "s" and "ſ" while strings.ToLower does not, so EqualFold would let
// `providers remove s` skip deleting the key and hand the marker to "ſ", whose
// lookup uses a different store entry — orphaning the secret and leaving the
// survivor with a marker pointing at nothing.
func survivingCaseVariantProviderName(configPath string, name string) (string, bool, error) {
	names, err := config.PersistedProviderNames(configPath)
	if err != nil {
		return "", false, err
	}
	removed := strings.TrimSpace(name)
	target := credstore.NormalizeProvider(removed)
	for _, candidate := range names {
		if candidate == removed {
			continue
		}
		if credstore.NormalizeProvider(candidate) == target {
			return candidate, true, nil
		}
	}
	return "", false, nil
}

// runProvidersRename renames a saved provider profile, migrating its stored
// API key and the activeProvider pointer along with it (config.RenameProvider).
func runProvidersRename(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseProviderNamesArgs(args, 2, "usage: zero providers rename <old> <new>")
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeProvidersHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	configPath, err := deps.userConfigPath()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	oldName := options.names[0]
	persisted, err := config.ProviderPersisted(configPath, oldName)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if !persisted {
		if exit, handled := reportUnpersistedProviderRename(stdout, stderr, deps, oldName, options.json, configPath); handled {
			return exit
		}
	}
	cfg, err := config.RenameProvider(configPath, options.names[0], options.names[1])
	if err != nil {
		return writeAppError(stderr, providerMutationError(configPath, options.names[0], err), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, map[string]any{
			"renamed":        map[string]string{"from": options.names[0], "to": options.names[1]},
			"activeProvider": cfg.ActiveProvider,
			"configPath":     configPath,
		}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Renamed provider %s to %s\n", options.names[0], options.names[1]); err != nil {
		return exitCrash
	}
	return exitSuccess
}

// providerResolvedByName reports whether name matches a provider in a
// resolved provider list — used to tell a genuinely unknown name (a typo)
// apart from a real, env-derived provider that just has no config.json row.
func providerResolvedByName(providers []config.ProviderProfile, name string) bool {
	name = strings.TrimSpace(name)
	matches := 0
	for _, provider := range providers {
		if config.SameProviderIdentity(provider.Name, name) || config.SameProviderIdentity(provider.CatalogID, name) {
			matches++
		}
	}
	return matches == 1
}

// providerMutationError renders a provider mutator's failure, naming the saved
// profile when the config owns what the user asked for under another spelling.
// The mutators match exactly on purpose — one credential identity, one row — so
// `zero providers use SAVED` against a saved "saved", or a catalog id against a
// row saved under a different name, correctly fails not-found. Saying only that
// leaves the user to guess what they got wrong, and `zero auth logout` already
// explains the same situation; this keeps the provider commands consistent with
// it, and goes one better by naming the spelling that works.
func providerMutationError(configPath, name string, err error) string {
	message := err.Error()
	if !strings.Contains(message, "not found") {
		return message
	}
	canonical, owned, lookupErr := config.PersistedProviderIdentity(configPath, name)
	if lookupErr != nil || !owned || canonical == strings.TrimSpace(name) {
		return message
	}
	return fmt.Sprintf("%s; the saved profile is named %q and provider names are matched exactly", message, canonical)
}

// configOwnsProviderIdentity reports whether a persisted row already owns name —
// under any capitalization, or as the catalog id of a row saved under a
// different name. Every runtime-only report below must consult it first: its
// message claims the provider exists only because an environment variable is
// set, and that is simply false for a provider the config owns. The caller falls
// through to the command's own not-found handling instead, which is the right
// answer for a mis-addressed saved profile — `zero providers use openrouter`
// against a saved {name: "my-router", catalogId: "openrouter"} is the wrong name
// for a real profile, not an env-derived provider.
//
// failed reports that reading the config failed; the error is already on stderr
// and exit carries the code. owned is meaningful only when failed is false.
func configOwnsProviderIdentity(stderr io.Writer, configPath, name string) (owned bool, exit int, failed bool) {
	if _, ok, err := config.PersistedProviderIdentity(configPath, name); err != nil {
		return false, writeAppError(stderr, err.Error(), exitCrash), true
	} else if ok {
		return true, exitSuccess, false
	}
	return false, exitSuccess, false
}

// reportUnpersistedProviderUse handles `zero providers use <name>` for a
// provider that is not persisted in config.json. If it's not resolvable at
// all (an unknown/misspelled name), it returns handled=false so the caller
// falls through to SetActiveProvider's real "not found" error. If it IS
// resolvable — an env-derived profile (e.g. ambient OPENAI_API_KEY) —
// SetActiveProvider would only ever fail "not found" against it, so this
// reports the situation plainly instead of that confusing error (issue
// #707).
func reportUnpersistedProviderUse(stdout, stderr io.Writer, deps appDeps, options providerUseOptions, configPath string) (int, bool) {
	if owned, exit, failed := configOwnsProviderIdentity(stderr, configPath, options.name); failed {
		return exit, true
	} else if owned {
		return exitSuccess, false
	}
	resolved, exitCode := resolveCommandCenterConfig(stderr, deps)
	if exitCode != exitSuccess {
		// resolveCommandCenterConfig already wrote its own error to stderr;
		// stop here instead of letting the caller try SetActiveProvider too.
		return exitCode, true
	}
	if !providerResolvedByName(resolved.Providers, options.name) {
		return exitCode, false
	}
	message := fmt.Sprintf(
		"Provider %q is not saved in config.json (likely set via an environment variable), so there is no saved profile to switch to.\nIt is available whenever its environment variable is set, but is only active when selected (for example via ZERO_PROVIDER); unset its environment variable to stop Zero from detecting it automatically.",
		options.name,
	)
	if options.json {
		if err := writePrettyJSON(stdout, map[string]any{
			"activeProvider": resolved.ActiveProvider,
			"configPath":     configPath,
			"persisted":      false,
			"message":        message,
		}); err != nil {
			return exitCrash, true
		}
		return exitSuccess, true
	}
	if _, err := fmt.Fprintln(stdout, message); err != nil {
		return exitCrash, true
	}
	return exitSuccess, true
}

// reportUnpersistedProviderRemove handles `zero providers remove <name>` for
// a provider that is not persisted in config.json, mirroring the TUI
// provider manager's delete handling (internal/tui/provider_manager.go). If
// name isn't resolvable at all, it returns handled=false so the caller falls
// through to RemoveProvider's real "not found" error.
func reportUnpersistedProviderRemove(stdout, stderr io.Writer, deps appDeps, name string, jsonOutput bool, configPath string) (int, bool) {
	if owned, exit, failed := configOwnsProviderIdentity(stderr, configPath, name); failed {
		return exit, true
	} else if owned {
		return exitSuccess, false
	}
	resolved, exitCode := resolveCommandCenterConfig(stderr, deps)
	if exitCode != exitSuccess {
		return exitCode, true
	}
	if !providerResolvedByName(resolved.Providers, name) {
		return exitCode, false
	}
	message := fmt.Sprintf(
		"Provider %q is not saved in config.json (likely set via an environment variable) — nothing to remove there.\nUnset its environment variable to stop Zero from detecting it automatically.",
		name,
	)
	if jsonOutput {
		if err := writePrettyJSON(stdout, map[string]any{
			"removed":        "",
			"keyRemoved":     false,
			"activeProvider": resolved.ActiveProvider,
			"configPath":     configPath,
			"persisted":      false,
			"message":        message,
		}); err != nil {
			return exitCrash, true
		}
		return exitSuccess, true
	}
	if _, err := fmt.Fprintln(stdout, message); err != nil {
		return exitCrash, true
	}
	return exitSuccess, true
}

func reportUnpersistedProviderRename(stdout, stderr io.Writer, deps appDeps, name string, jsonOutput bool, configPath string) (int, bool) {
	if owned, exit, failed := configOwnsProviderIdentity(stderr, configPath, name); failed {
		return exit, true
	} else if owned {
		return exitSuccess, false
	}
	resolved, exitCode := resolveCommandCenterConfig(stderr, deps)
	if exitCode != exitSuccess {
		return exitCode, true
	}
	if !providerResolvedByName(resolved.Providers, name) {
		return exitCode, false
	}
	message := fmt.Sprintf(
		"Provider %q is not saved in config.json (likely set via an environment variable), so there is no saved profile to rename.",
		name,
	)
	if jsonOutput {
		if err := writePrettyJSON(stdout, map[string]any{
			"renamed":    nil,
			"configPath": configPath,
			"persisted":  false,
			"message":    message,
		}); err != nil {
			return exitCrash, true
		}
		return exitSuccess, true
	}
	if _, err := fmt.Fprintln(stdout, message); err != nil {
		return exitCrash, true
	}
	return exitSuccess, true
}
