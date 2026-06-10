package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/Gitlawb/zero/internal/config"
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
	cfg, err := config.SetActiveProvider(configPath, options.name)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	if options.json {
		if err := writePrettyJSON(stdout, map[string]any{
			"activeProvider": cfg.ActiveProvider,
			"configPath":     configPath,
		}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Active provider set to %s\nnext: %s\n", cfg.ActiveProvider, providerCheckCommand(cfg.ActiveProvider, false)); err != nil {
		return exitCrash
	}
	return exitSuccess
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
