package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Gitlawb/zero/internal/modelregistry"
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
	if providerKind == ProviderKindOpenAI && apiKey != "" && model == "" && isOfficialOpenAIBaseURL(baseURL) {
		model = modelregistry.DefaultModelID
	}
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
	case ProviderKindAnthropic:
		if profile.BaseURL == "" {
			profile.BaseURL = AnthropicBaseURL
		}
		return profile, nil
	case ProviderKindGoogle:
		if profile.BaseURL == "" {
			profile.BaseURL = GoogleBaseURL
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
