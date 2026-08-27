package config

import (
	"encoding/json"
	"fmt"
)

// Issue is a single structured problem found while validating a config file.
// Message is already routed through the package secret redaction.
type Issue struct {
	FieldPath string `json:"fieldPath,omitempty"`
	Message   string `json:"message"`
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
	issues = append(issues, unknownFieldIssues(data)...)
	return cfg, issues
}

func validateSemantics(cfg FileConfig) []Issue {
	if _, _, err := normalizeProviders(cfg.Providers, cfg.ActiveProvider); err != nil {
		// normalizeProviders already redacts secrets via providerError.
		return []Issue{{FieldPath: "providers", Message: err.Error()}}
	}
	if err := validateSTTConfig(cfg.STT); err != nil {
		return []Issue{{FieldPath: "stt", Message: err.Error()}}
	}
	if err := validateSessionsConfig(cfg.Sessions); err != nil {
		return []Issue{{FieldPath: "sessions", Message: err.Error()}}
	}
	return nil
}

func validateSessionsConfig(cfg SessionsConfig) error {
	if cfg.RetentionDays < 0 {
		return fmt.Errorf("invalid sessions.retentionDays %d: must be >= 0 (0 disables age-based pruning)", cfg.RetentionDays)
	}
	if cfg.MaxCount < 0 {
		return fmt.Errorf("invalid sessions.maxCount %d: must be >= 0 (0 disables count-based pruning)", cfg.MaxCount)
	}
	return nil
}
