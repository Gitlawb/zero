package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gitlawb/zero/internal/agentcli"
	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providermodeldiscovery"
)

type providerModelsOptions struct {
	name string
	json bool
}

// runProvidersModels lists the models a saved provider actually serves by probing
// its live model-discovery endpoint (e.g. an OpenAI-compatible `/v1/models`). It
// works for custom OpenAI-/Anthropic-compatible providers too: discovery runs off
// the profile's base URL + credentials, so a self-hosted endpoint serving a dozen
// models no longer needs a config object per model — configure the provider once,
// then run any listed model with `zero exec --model <id>` (Zero passes unknown
// model ids through to the provider).
func runProvidersModels(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseProviderModelsArgs(args)
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
	profile, err := selectProviderForCheck(resolved, options.name)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	ctx, stop := signalContext()
	defer stop()
	models, err := deps.discoverProviderModels(ctx, discoveryCredentialProfile(profile, deps.extractAgentCLICredentials))
	if err != nil {
		return writeAppError(stderr, err.Error(), exitProvider)
	}

	if options.json {
		items := make([]map[string]any, 0, len(models))
		for _, model := range models {
			entry := map[string]any{"id": model.ID}
			if description := strings.TrimSpace(model.Description); description != "" {
				entry["description"] = description
			}
			items = append(items, entry)
		}
		payload := map[string]any{
			"provider": profile.Name,
			"count":    len(items),
			"models":   items,
		}
		if err := writePrettyJSON(stdout, payload); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = "provider"
	}
	if _, err := fmt.Fprintf(stdout, "Provider models (%s)\n", name); err != nil {
		return exitCrash
	}
	for _, model := range models {
		line := strings.TrimSpace(model.ID)
		if description := strings.TrimSpace(model.Description); description != "" {
			line += " — " + description
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return exitCrash
		}
	}
	suffix := "s"
	if len(models) == 1 {
		suffix = ""
	}
	if _, err := fmt.Fprintf(stdout, "%d model%s discovered\n", len(models), suffix); err != nil {
		return exitCrash
	}
	if len(models) > 0 {
		if _, err := fmt.Fprintf(stdout, "next: zero exec %q --model %s\n", "hello", setupCommandArg(models[0].ID)); err != nil {
			return exitCrash
		}
	}
	return exitSuccess
}

// discoveryCredentialProfile resolves the profile's API key the same way the
// runtime does — inline, then the stored credential, then the configured env var —
// so a `providers models` probe authenticates exactly like a real request. Mirrors
// discoveredModelContextWindow's credential resolution. extractCreds is
// deps.extractAgentCLICredentials (agentcli.ExtractCredentials in production);
// injectable so the AuthCLI branch is testable without a real PATH/keychain.
func discoveryCredentialProfile(profile config.ProviderProfile, extractCreds func(agentcli.Harness, agentcli.Deps) (agentcli.Credentials, bool, error)) config.ProviderProfile {
	authed := profile
	// An AuthCLI profile (e.g. Claude Code) has no API key: its credential is a
	// live bearer read from the harness's local store. Resolve it first and stamp
	// it onto the discovery request so `providers models` probes exactly like a
	// real request. A stored API key is used verbatim; an OAuth access token
	// becomes an Authorization: Bearer header (plus the anthropic OAuth beta the
	// live provider sends). Non-AuthCLI profiles fall through to the key paths.
	if strings.TrimSpace(authed.AuthCLI) != "" {
		if harness, ok := agentcli.Lookup(authed.AuthCLI); ok && harness.CatalogID != "" {
			if creds, ok, err := extractCreds(harness, agentcli.Deps{}); err == nil && ok {
				authed = applyAuthCLIDiscoveryCredential(authed, harness.CatalogID, creds)
			}
		}
	}
	if strings.TrimSpace(authed.APIKey) == "" && strings.TrimSpace(authed.AuthHeaderValue) == "" {
		if store, err := config.ProviderKeyStore(); err == nil {
			authed = config.ApplyStoredAPIKey(authed, store)
		}
	}
	if strings.TrimSpace(authed.APIKey) == "" && strings.TrimSpace(authed.AuthHeaderValue) == "" && strings.TrimSpace(authed.APIKeyEnv) != "" {
		authed.APIKey = strings.TrimSpace(os.Getenv(authed.APIKeyEnv))
	}
	return authed
}

// applyAuthCLIDiscoveryCredential stamps an AuthCLI harness's extracted credential
// onto the profile the discovery probe uses. Pure (no I/O) so it is unit-tested
// with a constructed agentcli.Credentials. A stored API key (codex's
// OPENAI_API_KEY) wins as profile.APIKey; otherwise an OAuth access token becomes
// an Authorization: Bearer header. For the anthropic catalog (Claude Code) the
// /v1/models probe also needs the OAuth beta header the live provider sends, added
// via a cloned CustomHeaders map. No usable credential leaves the profile unchanged.
func applyAuthCLIDiscoveryCredential(profile config.ProviderProfile, catalogID string, creds agentcli.Credentials) config.ProviderProfile {
	if key := strings.TrimSpace(creds.APIKey); key != "" {
		profile.APIKey = key
		return profile
	}
	token := strings.TrimSpace(creds.AccessToken)
	if token == "" {
		return profile
	}
	profile.AuthHeader = "Authorization"
	profile.AuthHeaderValue = "Bearer " + token
	if catalogID == "anthropic" {
		cloned := make(map[string]string, len(profile.CustomHeaders)+1)
		for k, v := range profile.CustomHeaders {
			cloned[k] = v
		}
		cloned["anthropic-beta"] = "oauth-2025-04-20"
		profile.CustomHeaders = cloned
	}
	return profile
}

// defaultDiscoverProviderModels is the production discovery hook: a live probe of
// the provider's model-listing endpoint with no curated-catalog merge or
// coding-model filtering, so a custom provider's full model list is returned.
func defaultDiscoverProviderModels(ctx context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
	return providermodeldiscovery.Discover(ctx, profile, providermodeldiscovery.Options{})
}

func parseProviderModelsArgs(args []string) (providerModelsOptions, bool, error) {
	options := providerModelsOptions{}
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
	return options, false, nil
}
