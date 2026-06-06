package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/zerocommands"
)

type commandCenterOptions struct {
	json              bool
	provider          string
	includeDeprecated bool
}

type configSummary = zerocommands.ConfigSnapshot
type providerSummary = zerocommands.ProviderSnapshot
type modelSummary = zerocommands.ModelSnapshot

func runConfig(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseCommandCenterArgs(args, false)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeConfigHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	resolved, exitCode := resolveCommandCenterConfig(stderr, deps)
	if exitCode != exitSuccess {
		return exitCode
	}
	summary := summarizeConfig(resolved)
	if options.json {
		if err := writePrettyJSON(stdout, summary); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatConfigSummary(summary)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runProviders(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	command := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = strings.ToLower(strings.TrimSpace(args[0]))
		args = args[1:]
	}
	if command == "help" {
		if err := writeProvidersHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if command != "list" && command != "current" {
		return writeExecUsageError(stderr, fmt.Sprintf("unknown providers command %q", command))
	}
	options, help, err := parseCommandCenterArgs(args, false)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeProvidersHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	resolved, exitCode := resolveCommandCenterConfig(stderr, deps)
	if exitCode != exitSuccess {
		return exitCode
	}
	summary := summarizeConfig(resolved)
	providers := summary.Providers
	if command == "current" {
		providers = []providerSummary{}
		for _, provider := range summary.Providers {
			if provider.Active {
				providers = append(providers, provider)
				break
			}
		}
	}
	if options.json {
		if command == "current" {
			var provider any
			if len(providers) > 0 {
				provider = providers[0]
			}
			if err := writePrettyJSON(stdout, map[string]any{"provider": provider}); err != nil {
				return exitCrash
			}
			return exitSuccess
		}
		if err := writePrettyJSON(stdout, map[string]any{"providers": providers}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatProviderSummaries(command, providers)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runModels(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "list" || args[0] == "ls") {
		args = args[1:]
	}
	options, help, err := parseCommandCenterArgs(args, true)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeModelsHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	models, err := listModelSummaries(registry, options)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if options.json {
		if err := writePrettyJSON(stdout, map[string]any{"models": models}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatModelSummaries(models)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func resolveCommandCenterConfig(stderr io.Writer, deps appDeps) (config.ResolvedConfig, int) {
	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return config.ResolvedConfig{}, writeExecUsageError(stderr, err.Error())
	}
	resolved, err := deps.resolveConfig(workspaceRoot, config.Overrides{})
	if err != nil {
		return config.ResolvedConfig{}, writeAppError(stderr, err.Error(), exitProvider)
	}
	return resolved, exitSuccess
}

func parseCommandCenterArgs(args []string, allowModelFilters bool) (commandCenterOptions, bool, error) {
	options := commandCenterOptions{}
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--json":
			options.json = true
		case allowModelFilters && arg == "--include-deprecated":
			options.includeDeprecated = true
		case allowModelFilters && arg == "--provider":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.provider = value
			index = next
		case allowModelFilters && strings.HasPrefix(arg, "--provider="):
			options.provider = strings.TrimSpace(strings.TrimPrefix(arg, "--provider="))
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown flag %q", arg)}
		default:
			return options, false, execUsageError{fmt.Sprintf("unexpected argument %q", arg)}
		}
	}
	return options, false, nil
}

func summarizeConfig(resolved config.ResolvedConfig) configSummary {
	summary := zerocommands.ConfigSnapshotFromResolved(resolved)
	sort.SliceStable(summary.Providers, func(i int, j int) bool {
		if summary.Providers[i].Active != summary.Providers[j].Active {
			return summary.Providers[i].Active
		}
		return summary.Providers[i].Name < summary.Providers[j].Name
	})
	return summary
}

func listModelSummaries(registry modelregistry.Registry, options commandCenterOptions) ([]modelSummary, error) {
	summaries, err := zerocommands.ModelSnapshots(registry, zerocommands.ModelSnapshotOptions{
		Provider:          modelregistry.ProviderKind(strings.TrimSpace(strings.ToLower(options.provider))),
		IncludeDeprecated: options.includeDeprecated,
	})
	if err != nil {
		return nil, execUsageError{err.Error()}
	}
	sort.SliceStable(summaries, func(i int, j int) bool {
		if summaries[i].Provider == summaries[j].Provider {
			return summaries[i].ID < summaries[j].ID
		}
		return summaries[i].Provider < summaries[j].Provider
	})
	return summaries, nil
}

func formatConfigSummary(summary configSummary) string {
	lines := []string{
		"Config",
		"runtime: " + summary.Runtime,
		"active provider: " + displayCLIValue(summary.ActiveProvider, "none"),
		fmt.Sprintf("max turns: %d", summary.MaxTurns),
		"providers:",
	}
	if len(summary.Providers) == 0 {
		lines = append(lines, "  (none)")
	}
	for _, provider := range summary.Providers {
		lines = append(lines, "  "+formatProviderLine(provider))
	}
	return strings.Join(lines, "\n")
}

func formatProviderSummaries(command string, providers []providerSummary) string {
	title := "Providers"
	if command == "current" {
		title = "Provider"
	}
	lines := []string{title}
	if len(providers) == 0 {
		lines = append(lines, "  (none)")
		return strings.Join(lines, "\n")
	}
	for _, provider := range providers {
		if command == "current" {
			lines = append(lines,
				"name: "+displayCLIValue(provider.Name, "none"),
				"kind: "+displayCLIValue(provider.ProviderKind, "unknown"),
				"model: "+displayCLIValue(provider.Model, "none"),
				"api model: "+displayCLIValue(provider.APIModel, "unknown"),
				"base url: "+displayCLIValue(provider.BaseURL, "default"),
				"api key: "+apiKeyState(provider.APIKeySet),
			)
			if provider.Message != "" {
				lines = append(lines, "status: "+provider.Status+" - "+provider.Message)
			}
			continue
		}
		lines = append(lines, "  "+formatProviderLine(provider))
	}
	return strings.Join(lines, "\n")
}

func formatProviderLine(provider providerSummary) string {
	marker := " "
	if provider.Active {
		marker = "*"
	}
	line := fmt.Sprintf("%s %s [%s] model=%s apiModel=%s api key: %s", marker, displayCLIValue(provider.Name, "none"), displayCLIValue(provider.ProviderKind, "unknown"), displayCLIValue(provider.Model, "none"), displayCLIValue(provider.APIModel, "unknown"), apiKeyState(provider.APIKeySet))
	if provider.Message != "" {
		line += " (" + provider.Status + ": " + provider.Message + ")"
	}
	return line
}

func formatModelSummaries(models []modelSummary) string {
	lines := []string{"Models"}
	if len(models) == 0 {
		lines = append(lines, "  (none)")
		return strings.Join(lines, "\n")
	}
	for _, model := range models {
		lines = append(lines, fmt.Sprintf("  %s [%s] ctx=%d out=%d - %s", model.ID, model.Provider, model.ContextWindow, model.MaxOutputTokens, model.DisplayName))
	}
	return strings.Join(lines, "\n")
}

func apiKeyState(set bool) string {
	if set {
		return "set"
	}
	return "not set"
}

func displayCLIValue(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func writeConfigHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero config [flags]

Inspects resolved Go configuration without printing secrets.

Flags:
      --json      Print JSON summary
  -h, --help      Show this help
`)
	return err
}

func writeModelsHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero models list [flags]

Lists Zero model registry entries.

Flags:
      --json                  Print JSON model list
      --provider <provider>   Filter by openai, anthropic, google, or openai-compatible
      --include-deprecated    Include deprecated models
  -h, --help                  Show this help
`)
	return err
}

func writeProvidersHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero providers current [flags]
  zero providers list [flags]

Inspects resolved provider profiles without printing secrets.

Flags:
      --json      Print JSON summary
  -h, --help      Show this help
`)
	return err
}
