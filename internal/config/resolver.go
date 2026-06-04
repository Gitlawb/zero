package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const defaultMaxTurns = 12

func Resolve(options ResolveOptions) (ResolvedConfig, error) {
	cfg := FileConfig{
		MaxTurns: defaultMaxTurns,
	}

	for _, path := range []string{options.UserConfigPath, options.ProjectConfigPath} {
		if path == "" {
			continue
		}
		fileConfig, err := loadConfigFile(path)
		if err != nil {
			return ResolvedConfig{}, err
		}
		mergeConfig(&cfg, fileConfig)
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

	providers, active, err := normalizeProviders(cfg.Providers, cfg.ActiveProvider)
	if err != nil {
		return ResolvedConfig{}, err
	}

	return ResolvedConfig{
		ActiveProvider: active.Name,
		Providers:      providers,
		Provider:       active,
		MaxTurns:       cfg.MaxTurns,
	}, nil
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
}

func mergeProvider(cfg *FileConfig, provider ProviderProfile) {
	provider.Name = strings.TrimSpace(provider.Name)
	if provider.Name == "" {
		provider.Name = strings.TrimSpace(cfg.ActiveProvider)
	}
	if provider.Name == "" {
		provider.Name = string(ProviderKindOpenAI)
	}

	for index := range cfg.Providers {
		if cfg.Providers[index].Name == provider.Name {
			cfg.Providers[index] = mergeProfile(cfg.Providers[index], provider)
			return
		}
	}
	cfg.Providers = append(cfg.Providers, provider)
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
	if next.BaseURL != "" {
		base.BaseURL = next.BaseURL
	}
	if next.APIKey != "" {
		base.APIKey = next.APIKey
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

	apiKey := envValue(env, "OPENAI_API_KEY")
	baseURL := strings.TrimSpace(envValue(env, "OPENAI_BASE_URL"))
	model := strings.TrimSpace(envValue(env, "OPENAI_MODEL"))
	if apiKey == "" && baseURL == "" && model == "" {
		return
	}

	profile := ProviderProfile{
		Name:         cfg.ActiveProvider,
		ProviderKind: ProviderKindOpenAI,
		APIKey:       apiKey,
		BaseURL:      baseURL,
		Model:        model,
	}
	if profile.Name == "" {
		profile.Name = string(ProviderKindOpenAI)
	}
	if baseURL != "" && !isOfficialOpenAIBaseURL(baseURL) {
		profile.ProviderKind = ProviderKindOpenAICompatible
	}
	mergeProvider(cfg, profile)
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
	for _, provider := range overrides.Providers {
		mergeProvider(cfg, provider)
	}
	if hasProviderFields(overrides.Provider) {
		mergeProvider(cfg, overrides.Provider)
	}
}

func hasProviderFields(profile ProviderProfile) bool {
	return profile.Name != "" ||
		profile.Provider != "" ||
		profile.ProviderKind != "" ||
		profile.BaseURL != "" ||
		profile.APIKey != "" ||
		profile.Model != "" ||
		profile.Description != ""
}

func normalizeProviders(providers []ProviderProfile, activeName string) ([]ProviderProfile, ProviderProfile, error) {
	activeName = strings.TrimSpace(activeName)
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
		next, err := normalizeProvider(provider)
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

func normalizeProvider(profile ProviderProfile) (ProviderProfile, error) {
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Provider = strings.TrimSpace(profile.Provider)
	profile.ProviderKind = ProviderKind(strings.TrimSpace(strings.ToLower(string(profile.ProviderKind))))
	profile.BaseURL = strings.TrimSpace(profile.BaseURL)
	profile.Model = strings.TrimSpace(profile.Model)

	if profile.Name == "" {
		profile.Name = string(ProviderKindOpenAI)
	}
	if profile.ProviderKind == "" && profile.Provider != "" {
		profile.ProviderKind = ProviderKind(strings.ToLower(profile.Provider))
	}
	if profile.ProviderKind == "" {
		profile.ProviderKind = ProviderKindOpenAI
	}

	switch profile.ProviderKind {
	case ProviderKindOpenAI:
		if profile.BaseURL == "" || isOfficialOpenAIBaseURL(profile.BaseURL) {
			profile.BaseURL = OpenAIBaseURL
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
	default:
		return ProviderProfile{}, providerError(profile, "unknown provider kind %q for provider %s", profile.ProviderKind, profile.Name)
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
	return fmt.Errorf("%s", redactSecrets(message, profile.APIKey))
}
