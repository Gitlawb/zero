package config

import (
	"encoding/json"
	"fmt"
	"os"
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
	if _, _, err := normalizeProviders(cfg.Providers, cfg.ActiveProvider); err != nil {
		// normalizeProviders already redacts secrets via providerError.
		return []Issue{{FieldPath: "providers", Message: err.Error()}}
	}
	return nil
}
