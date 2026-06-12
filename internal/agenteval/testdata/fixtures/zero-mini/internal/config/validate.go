package config

import (
	"errors"
	"fmt"
)

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
	if _, ok := cfg.Providers[cfg.DefaultProvider]; !ok {
		return fmt.Errorf("default provider %q is not configured", cfg.DefaultProvider)
	}
	return nil
}
