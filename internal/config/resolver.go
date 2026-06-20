package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/notify"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/sandbox"
)

const defaultMaxTurns = 30

// defaultDeferThreshold is the number of deferred-eligible (MCP) tools at which
// Zero collapses their full JSON schemas into compact `tool_search` reminder
// lines instead of advertising every schema on every turn. MCP tool schemas run
// 300-600 tokens each, so eagerly shipping even a small server's toolset wastes
// thousands of input tokens per message. Kept low so a typical single-server set
// (Exa-style, a handful of tools) defers and stays cheap; the only cost is one
// `tool_search` round-trip before a deferred tool's first use, after which it is
// loaded for the rest of the run. Override per-config with tools.deferThreshold
// (set 0 to always advertise every schema, e.g. for a model without tool_search).
const defaultDeferThreshold = 3

func Resolve(options ResolveOptions) (ResolvedConfig, error) {
	cfg := FileConfig{
		MaxTurns: defaultMaxTurns,
	}

	if options.UserConfigPath != "" {
		fileConfig, err := loadConfigFile(options.UserConfigPath)
		if err != nil {
			return ResolvedConfig{}, err
		}
		mergeConfig(&cfg, fileConfig)
	}
	if options.ProjectConfigPath != "" {
		fileConfig, err := loadConfigFile(options.ProjectConfigPath)
		if err != nil {
			return ResolvedConfig{}, err
		}
		if err := mergeProjectConfig(&cfg, fileConfig); err != nil {
			return ResolvedConfig{}, err
		}
	}

	applyEnv(&cfg, options.Env)

	if options.ProviderCommand != "" {
		commandConfig, err := LoadProviderCommand(options.ProviderCommand)
		if err != nil {
			return ResolvedConfig{}, err
		}
		mergeConfig(&cfg, commandConfig)
	}

	applyOverrides(&cfg, options.Overrides)

	if !cfg.Tools.deferThresholdSet && cfg.Tools.DeferThreshold == 0 {
		cfg.Tools.DeferThreshold = defaultDeferThreshold
	}
	if cfg.Tools.DeferThreshold < 0 {
		return ResolvedConfig{}, fmt.Errorf("invalid tools.deferThreshold %d: must be >= 0", cfg.Tools.DeferThreshold)
	}

	if cfg.Swarm.MaxTeamSize < 0 {
		return ResolvedConfig{}, fmt.Errorf("invalid swarm.maxTeamSize %d: must be >= 0 (0 uses the default)", cfg.Swarm.MaxTeamSize)
	}

	if network := strings.TrimSpace(cfg.Sandbox.Network); network != "" {
		switch sandbox.NetworkMode(network) {
		case sandbox.NetworkAllow, sandbox.NetworkDeny:
		default:
			return ResolvedConfig{}, fmt.Errorf("invalid sandbox.network %q: expected allow or deny", network)
		}
	}
	if mode := strings.TrimSpace(cfg.Notify.Mode); mode != "" {
		switch notify.Mode(mode) {
		case notify.ModeOff, notify.ModeBell, notify.ModeNotify, notify.ModeBoth:
		default:
			return ResolvedConfig{}, fmt.Errorf("invalid notify.mode %q: expected off, bell, notify, or both", mode)
		}
	}
	if focusMode := strings.TrimSpace(cfg.Notify.FocusMode); focusMode != "" {
		switch notify.FocusMode(focusMode) {
		case notify.FocusUnfocused, notify.FocusAlways, notify.FocusFocused:
		default:
			return ResolvedConfig{}, fmt.Errorf("invalid notify.focusMode %q: expected unfocused, always, or focused", focusMode)
		}
	}

	providers, active, err := normalizeProviders(cfg.Providers, cfg.ActiveProvider, options.Env)
	if err != nil {
		return ResolvedConfig{}, err
	}

	return ResolvedConfig{
		ActiveProvider: active.Name,
		Providers:      providers,
		Provider:       active,
		MaxTurns:       cfg.MaxTurns,
		MCP:            cfg.MCP,
		Sandbox:        cfg.Sandbox,
		Notify:         cfg.Notify,
		Tools:          cfg.Tools,
		Swarm:          cfg.Swarm,
		Preferences:    cfg.Preferences,
	}, nil
}

func ResolveMCP(options ResolveOptions) (MCPConfig, error) {
	// Seed Zero's built-in default MCP servers (e.g. keyless Firecrawl for free,
	// no-setup web search/scrape) BEFORE merging user/project config, so the user
	// can override any field or disable a default by writing over it.
	cfg := FileConfig{MCP: MCPConfig{Servers: DefaultMCPServers()}}

	for _, path := range []string{options.UserConfigPath, options.ProjectConfigPath} {
		if path == "" {
			continue
		}
		fileConfig, err := loadConfigFile(path)
		if err != nil {
			return MCPConfig{}, err
		}
		mergeMCPConfig(&cfg.MCP, fileConfig.MCP)
	}
	mergeMCPConfig(&cfg.MCP, options.Overrides.MCP)
	return cfg.MCP, nil
}

func loadConfigFile(path string) (FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	return cfg, nil
}

func mergeConfig(dst *FileConfig, src FileConfig) {
	if activeProvider := strings.TrimSpace(src.ActiveProvider); activeProvider != "" {
		dst.ActiveProvider = activeProvider
	}
	if src.MaxTurns > 0 {
		dst.MaxTurns = src.MaxTurns
	}
	for _, provider := range src.Providers {
		mergeProvider(dst, provider)
	}
	mergeMCPConfig(&dst.MCP, src.MCP)
	if network := strings.TrimSpace(src.Sandbox.Network); network != "" {
		dst.Sandbox.Network = network
	}
	dst.Sandbox.AdditionalWriteRoots = unionStrings(dst.Sandbox.AdditionalWriteRoots, src.Sandbox.AdditionalWriteRoots)
	if src.Sandbox.BlockUnixSockets {
		dst.Sandbox.BlockUnixSockets = true
	}
	if src.Sandbox.MonitorDenials {
		dst.Sandbox.MonitorDenials = true
	}
	if mode := strings.TrimSpace(src.Notify.Mode); mode != "" {
		dst.Notify.Mode = mode
	}
	if focusMode := strings.TrimSpace(src.Notify.FocusMode); focusMode != "" {
		dst.Notify.FocusMode = focusMode
	}
	if src.Tools.deferThresholdSet {
		dst.Tools.DeferThreshold = src.Tools.DeferThreshold
		dst.Tools.deferThresholdSet = true
	}
	if src.Swarm.MaxTeamSize != 0 {
		dst.Swarm.MaxTeamSize = src.Swarm.MaxTeamSize
	}
	if src.Preferences.FavoriteModels != nil {
		dst.Preferences.FavoriteModels = normalizeFavoriteModels(src.Preferences.FavoriteModels)
	}
}

func mergeProjectConfig(dst *FileConfig, src FileConfig) error {
	if activeProvider := strings.TrimSpace(src.ActiveProvider); activeProvider != "" {
		dst.ActiveProvider = activeProvider
	}
	if src.MaxTurns > 0 {
		dst.MaxTurns = src.MaxTurns
	}
	for _, provider := range src.Providers {
		candidate := providerMergeCandidate(*dst, provider)
		if err := validateProjectProviderMerge(provider, candidate); err != nil {
			return err
		}
		mergeProvider(dst, provider)
	}
	mergeMCPConfig(&dst.MCP, src.MCP)
	// Sandbox.AdditionalWriteRoots is intentionally NOT merged from project
	// config: a cloned repo's .zero/config.json must not be able to grant
	// itself write access outside the workspace. Global config and CLI flags
	// are the only config sources for write roots.
	if network := strings.TrimSpace(src.Sandbox.Network); network != "" {
		dst.Sandbox.Network = network
	}
	if src.Sandbox.BlockUnixSockets {
		dst.Sandbox.BlockUnixSockets = true
	}
	if src.Sandbox.MonitorDenials {
		dst.Sandbox.MonitorDenials = true
	}
	if mode := strings.TrimSpace(src.Notify.Mode); mode != "" {
		dst.Notify.Mode = mode
	}
	if focusMode := strings.TrimSpace(src.Notify.FocusMode); focusMode != "" {
		dst.Notify.FocusMode = focusMode
	}
	if src.Tools.deferThresholdSet {
		dst.Tools.DeferThreshold = src.Tools.DeferThreshold
		dst.Tools.deferThresholdSet = true
	}
	if src.Swarm.MaxTeamSize != 0 {
		dst.Swarm.MaxTeamSize = src.Swarm.MaxTeamSize
	}
	return nil
}

func mergeProvider(cfg *FileConfig, provider ProviderProfile) {
	provider.Name = providerMergeName(*cfg, provider)

	for index := range cfg.Providers {
		if cfg.Providers[index].Name == provider.Name {
			cfg.Providers[index] = mergeProfile(cfg.Providers[index], provider)
			return
		}
	}
	cfg.Providers = append(cfg.Providers, provider)
}

func providerMergeCandidate(cfg FileConfig, provider ProviderProfile) ProviderProfile {
	provider.Name = providerMergeName(cfg, provider)
	for _, existing := range cfg.Providers {
		if existing.Name == provider.Name {
			return mergeProfile(existing, provider)
		}
	}
	return provider
}

func providerMergeName(cfg FileConfig, provider ProviderProfile) string {
	name := strings.TrimSpace(provider.Name)
	if name == "" {
		name = strings.TrimSpace(cfg.ActiveProvider)
	}
	if name == "" {
		name = string(ProviderKindOpenAI)
	}
	return name
}

func validateProjectProviderMerge(project ProviderProfile, candidate ProviderProfile) error {
	if strings.TrimSpace(project.APIKeyEnv) != "" &&
		projectEndpointNeedsCredentialGuard(candidate) &&
		!projectAPIKeyEnvAllowed(candidate, project.APIKeyEnv) {
		return providerError(candidate, "project provider %s cannot bind apiKeyEnv to custom provider endpoint", candidate.Name)
	}
	if strings.TrimSpace(project.BaseURL) != "" &&
		projectEndpointNeedsCredentialGuard(candidate) &&
		hasInheritedProviderCredentialMaterial(project, candidate) &&
		!projectBaseURLAllowed(candidate) {
		return providerError(candidate, "project provider %s cannot override baseURL for a credentialed custom provider endpoint", candidate.Name)
	}
	return nil
}

func projectEndpointNeedsCredentialGuard(profile ProviderProfile) bool {
	kind := effectiveProviderKind(profile)
	switch kind {
	case ProviderKindOpenAICompatible, ProviderKindAnthropicCompat:
		return strings.TrimSpace(profile.BaseURL) != "" || strings.TrimSpace(profile.CatalogID) != ""
	case ProviderKindOpenAI:
		return profile.BaseURL != "" && !isOfficialOpenAIBaseURL(profile.BaseURL)
	case ProviderKindAnthropic:
		return profile.BaseURL != "" && !isOfficialAnthropicBaseURL(profile.BaseURL)
	case ProviderKindGoogle:
		return profile.BaseURL != "" && !isOfficialGoogleBaseURL(profile.BaseURL)
	default:
		return strings.TrimSpace(profile.BaseURL) != ""
	}
}

func projectAPIKeyEnvAllowed(profile ProviderProfile, envName string) bool {
	descriptor, ok := catalogDescriptorForProfile(profile)
	if !ok {
		return false
	}
	baseURL := strings.TrimSpace(profile.BaseURL)
	if baseURL == "" {
		baseURL = descriptor.DefaultBaseURL
	}
	if !sameBaseURL(baseURL, descriptor.DefaultBaseURL) {
		return false
	}
	return containsStringFold(descriptor.AuthEnvVars, envName)
}

func projectBaseURLAllowed(profile ProviderProfile) bool {
	descriptor, ok := catalogDescriptorForProfile(profile)
	return ok && sameBaseURL(profile.BaseURL, descriptor.DefaultBaseURL)
}

func hasInheritedProviderCredentialMaterial(project ProviderProfile, candidate ProviderProfile) bool {
	if strings.TrimSpace(project.APIKey) == "" && strings.TrimSpace(candidate.APIKey) != "" {
		return true
	}
	if strings.TrimSpace(project.APIKeyEnv) == "" && strings.TrimSpace(candidate.APIKeyEnv) != "" {
		return true
	}
	if strings.TrimSpace(project.AuthHeaderValue) == "" && strings.TrimSpace(candidate.AuthHeaderValue) != "" {
		return true
	}
	if project.CustomHeaders == nil && hasCustomHeaderMaterial(candidate.CustomHeaders) {
		return true
	}
	return false
}

func hasCustomHeaderMaterial(headers map[string]string) bool {
	for _, value := range headers {
		if strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func effectiveProviderKind(profile ProviderProfile) ProviderKind {
	if kind := ProviderKind(strings.TrimSpace(strings.ToLower(string(profile.ProviderKind)))); kind != "" {
		return kind
	}
	if provider := strings.TrimSpace(strings.ToLower(profile.Provider)); provider != "" {
		return ProviderKind(provider)
	}
	if descriptor, ok := catalogDescriptorForProfile(profile); ok {
		return providerKindForCatalogTransport(descriptor.Transport)
	}
	return ""
}

func catalogDescriptorForProfile(profile ProviderProfile) (providercatalog.Descriptor, bool) {
	catalogID := providercatalog.NormalizeID(profile.CatalogID)
	if catalogID == "" {
		return providercatalog.Descriptor{}, false
	}
	descriptor, err := providercatalog.Require(catalogID)
	if err != nil {
		return providercatalog.Descriptor{}, false
	}
	return descriptor, true
}

func containsStringFold(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.EqualFold(strings.TrimSpace(value), target) {
			return true
		}
	}
	return false
}

func mergeProfile(base ProviderProfile, next ProviderProfile) ProviderProfile {
	if next.Name != "" {
		base.Name = next.Name
	}
	if next.Provider != "" {
		base.Provider = next.Provider
	}
	if next.ProviderKind != "" {
		base.ProviderKind = next.ProviderKind
	}
	if next.CatalogID != "" {
		base.CatalogID = next.CatalogID
	}
	if next.BaseURL != "" {
		base.BaseURL = next.BaseURL
	}
	if next.APIKey != "" {
		base.APIKey = next.APIKey
	}
	if next.APIKeyEnv != "" {
		base.APIKeyEnv = next.APIKeyEnv
	}
	if next.APIFormat != "" {
		base.APIFormat = next.APIFormat
	}
	if next.AuthHeader != "" {
		base.AuthHeader = next.AuthHeader
	}
	if next.AuthScheme != "" {
		base.AuthScheme = next.AuthScheme
	}
	if next.AuthHeaderValue != "" {
		base.AuthHeaderValue = next.AuthHeaderValue
	}
	if next.CustomHeaders != nil {
		base.CustomHeaders = copyStringMap(next.CustomHeaders)
	}
	if next.Model != "" {
		base.Model = next.Model
	}
	if next.Description != "" {
		base.Description = next.Description
	}
	return base
}

func applyEnv(cfg *FileConfig, env map[string]string) {
	activeProvider := strings.TrimSpace(envValue(env, "ZERO_PROVIDER"))
	if activeProvider != "" {
		cfg.ActiveProvider = activeProvider
	}

	applyProviderEnv(cfg, ProviderKindOpenAI, envProfile{
		Name:    string(ProviderKindOpenAI),
		APIKey:  envValue(env, "OPENAI_API_KEY"),
		BaseURL: envValue(env, "OPENAI_BASE_URL"),
		Model:   envValue(env, "OPENAI_MODEL"),
	})
	applyProviderEnv(cfg, ProviderKindAnthropic, envProfile{
		Name:    string(ProviderKindAnthropic),
		APIKey:  envValue(env, "ANTHROPIC_API_KEY"),
		BaseURL: envValue(env, "ANTHROPIC_BASE_URL"),
		Model:   envValue(env, "ANTHROPIC_MODEL"),
	})
	applyProviderEnv(cfg, ProviderKindGoogle, envProfile{
		Name:    string(ProviderKindGoogle),
		APIKey:  firstNonEmpty(envValue(env, "GEMINI_API_KEY"), envValue(env, "GOOGLE_API_KEY")),
		BaseURL: firstNonEmpty(envValue(env, "GEMINI_BASE_URL"), envValue(env, "GOOGLE_BASE_URL")),
		Model:   firstNonEmpty(envValue(env, "GEMINI_MODEL"), envValue(env, "GOOGLE_MODEL")),
	})
}

type envProfile struct {
	Name    string
	APIKey  string
	BaseURL string
	Model   string
}

func applyProviderEnv(cfg *FileConfig, providerKind ProviderKind, env envProfile) {
	apiKey := strings.TrimSpace(env.APIKey)
	baseURL := strings.TrimSpace(env.BaseURL)
	model := strings.TrimSpace(env.Model)
	if apiKey == "" && baseURL == "" && model == "" {
		return
	}

	profile := ProviderProfile{
		Name:         providerEnvTargetName(cfg, providerKind, env.Name),
		ProviderKind: providerKind,
		APIKey:       apiKey,
		BaseURL:      baseURL,
		Model:        model,
	}
	if profile.Name == "" {
		profile.Name = string(providerKind)
	}
	if providerKind == ProviderKindOpenAI && baseURL != "" && !isOfficialOpenAIBaseURL(baseURL) {
		profile.ProviderKind = ProviderKindOpenAICompatible
	}
	// When the env supplies only credentials (no baseURL) for a provider name that
	// already exists, don't force the standard transport kind — a same-named
	// openai-compatible/anthropic-compatible proxy or gateway must keep its kind.
	// An empty kind makes mergeProfile preserve the existing provider's kind (H2).
	if baseURL == "" {
		for _, existing := range cfg.Providers {
			if strings.TrimSpace(existing.Name) == profile.Name {
				profile.ProviderKind = ""
				break
			}
		}
	}
	mergeProvider(cfg, profile)
}

func providerEnvTargetName(cfg *FileConfig, providerKind ProviderKind, fallback string) string {
	if activeName := strings.TrimSpace(cfg.ActiveProvider); activeName != "" {
		for _, provider := range cfg.Providers {
			if strings.TrimSpace(provider.Name) != activeName {
				continue
			}
			if normalizedProfileKind(provider) == providerKind {
				return activeName
			}
			break
		}
	}

	for _, provider := range cfg.Providers {
		if normalizedProfileKind(provider) != providerKind {
			continue
		}
		if name := strings.TrimSpace(provider.Name); name != "" {
			return name
		}
	}

	return strings.TrimSpace(fallback)
}

func normalizedProfileKind(profile ProviderProfile) ProviderKind {
	kind := strings.TrimSpace(string(profile.ProviderKind))
	if kind == "" {
		kind = strings.TrimSpace(profile.Provider)
	}
	return ProviderKind(strings.ToLower(kind))
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}

func applyOverrides(cfg *FileConfig, overrides Overrides) {
	if activeProvider := strings.TrimSpace(overrides.ActiveProvider); activeProvider != "" {
		cfg.ActiveProvider = activeProvider
	}
	if overrides.MaxTurns > 0 {
		cfg.MaxTurns = overrides.MaxTurns
	}
	if overrides.Sandbox.BlockUnixSockets {
		cfg.Sandbox.BlockUnixSockets = true
	}
	if overrides.Sandbox.MonitorDenials {
		cfg.Sandbox.MonitorDenials = true
	}
	if mode := strings.TrimSpace(overrides.Notify.Mode); mode != "" {
		cfg.Notify.Mode = mode
	}
	if focusMode := strings.TrimSpace(overrides.Notify.FocusMode); focusMode != "" {
		cfg.Notify.FocusMode = focusMode
	}
	if overrides.Tools.deferThresholdSet || overrides.Tools.DeferThreshold != 0 {
		cfg.Tools.DeferThreshold = overrides.Tools.DeferThreshold
		cfg.Tools.deferThresholdSet = true
	}
	for _, provider := range overrides.Providers {
		mergeProvider(cfg, provider)
	}
	if hasProviderFields(overrides.Provider) {
		mergeProvider(cfg, overrides.Provider)
	}
	mergeMCPConfig(&cfg.MCP, overrides.MCP)
}

func mergeMCPConfig(dst *MCPConfig, src MCPConfig) {
	if len(src.Servers) == 0 {
		return
	}
	if dst.Servers == nil {
		dst.Servers = map[string]MCPServerConfig{}
	}
	for name, server := range src.Servers {
		dst.Servers[name] = mergeMCPServer(dst.Servers[name], server)
	}
}

func mergeMCPServer(base MCPServerConfig, next MCPServerConfig) MCPServerConfig {
	if strings.TrimSpace(next.Type) != "" {
		base.Type = next.Type
	}
	if strings.TrimSpace(next.Command) != "" {
		base.Command = next.Command
	}
	if next.Args != nil {
		base.Args = append([]string{}, next.Args...)
	}
	if next.Env != nil {
		base.Env = copyMCPStringMap(next.Env)
	}
	if strings.TrimSpace(next.URL) != "" {
		base.URL = next.URL
	}
	if next.Headers != nil {
		base.Headers = copyMCPStringMap(next.Headers)
	}
	if strings.TrimSpace(next.Auth) != "" {
		base.Auth = next.Auth
	}
	if next.OAuth != nil {
		base.OAuth = next.OAuth
	}
	if next.disabledSet || next.Disabled {
		base.Disabled = next.Disabled
	}
	return base
}

func copyMCPStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func hasProviderFields(profile ProviderProfile) bool {
	return HasProviderProfile(profile)
}

// unionStrings appends the values of extra that are not already present in
// base, preserving order. Used for additive config keys like
// sandbox.additionalWriteRoots where a later layer must not erase earlier
// grants.
func unionStrings(base []string, extra []string) []string {
	seen := make(map[string]struct{}, len(base))
	for _, value := range base {
		seen[value] = struct{}{}
	}
	for _, value := range extra {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		base = append(base, value)
	}
	return base
}

type normalizeOptions struct {
	defaultOpenAIModel bool
}

func normalizeProviders(providers []ProviderProfile, activeName string, envMaps ...map[string]string) ([]ProviderProfile, ProviderProfile, error) {
	return normalizeProvidersWithOptions(providers, activeName, normalizeOptions{defaultOpenAIModel: true}, envMaps...)
}

func normalizeProvidersWithoutModelDefaults(providers []ProviderProfile, activeName string, envMaps ...map[string]string) ([]ProviderProfile, ProviderProfile, error) {
	return normalizeProvidersWithOptions(providers, activeName, normalizeOptions{}, envMaps...)
}

func normalizeProvidersWithOptions(providers []ProviderProfile, activeName string, options normalizeOptions, envMaps ...map[string]string) ([]ProviderProfile, ProviderProfile, error) {
	activeName = strings.TrimSpace(activeName)
	var env map[string]string
	if len(envMaps) > 0 {
		env = envMaps[0]
	}
	if len(providers) == 0 {
		if activeName != "" {
			return nil, ProviderProfile{}, fmt.Errorf("active provider %q not found", activeName)
		}
		return []ProviderProfile{}, ProviderProfile{}, nil
	}

	if activeName == "" && len(providers) == 1 {
		activeName = providers[0].Name
	}

	normalized := make([]ProviderProfile, 0, len(providers))
	var active ProviderProfile
	activeFound := false
	for _, provider := range providers {
		next, err := normalizeProvider(provider, env, options)
		if err != nil {
			return nil, ProviderProfile{}, err
		}
		normalized = append(normalized, next)
		if next.Name == activeName {
			active = next
			activeFound = true
		}
	}

	if !activeFound {
		return nil, ProviderProfile{}, fmt.Errorf("active provider %q not found", activeName)
	}
	if active.Model == "" {
		return nil, ProviderProfile{}, providerError(active, "provider %s requires model", active.Name)
	}

	return normalized, active, nil
}

func normalizeProvider(profile ProviderProfile, env map[string]string, options normalizeOptions) (ProviderProfile, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Provider = strings.TrimSpace(profile.Provider)
	profile.ProviderKind = ProviderKind(strings.TrimSpace(strings.ToLower(string(profile.ProviderKind))))
	profile.CatalogID = providercatalog.NormalizeID(profile.CatalogID)
	profile.BaseURL = strings.TrimSpace(profile.BaseURL)
	explicitBaseURL := profile.BaseURL != ""
	profile.APIKeyEnv = strings.TrimSpace(profile.APIKeyEnv)
	profile.APIFormat = strings.TrimSpace(profile.APIFormat)
	profile.AuthHeader = strings.TrimSpace(profile.AuthHeader)
	profile.AuthScheme = strings.TrimSpace(profile.AuthScheme)
	profile.AuthHeaderValue = strings.TrimSpace(profile.AuthHeaderValue)
	profile.Model = strings.TrimSpace(profile.Model)

	if profile.Name == "" {
		profile.Name = string(ProviderKindOpenAI)
	}
	if profile.CatalogID != "" {
		descriptor, err := providercatalog.Require(profile.CatalogID)
		if err != nil {
			return ProviderProfile{}, providerError(profile, "%s", err.Error())
		}
		if !providercatalog.RuntimeSupported(descriptor) {
			return ProviderProfile{}, providerError(profile, "provider %q uses transport %q: %s", descriptor.ID, descriptor.Transport, providercatalog.RuntimeUnsupportedReason(descriptor))
		}
		applyCatalogDescriptor(&profile, descriptor, explicitBaseURL)
	}
	if profile.ProviderKind == "" && profile.Provider != "" {
		profile.ProviderKind = ProviderKind(strings.ToLower(profile.Provider))
	}
	if profile.ProviderKind == "" {
		profile.ProviderKind = ProviderKindOpenAI
	}
	if strings.TrimSpace(profile.APIKey) == "" && profile.APIKeyEnv != "" {
		profile.APIKey = strings.TrimSpace(envValue(env, profile.APIKeyEnv))
	}

	switch profile.ProviderKind {
	case ProviderKindOpenAI:
		if profile.BaseURL == "" || isOfficialOpenAIBaseURL(profile.BaseURL) {
			profile.BaseURL = OpenAIBaseURL
			if options.defaultOpenAIModel && profile.Model == "" {
				profile.Model = modelregistry.DefaultModelID
			}
			return profile, nil
		}
		return ProviderProfile{}, providerError(profile, "openai provider %s requires official baseURL %s", profile.Name, OpenAIBaseURL)
	case ProviderKindOpenAICompatible:
		if profile.BaseURL == "" {
			return ProviderProfile{}, providerError(profile, "openai-compatible provider %s requires baseURL", profile.Name)
		}
		if isOfficialOpenAIBaseURL(profile.BaseURL) {
			return ProviderProfile{}, providerError(profile, "openai-compatible provider %s requires custom baseURL", profile.Name)
		}
		return profile, nil
	case ProviderKindAnthropic:
		if profile.BaseURL == "" || isOfficialAnthropicBaseURL(profile.BaseURL) {
			profile.BaseURL = AnthropicBaseURL
			return profile, nil
		}
		return ProviderProfile{}, providerError(profile, "anthropic provider %s requires official baseURL %s", profile.Name, AnthropicBaseURL)
	case ProviderKindAnthropicCompat:
		if profile.BaseURL == "" {
			return ProviderProfile{}, providerError(profile, "anthropic-compatible provider %s requires baseURL", profile.Name)
		}
		if isOfficialAnthropicBaseURL(profile.BaseURL) {
			return ProviderProfile{}, providerError(profile, "anthropic-compatible provider %s requires custom baseURL", profile.Name)
		}
		return profile, nil
	case ProviderKindGoogle:
		if profile.BaseURL == "" || isOfficialGoogleBaseURL(profile.BaseURL) {
			profile.BaseURL = GoogleBaseURL
			return profile, nil
		}
		return ProviderProfile{}, providerError(profile, "google provider %s requires official baseURL %s", profile.Name, GoogleBaseURL)
	default:
		return ProviderProfile{}, providerError(profile, "unknown provider kind %q for provider %s", profile.ProviderKind, profile.Name)
	}
}

func applyCatalogDescriptor(profile *ProviderProfile, descriptor providercatalog.Descriptor, explicitBaseURL bool) {
	if descriptor.ID != "" {
		profile.CatalogID = descriptor.ID
	}
	if profile.ProviderKind == "" && strings.TrimSpace(profile.Provider) == "" {
		profile.ProviderKind = providerKindForCatalogTransport(descriptor.Transport)
	}
	if profile.BaseURL == "" {
		profile.BaseURL = descriptor.DefaultBaseURL
	}
	if profile.Model == "" {
		profile.Model = descriptor.DefaultModel
	}
	if profile.APIKeyEnv == "" && len(descriptor.AuthEnvVars) > 0 && (!explicitBaseURL || sameBaseURL(profile.BaseURL, descriptor.DefaultBaseURL)) {
		profile.APIKeyEnv = descriptor.AuthEnvVars[0]
	}
}

func sameBaseURL(left string, right string) bool {
	return strings.EqualFold(
		strings.TrimRight(strings.TrimSpace(left), "/"),
		strings.TrimRight(strings.TrimSpace(right), "/"),
	)
}

func isOfficialAnthropicBaseURL(baseURL string) bool {
	normalized := strings.TrimSpace(baseURL)
	if strings.TrimRight(normalized, "/") == strings.TrimRight(AnthropicBaseURL, "/") {
		return true
	}
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Host == "" {
		return false
	}
	official, err := url.Parse(AnthropicBaseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, official.Host)
}

func isOfficialGoogleBaseURL(baseURL string) bool {
	normalized := strings.TrimSpace(baseURL)
	if strings.TrimRight(normalized, "/") == strings.TrimRight(GoogleBaseURL, "/") {
		return true
	}
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Host == "" {
		return false
	}
	official, err := url.Parse(GoogleBaseURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, official.Host)
}

func providerKindForCatalogTransport(transport providercatalog.Transport) ProviderKind {
	switch transport {
	case providercatalog.TransportOpenAI:
		return ProviderKindOpenAI
	case providercatalog.TransportAnthropic:
		return ProviderKindAnthropic
	case providercatalog.TransportAnthropicCompatible:
		return ProviderKindAnthropicCompat
	case providercatalog.TransportGoogle:
		return ProviderKindGoogle
	case providercatalog.TransportOpenAICompatible:
		return ProviderKindOpenAICompatible
	default:
		return ProviderKind(strings.ToLower(string(transport)))
	}
}

func isOfficialOpenAIBaseURL(baseURL string) bool {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return baseURL == "" ||
		baseURL == "openai" ||
		baseURL == strings.TrimRight(OpenAIBaseURL, "/")
}

func providerError(profile ProviderProfile, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if profile.APIKey != "" {
		message += " (apiKey=[REDACTED])"
	}
	return fmt.Errorf("%s", redactSecrets(message, profile.APIKey, profile.AuthHeaderValue))
}
