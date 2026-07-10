package marketplace

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	OfficialCatalogID     = "official"
	OfficialCatalogSource = "Gitlawb/zero-plugins"
)

// OfficialCatalogPublicKey verifies Gitlawb/zero-plugins catalog.sig. The
// matching private key is held by the official catalog signing workflow.
var OfficialCatalogPublicKey = ed25519.PublicKey{
	0x58, 0x13, 0x61, 0x1f, 0xf4, 0xb4, 0x5f, 0xf5,
	0x26, 0x8f, 0x57, 0x26, 0x61, 0xe0, 0x6f, 0x91,
	0xa7, 0x2b, 0x2a, 0x2e, 0x0b, 0xf6, 0x90, 0x57,
	0x76, 0x25, 0x52, 0x1f, 0xa8, 0x23, 0x6e, 0x86,
}

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
	PublicKey          ed25519.PublicKey  `json:"-"`
	VerificationStatus VerificationStatus `json:"verificationStatus,omitempty"`
	Scope              Scope              `json:"-"`
	CachePath          string             `json:"-"`
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

func CachePathForScope(scope Scope, cwd string, catalogID string, env map[string]string) (string, error) {
	if err := validateID("catalog id", catalogID); err != nil {
		return "", err
	}
	registryPath, err := RegistryPathForScope(scope, cwd, env)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(registryPath), "marketplace-cache", catalogID, "catalog.json"), nil
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

func SaveCachedCatalog(path string, data []byte, signature []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create marketplace cache dir: %w", err)
	}
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		return fmt.Errorf("write cached catalog: %w", err)
	}
	signaturePath := signaturePathForCatalog(path)
	if len(signature) == 0 {
		if err := os.Remove(signaturePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale catalog signature: %w", err)
		}
		return nil
	}
	if err := writeFileAtomic(signaturePath, signature, 0o644); err != nil {
		return fmt.Errorf("write cached catalog signature: %w", err)
	}
	return nil
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tempName)
		}
	}()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return err
	}
	cleanup = false
	_ = syncDirectory(filepath.Dir(path))
	return nil
}

func syncDirectory(dir string) error {
	file, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return file.Sync()
}

func FetchCatalog(ctx context.Context, rawSource string) ([]byte, []byte, error) {
	source, err := ParseCatalogSource(rawSource)
	if err != nil {
		return nil, nil, err
	}
	switch source.Kind {
	case CatalogSourceLocal:
		return ReadLocalCatalog(source.Canonical)
	case CatalogSourceHTTPS:
		return fetchHTTPCatalog(ctx, source.Canonical)
	case CatalogSourceGitHub, CatalogSourceGit:
		return fetchGitCatalog(ctx, source.Canonical)
	default:
		return nil, nil, fmt.Errorf("unsupported catalog source kind %q", source.Kind)
	}
}

func fetchHTTPCatalog(ctx context.Context, source string) ([]byte, []byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	data, err := fetchURL(ctx, client, source, true)
	if err != nil {
		return nil, nil, err
	}
	signature, err := fetchURL(ctx, client, signatureURLForCatalog(source), false)
	if err != nil {
		return nil, nil, err
	}
	return data, signature, nil
}

func fetchURL(ctx context.Context, client *http.Client, source string, required bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch catalog: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound && !required {
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("fetch catalog: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("read catalog response: %w", err)
	}
	return data, nil
}

func fetchGitCatalog(ctx context.Context, source string) ([]byte, []byte, error) {
	temp, err := os.MkdirTemp("", "zero-marketplace-catalog-")
	if err != nil {
		return nil, nil, fmt.Errorf("create catalog fetch temp: %w", err)
	}
	defer func() { _ = os.RemoveAll(temp) }()
	command := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--", source, temp)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if output, err := command.CombinedOutput(); err != nil {
		return nil, nil, fmt.Errorf("git clone catalog failed: %v: %s", err, strings.TrimSpace(string(output)))
	}
	return ReadLocalCatalog(filepath.Join(temp, "catalog.json"))
}

func signaturePathForCatalog(catalogPath string) string {
	if strings.EqualFold(filepath.Base(catalogPath), "catalog.json") {
		return filepath.Join(filepath.Dir(catalogPath), "catalog.sig")
	}
	return catalogPath + ".sig"
}

func SignaturePathForCatalog(catalogPath string) string {
	return signaturePathForCatalog(catalogPath)
}

func signatureURLForCatalog(catalogURL string) string {
	if strings.HasSuffix(strings.ToLower(catalogURL), "/catalog.json") {
		return catalogURL[:len(catalogURL)-len("catalog.json")] + "catalog.sig"
	}
	return catalogURL + ".sig"
}

func isLoopbackHost(host string) bool {
	name := strings.Trim(host, "[]")
	if name == "localhost" {
		return true
	}
	if ip := net.ParseIP(name); ip != nil && ip.IsLoopback() {
		return true
	}
	return false
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
