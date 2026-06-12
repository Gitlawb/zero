package config

import "errors"

type Config struct {
	DefaultProvider string
	Providers       map[string]string
}

func Validate(cfg Config) error {
	if cfg.DefaultProvider == "" {
		return errors.New("default provider is required")
	}
	if len(cfg.Providers) == 0 {
		return errors.New("providers are required")
	}
	return nil
}
