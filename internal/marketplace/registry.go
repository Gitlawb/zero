package marketplace

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	OfficialCatalogID     = "official"
	OfficialCatalogSource = "Gitlawb/zero-plugins"
)

type Scope string

const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

type Registry struct {
	Catalogs []RegisteredCatalog `json:"catalogs"`
}

type RegisteredCatalog struct {
	ID                 string             `json:"id"`
	Source             string             `json:"source"`
	PublicKeyPath      string             `json:"publicKeyPath,omitempty"`
	VerificationStatus VerificationStatus `json:"verificationStatus,omitempty"`
}

func (registry *Registry) Add(catalog RegisteredCatalog) error {
	if err := validateID("id", catalog.ID); err != nil {
		return err
	}
	if strings.TrimSpace(catalog.Source) == "" {
		return fmt.Errorf("source: required")
	}
	if catalog.ID == OfficialCatalogID {
		return fmt.Errorf("catalog id %q is reserved", OfficialCatalogID)
	}
	for _, existing := range registry.Catalogs {
		if existing.ID == catalog.ID {
			return fmt.Errorf("catalog id %q already exists", catalog.ID)
		}
	}
	registry.Catalogs = append(registry.Catalogs, catalog)
	return nil
}

func LoadRegistry(path string) (Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Registry{}, nil
		}
		return Registry{}, fmt.Errorf("read marketplace registry: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return Registry{}, nil
	}
	var registry Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return Registry{}, fmt.Errorf("parse marketplace registry: %w", err)
	}
	return registry, nil
}

func SaveRegistry(path string, registry Registry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create marketplace registry dir: %w", err)
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return fmt.Errorf("encode marketplace registry: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".marketplaces-*.tmp")
	if err != nil {
		return fmt.Errorf("create marketplace registry temp: %w", err)
	}
	tempName := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempName)
		}
	}()
	if _, err := temp.Write(append(data, '\n')); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write marketplace registry temp: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close marketplace registry temp: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace marketplace registry: %w", err)
	}
	cleanup = false
	return nil
}

func RegistryPathForScope(scope Scope, cwd string, env map[string]string) (string, error) {
	switch scope {
	case ScopeProject:
		if strings.TrimSpace(cwd) == "" {
			return "", fmt.Errorf("workspace root is required for project scope")
		}
		return filepath.Join(cwd, ".zero", "marketplaces.json"), nil
	case "", ScopeUser:
		configHome := strings.TrimSpace(envValue(env, "XDG_CONFIG_HOME"))
		if configHome == "" {
			home := strings.TrimSpace(firstNonEmpty(envValue(env, "HOME"), envValue(env, "USERPROFILE")))
			if home == "" {
				var err error
				home, err = os.UserHomeDir()
				if err != nil {
					return "", fmt.Errorf("resolve user home: %w", err)
				}
			}
			configHome = filepath.Join(home, ".config")
		}
		return filepath.Join(configHome, "zero", "marketplaces.json"), nil
	default:
		return "", fmt.Errorf("unsupported scope %q", scope)
	}
}

func ReadLocalCatalog(path string) ([]byte, []byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read catalog: %w", err)
	}
	signature, err := os.ReadFile(signaturePathForCatalog(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return data, nil, nil
		}
		return nil, nil, fmt.Errorf("read catalog signature: %w", err)
	}
	return data, signature, nil
}

func signaturePathForCatalog(catalogPath string) string {
	if strings.EqualFold(filepath.Base(catalogPath), "catalog.json") {
		return filepath.Join(filepath.Dir(catalogPath), "catalog.sig")
	}
	return catalogPath + ".sig"
}

func envValue(env map[string]string, key string) string {
	if env == nil {
		return os.Getenv(key)
	}
	return env[key]
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
