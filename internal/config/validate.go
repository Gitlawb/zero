package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Issue is a single structured problem found while validating a config file.
// Message is already routed through the package secret redaction.
type Issue struct {
	FieldPath string `json:"fieldPath,omitempty"`
	Message   string `json:"message"`
}

// ValidateFile reads and parses path as a Zero FileConfig and runs the same
// semantic provider/model rules used during resolution. It returns the parsed
// config (zero value on parse failure) plus any structured issues. A parse
// failure yields a single issue whose Message wraps the underlying JSON error
// so callers can extract *json.SyntaxError / *json.UnmarshalTypeError offsets
// via errors.As.
func ValidateFile(path string) (FileConfig, []Issue) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, []Issue{{Message: fmt.Sprintf("read config %s: %v", path, err)}}
	}

	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, []Issue{{Message: fmt.Errorf("invalid config JSON %s: %w", path, err).Error()}}
	}

	issues := validateSemantics(cfg)
	return cfg, issues
}

// ValidateBytes parses data as a Zero FileConfig and runs the same semantic
// provider/model rules as ValidateFile. It returns the parsed config (zero
// value on parse failure) plus any structured issues. A parse failure yields a
// single issue whose Message wraps the underlying JSON error (path-less form:
// "invalid config JSON: <err>") so callers can extract *json.SyntaxError /
// *json.UnmarshalTypeError offsets via errors.As.
func ValidateBytes(data []byte) (FileConfig, []Issue) {
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, []Issue{{Message: fmt.Errorf("invalid config JSON: %w", err).Error()}}
	}
	issues := validateSemantics(cfg)
	return cfg, issues
}

func validateSemantics(cfg FileConfig) []Issue {
	issues := []Issue{}
	if _, _, err := normalizeProviders(cfg.Providers, cfg.ActiveProvider); err != nil {
		// normalizeProviders already redacts secrets via providerError.
		issues = append(issues, Issue{FieldPath: "providers", Message: err.Error()})
	}
	if err := validateSTTConfig(cfg.STT); err != nil {
		issues = append(issues, Issue{FieldPath: "stt", Message: err.Error()})
	}
	for _, issue := range invalidProviderModelIssues(cfg) {
		issues = append(issues, issue)
	}
	for _, entry := range invalidFavoriteModelRefs(cfg) {
		issues = append(issues, Issue{
			FieldPath: "preferences.favoriteModels",
			Message:   fmt.Sprintf("favorite model %q does not reference an available provider/model in config.json", entry),
		})
	}
	return issues
}

func invalidProviderModelIssues(cfg FileConfig) []Issue {
	issues := []Issue{}
	for _, provider := range cfg.Providers {
		model := strings.TrimSpace(provider.Model)
		if model == "" || len(provider.Models) == 0 {
			continue
		}
		available := map[string]bool{}
		for _, discovered := range provider.Models {
			if id := strings.TrimSpace(discovered.ID); id != "" {
				available[strings.ToLower(id)] = true
			}
		}
		if len(available) == 0 || available[strings.ToLower(model)] {
			continue
		}
		name := strings.TrimSpace(provider.Name)
		if name == "" {
			name = "provider"
		}
		issues = append(issues, Issue{
			FieldPath: "providers." + name + ".model",
			Message:   fmt.Sprintf("provider %q model %q is not in its discovered models list", name, model),
		})
	}
	return issues
}

func invalidFavoriteModelRefs(cfg FileConfig) []string {
	if len(cfg.Preferences.FavoriteModels) == 0 {
		return nil
	}
	inventory := newFavoriteModelInventory(cfg)
	invalid := make([]string, 0)
	for _, entry := range cfg.Preferences.FavoriteModels {
		entry = strings.TrimSpace(entry)
		if !inventory.validFavoriteModelRef(entry) {
			invalid = append(invalid, entry)
		}
	}
	return invalid
}
