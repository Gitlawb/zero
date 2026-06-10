package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/tui"
)

type setupOptions struct {
	catalogID string
	name      string
	model     string
	baseURL   string
	apiKeyEnv string
	json      bool
}

func runSetup(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseSetupArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeSetupHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if strings.TrimSpace(options.catalogID) == "" {
		return runInteractiveTUIWithSetup(stderr, deps, "", true)
	}

	result, err := saveSetupProvider(deps, tui.SetupSelection{
		CatalogID: options.catalogID,
		Model:     options.model,
	}, setupSaveOptions{
		name:      options.name,
		baseURL:   options.baseURL,
		apiKeyEnv: options.apiKeyEnv,
	})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, map[string]any{
			"configPath": result.ConfigPath,
			"provider":   result.Provider.Name,
			"model":      result.Provider.Model,
			"catalogID":  result.Provider.CatalogID,
		}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatSetupComplete(result)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func parseSetupArgs(args []string) (setupOptions, bool, error) {
	options := setupOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
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
	return options, false, nil
}

type setupSaveOptions struct {
	name      string
	baseURL   string
	apiKeyEnv string
}

func saveSetupProvider(deps appDeps, selection tui.SetupSelection, options setupSaveOptions) (tui.SetupResult, error) {
	profile, err := providerProfileForAdd(providerAddOptions{
		catalogID: selection.CatalogID,
		name:      options.name,
		model:     firstNonEmptyCLI(selection.Model),
		baseURL:   options.baseURL,
		apiKeyEnv: options.apiKeyEnv,
		setActive: true,
	})
	if err != nil {
		return tui.SetupResult{}, err
	}
	if apiKey := strings.TrimSpace(selection.APIKey); apiKey != "" {
		profile.APIKey = apiKey
		profile.APIKeyEnv = ""
	}
	configPath, err := deps.userConfigPath()
	if err != nil {
		return tui.SetupResult{}, err
	}
	if _, err := config.UpsertProvider(configPath, profile, true); err != nil {
		return tui.SetupResult{}, err
	}
	return tui.SetupResult{ConfigPath: configPath, Provider: profile}, nil
}

func setupProviderOptions() []tui.SetupProviderOption {
	descriptors := providercatalog.All()
	options := make([]tui.SetupProviderOption, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if !providercatalog.RuntimeSupported(descriptor) {
			continue
		}
		options = append(options, tui.SetupProviderOption{
			ID:           descriptor.ID,
			Name:         descriptor.Name,
			DefaultModel: descriptor.DefaultModel,
			EnvVar:       setupProviderEnvVar(descriptor),
			RequiresAuth: descriptor.RequiresAuth,
			Local:        descriptor.Local,
		})
	}
	return options
}

func setupProviderEnvVar(descriptor providercatalog.Descriptor) string {
	for _, envVar := range descriptor.AuthEnvVars {
		if envVar = strings.TrimSpace(envVar); envVar != "" {
			return envVar
		}
	}
	return ""
}

func setupRequired(resolved config.ResolvedConfig) bool {
	if !config.HasProviderProfile(resolved.Provider) {
		return true
	}
	_, missing := setupMissingCredentialEnv(resolved.Provider)
	return missing
}

func formatSetupComplete(result tui.SetupResult) string {
	lines := []string{"Zero setup complete"}
	if result.Provider.Name != "" {
		lines = append(lines, "provider: "+result.Provider.Name)
	}
	if result.Provider.Model != "" {
		lines = append(lines, "model: "+result.Provider.Model)
	}
	if result.ConfigPath != "" {
		lines = append(lines, "config: "+result.ConfigPath)
	}
	if envVar, ok := setupMissingCredentialEnv(result.Provider); ok {
		if envVar != "" {
			lines = append(lines, "next: set "+envVar+" in your shell")
		} else {
			lines = append(lines, "next: set provider credentials in your shell")
		}
	}
	lines = append(lines, "next: "+setupCheckCommand(result.Provider.Name), "next: zero")
	return strings.Join(lines, "\n")
}

func setupMissingCredentialEnv(profile config.ProviderProfile) (string, bool) {
	if providerProfileHasCredential(profile) {
		return "", false
	}
	if catalogID := strings.TrimSpace(profile.CatalogID); catalogID != "" {
		descriptor, err := providercatalog.Require(catalogID)
		if err != nil || !descriptor.RequiresAuth {
			return "", false
		}
		return firstNonEmptyCLI(profile.APIKeyEnv, setupProviderEnvVar(descriptor)), true
	}

	switch normalizedSetupProviderKind(profile) {
	case config.ProviderKindOpenAI, config.ProviderKindOpenAICompatible:
		return firstNonEmptyCLI(profile.APIKeyEnv, "OPENAI_API_KEY"), true
	case config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat:
		return firstNonEmptyCLI(profile.APIKeyEnv, "ANTHROPIC_API_KEY"), true
	case config.ProviderKindGoogle:
		return firstNonEmptyCLI(profile.APIKeyEnv, "GEMINI_API_KEY"), true
	default:
		if strings.TrimSpace(profile.APIKeyEnv) != "" {
			return strings.TrimSpace(profile.APIKeyEnv), true
		}
		return "", false
	}
}

func normalizedSetupProviderKind(profile config.ProviderProfile) config.ProviderKind {
	if kind := strings.TrimSpace(string(profile.ProviderKind)); kind != "" {
		return config.ProviderKind(strings.ToLower(kind))
	}
	if provider := strings.TrimSpace(profile.Provider); provider != "" {
		return config.ProviderKind(strings.ToLower(provider))
	}
	return ""
}

func setupCheckCommand(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "zero providers check --connectivity"
	}
	return "zero providers check " + setupCommandArg(name) + " --connectivity"
}

func setupCommandArg(value string) string {
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

func writeSetupHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero setup [provider] [flags]

Guides first-run Zero setup. Without a provider, opens the setup UI. With a
provider catalog id, writes that provider as the active provider.

Examples:
  zero setup
  zero setup openai --api-key-env OPENAI_API_KEY
  zero setup ollama
  zero setup ollama-cloud

Flags:
      --name <name>             Provider profile name
      --model <model>           Override the default model
      --base-url <url>          Override provider base URL
      --api-key-env <name>      Store an API key environment variable name
      --json                    Print machine-readable setup result
  -h, --help                    Show this help
`)
	return err
}
