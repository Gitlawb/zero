package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/credstore"
	"github.com/Gitlawb/zero/internal/providercatalog"
)

// ValidatePersistedProviderNames rejects user-config rows that share the same
// case-insensitive identity. Credential-store keys are case-insensitive, so
// allowing both rows would make writes and deletes affect a shared secret.
// This validator intentionally applies only to raw persisted user config, not
// to profiles merged from project, environment, or provider-command layers.
//
// A repeated folded identity is rejected whether or not the spellings differ.
// Exact duplicates are just as broken as case variants: resolver merging
// silently coalesces the rows, and plaintext-key migration writes both values
// into the same normalized credential-store entry, so the second row's key
// overwrites the first.
func ValidatePersistedProviderNames(cfg FileConfig) error {
	seen := make(map[string]string, len(cfg.Providers))
	for _, provider := range cfg.Providers {
		name := strings.TrimSpace(provider.Name)
		folded := credstore.NormalizeProvider(name)
		previous, ok := seen[folded]
		if ok && previous == name {
			return fmt.Errorf("duplicate persisted provider name %q; remove one of the rows in config.json", name)
		}
		if ok {
			return fmt.Errorf("ambiguous persisted provider names %q and %q differ only by case; rename or remove one row in config.json", previous, name)
		}
		seen[folded] = name
	}
	return nil
}

// sameProviderIdentity reports whether two persisted spellings name the same
// provider identity. It is credstore.NormalizeProvider — the credential store's
// own rule — rather than strings.EqualFold, because the two disagree and the
// store is the authority: EqualFold folds "s" and Unicode long-s "ſ" together,
// while the store keeps separate entries for them. Treating them as one identity
// let a mutation of one profile reach the other's row and its secret, which is
// precisely what ValidatePersistedProviderNames permits as a distinct pair.
func sameProviderIdentity(a string, b string) bool {
	return credstore.NormalizeProvider(a) == credstore.NormalizeProvider(b)
}

// SameProviderIdentity exposes the credential store's provider-name identity
// rule to UI and CLI list operations. It deliberately differs from
// strings.EqualFold for Unicode spellings such as "s" and long-s.
func SameProviderIdentity(a string, b string) bool {
	return sameProviderIdentity(strings.TrimSpace(a), strings.TrimSpace(b))
}

// PreflightUserConfig validates existing user config before any command makes
// credential-store side effects.
func PreflightUserConfig(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("config path is required")
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	return ValidatePersistedProviderNames(cfg)
}

// PreflightCatalogProviderLogin is the preflight for a credential login against
// a catalog provider. A login does not mint a new spelling, so a case-variant
// persisted row may be reused only when its catalogId positively proves that it
// owns the requested catalog identity. A matching display name alone is not
// ownership: custom profiles may use catalog-like names for unrelated endpoints.
//
// The collision check still runs when nothing owns the identity, because that is
// the case where a row WILL be created. It is unreachable today given
// EnsureCatalogProvider's own matching, and kept so that if that matching ever
// narrows, this fails fast before the browser flow instead of after it.
func PreflightCatalogProviderLogin(path, catalogID string) error {
	if err := PreflightUserConfig(path); err != nil {
		return err
	}
	descriptor, catalogProvider := providercatalog.Get(catalogID)
	if catalogProvider {
		providers, err := persistedProviders(path)
		if err != nil {
			return err
		}
		if _, owned, err := catalogProviderOwner(providers, descriptor.ID); err != nil {
			return err
		} else if owned {
			return nil
		}
		return PreflightProviderWrite(path, descriptor.ID)
	}
	if _, owned, err := PersistedProviderIdentity(path, catalogID); err != nil {
		return err
	} else if owned {
		return nil
	}
	return PreflightProviderWrite(path, catalogID)
}

// providerOwnsCatalog is the positive ownership test for catalog login and
// persistence. A matching display/name spelling is not ownership: custom
// profiles may legitimately use names such as "OpenRouter" while pointing at a
// different catalog provider or carrying no catalog id at all.
func providerOwnsCatalog(provider ProviderProfile, catalogID string) bool {
	rowCatalogID := strings.TrimSpace(provider.CatalogID)
	return rowCatalogID != "" && sameProviderIdentity(rowCatalogID, catalogID)
}

// catalogProviderOwner resolves the one persisted row that positively owns a
// catalog identity. Catalog ids may normally be shared by named profiles, but a
// catalog-addressed login cannot choose among them: it would store one shared
// provider:<catalog-id> token that logout/status/refresh cannot attribute safely.
// A different row owning the catalog spelling as its NAME is equally ambiguous.
func catalogProviderOwner(providers []ProviderProfile, catalogID string) (ProviderProfile, bool, error) {
	catalogID = strings.TrimSpace(catalogID)
	ownerIndex := -1
	owners := 0
	for index, provider := range providers {
		if providerOwnsCatalog(provider, catalogID) {
			ownerIndex = index
			owners++
		}
	}
	if owners > 1 {
		return ProviderProfile{}, false, fmt.Errorf("provider identity %q is ambiguous: %d saved profiles use it as a catalog id", catalogID, owners)
	}
	if owners == 1 {
		for index, provider := range providers {
			if index != ownerIndex && sameProviderIdentity(provider.Name, catalogID) {
				return ProviderProfile{}, false, fmt.Errorf("provider identity %q is ambiguous: saved profile %q uses it as a name while %q uses it as a catalog id", catalogID, strings.TrimSpace(provider.Name), strings.TrimSpace(providers[ownerIndex].Name))
			}
		}
		return providers[ownerIndex], true, nil
	}
	for _, provider := range providers {
		if sameProviderIdentity(provider.Name, catalogID) {
			return ProviderProfile{}, false, fmt.Errorf("saved profile %q does not prove ownership of catalog provider %q (catalogId is %q)", strings.TrimSpace(provider.Name), catalogID, strings.TrimSpace(provider.CatalogID))
		}
	}
	return ProviderProfile{}, false, nil
}

// AdoptPersistedCatalogProviderName retargets a catalog-named profile at the row
// that already owns its catalog identity, so persisting it UPDATES that row
// instead of colliding with it.
//
// It applies only when the caller took the catalog's own default name (profile
// name == catalog id) AND a persisted row owns that very NAME under a different
// spelling. That is the case where the two spellings are the same provider by
// construction — a re-setup or re-login of, say, openrouter against a row saved
// as "OpenRouter" — and where EnsureCatalogProvider already reuses the row on
// the login path. A user-chosen name is left alone on purpose: there, a case
// collision with an existing row is a real collision, and silently overwriting
// the other row would be worse than the error.
//
// Adoption deliberately does NOT follow the catalog id of an arbitrary row.
// Several profiles may legitimately share one catalog provider — {name:
// "work-xai", catalogId: "xai"} alongside a plain "xai" — so retargeting the
// default "xai" profile at whichever row happened to list catalogId "xai" would
// silently overwrite that row's endpoint, model, and stored key. An exactly
// spelled row short-circuits only after the same positive-ownership check: there
// is nothing to adopt when the caller's spelling and catalog identity already
// match the persisted profile.
//
// Nor is a matching NAME on its own proof of ownership. A custom profile is free
// to be called {name: "OpenRouter", catalogId: "custom-openai-compatible",
// baseURL: "https://corp.example/v1"}; adopting it for `zero providers add
// openrouter` would hand that row to UpsertProvider, whose merge overwrites the
// catalog id, endpoint, model, transport and headers with OpenRouter's defaults
// while a stored-key marker survives the rewrite. So the row's non-empty catalog
// identity must agree too; a missing catalog id is absence of ownership evidence,
// not proof that the row belongs to the requested descriptor. Anything else keeps
// the requested name and lets the write report the collision instead of mutating
// a different profile.
func AdoptPersistedCatalogProviderName(path string, profile ProviderProfile) (ProviderProfile, error) {
	catalogID := strings.TrimSpace(profile.CatalogID)
	name := strings.TrimSpace(profile.Name)
	if catalogID == "" || !sameProviderIdentity(name, catalogID) {
		return profile, nil
	}
	providers, err := persistedProviders(path)
	if err != nil {
		return profile, err
	}
	canonical := ""
	variants := 0
	for _, row := range providers {
		rowName := strings.TrimSpace(row.Name)
		if !sameProviderIdentity(rowName, name) {
			continue
		}
		if !providerOwnsCatalog(row, catalogID) {
			// A differently-cased collision is left for PreflightProviderWrite to
			// report. An exact collision needs an error here because exact-name
			// writes are otherwise valid updates and would rewrite this foreign
			// profile with catalog defaults while preserving its stored-key marker.
			if rowName == name {
				return profile, fmt.Errorf("saved profile %q does not prove ownership of catalog provider %q (catalogId is %q)", rowName, catalogID, strings.TrimSpace(row.CatalogID))
			}
			return profile, nil
		}
		if rowName == name {
			return profile, nil
		}
		canonical = rowName
		variants++
	}
	if variants == 1 {
		profile.Name = canonical
	}
	return profile, nil
}

// PersistedProviderNames returns the exact name of every row in the persisted
// user config, in file order. Callers that must reason about the SET of saved
// rows — e.g. deciding whether removing one row leaves a case variant behind
// that still reads the same credential-store entry — get the raw names here
// rather than re-implementing FileConfig parsing.
func PersistedProviderNames(path string) ([]string, error) {
	providers, err := persistedProviders(path)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		names = append(names, strings.TrimSpace(provider.Name))
	}
	return names, nil
}

// persistedProviders reads the provider rows out of the user config at path.
// A missing file is an empty list, not an error: every caller here asks "what
// is already saved?", and "nothing yet" is a legitimate answer.
func persistedProviders(path string) ([]ProviderProfile, error) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	return cfg.Providers, nil
}

// PersistedProviderIdentity reports whether a persisted user-config row already
// owns identity — as its name (case-insensitively) or as its catalog id — and
// returns that row's exact name.
//
// Callers use it to answer "is this the config's provider, however it was
// spelled or addressed?", which is a different question from ProviderPersisted's
// "is this the exact key a mutator will match". A row saved as
// {name: "my-router", catalogId: "openrouter"} is owned by both spellings here,
// so `zero providers use openrouter` is a wrong way to address a saved profile
// rather than an environment-derived provider.
func PersistedProviderIdentity(path, identity string) (string, bool, error) {
	row, match, err := ResolvePersistedProviderIdentity(path, identity)
	if err != nil || match == PersistedIdentityNone {
		return "", false, err
	}
	return strings.TrimSpace(row.Name), true, nil
}

// PersistedIdentityMatch says HOW a persisted row was addressed, because the
// two ways carry different authority. A name match identifies exactly one
// profile. A catalog-id match identifies "some profile of this kind" — several
// profiles may legitimately share one catalog provider — so callers that go on
// to delete credentials must not treat it as proof of ownership.
type PersistedIdentityMatch uint8

const (
	// PersistedIdentityNone means no row owns the identity, or a catalog id was
	// given that more than one row claims (an ambiguous request this package
	// refuses to guess at).
	PersistedIdentityNone PersistedIdentityMatch = iota
	// PersistedIdentityName means a row's own name matched, exactly or as a
	// case variant.
	PersistedIdentityName
	// PersistedIdentityCatalog means the identity matched only the catalog id of
	// exactly one row.
	PersistedIdentityCatalog
)

// ResolvePersistedProviderIdentity finds the persisted user-config row that
// owns identity and reports how it was addressed.
//
// Names win over catalog ids, and an exact name wins over a case variant.
// Scanning in file order and accepting the first name-OR-catalog hit picked
// whichever row came first, so `zero auth logout xai` against
// [{name:"work-xai", catalogId:"xai"}, {name:"xai"}] resolved to "work-xai" —
// a different profile, with different credentials, than the one named.
//
// A catalog id claimed by more than one row resolves to nothing rather than to
// an arbitrary winner: the caller asked for something the config does not
// uniquely identify, and guessing there is what let one profile's logout delete
// a sibling's shared token.
func ResolvePersistedProviderIdentity(path, identity string) (ProviderProfile, PersistedIdentityMatch, error) {
	identity = strings.TrimSpace(identity)
	if identity == "" {
		return ProviderProfile{}, PersistedIdentityNone, nil
	}
	providers, err := persistedProviders(path)
	if err != nil {
		return ProviderProfile{}, PersistedIdentityNone, err
	}
	var foldedName *ProviderProfile
	var catalogRow *ProviderProfile
	catalogMatches := 0
	for index := range providers {
		row := providers[index]
		name := strings.TrimSpace(row.Name)
		if name == identity {
			return row, PersistedIdentityName, nil
		}
		if foldedName == nil && sameProviderIdentity(name, identity) {
			match := row
			foldedName = &match
		}
		if sameProviderIdentity(row.CatalogID, identity) {
			catalogMatches++
			if catalogRow == nil {
				match := row
				catalogRow = &match
			}
		}
	}
	if foldedName != nil {
		return *foldedName, PersistedIdentityName, nil
	}
	if catalogMatches == 1 {
		return *catalogRow, PersistedIdentityCatalog, nil
	}
	return ProviderProfile{}, PersistedIdentityNone, nil
}

// CatalogIdentityExclusive reports whether catalogID is claimed by the row
// named owner and by nothing else in the persisted config — neither as another
// row's name nor as another row's catalog id.
//
// Credential cleanup uses this before treating the catalog id as one of the
// target profile's own credential keys. With stored-key "work-xai",
// stored-key "xai", and keyless "personal-xai" all carrying catalogId "xai",
// the "xai" token and key belong to whoever logged in under that spelling —
// deleting them while logging out of "work-xai" takes down a sibling's login.
func CatalogIdentityExclusive(path, catalogID, owner string) (bool, error) {
	catalogID = strings.TrimSpace(catalogID)
	owner = strings.TrimSpace(owner)
	if catalogID == "" || owner == "" {
		return false, nil
	}
	providers, err := persistedProviders(path)
	if err != nil {
		return false, err
	}
	for _, row := range providers {
		name := strings.TrimSpace(row.Name)
		if name == owner {
			continue
		}
		if sameProviderIdentity(name, catalogID) || sameProviderIdentity(row.CatalogID, catalogID) {
			return false, nil
		}
	}
	return true, nil
}

// ProviderCredentialCandidates returns every credential-store key that can
// belong exclusively to the addressed persisted profile. The requested spelling
// and canonical row name are always included; a catalog id is included only when
// no sibling row can own credentials under it. Auth status, refresh, and logout
// share this resolver so each command addresses the same stored login.
//
// The canonical name is returned separately for marker mutations. On a config
// read error, callers still receive the requested spelling so logout can delete
// the credential it was explicitly asked to clear before reporting the error.
func ProviderCredentialCandidates(path, addressedName string) (candidates []string, canonicalName string, err error) {
	add := func(candidate string) {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" && !slices.Contains(candidates, candidate) {
			candidates = append(candidates, candidate)
		}
	}
	canonicalName = strings.TrimSpace(addressedName)
	add(canonicalName)
	row, match, err := ResolvePersistedProviderIdentity(path, addressedName)
	if err != nil {
		return candidates, canonicalName, err
	}
	if match == PersistedIdentityNone {
		providers, err := persistedProviders(path)
		if err != nil {
			return candidates, canonicalName, err
		}
		catalogMatches := 0
		for _, provider := range providers {
			if sameProviderIdentity(provider.CatalogID, addressedName) {
				catalogMatches++
			}
		}
		if catalogMatches > 1 {
			return nil, canonicalName, fmt.Errorf("provider identity %q is ambiguous: %d saved profiles use it as a catalog id", addressedName, catalogMatches)
		}
		return candidates, canonicalName, nil
	}
	canonicalName = strings.TrimSpace(row.Name)
	add(canonicalName)
	exclusive, err := CatalogIdentityExclusive(path, row.CatalogID, canonicalName)
	if err != nil {
		return candidates, canonicalName, err
	}
	if exclusive {
		add(row.CatalogID)
	}
	return candidates, canonicalName, nil
}

// PreflightProviderWrite also rejects a new spelling that would share a
// case-insensitive credential key with an existing persisted row.
func PreflightProviderWrite(path, name string) error {
	if err := PreflightUserConfig(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	name = strings.TrimSpace(name)
	for _, provider := range cfg.Providers {
		existing := strings.TrimSpace(provider.Name)
		if sameProviderIdentity(existing, name) && existing != name {
			return fmt.Errorf("provider %q already exists as %q; provider names must be unique case-insensitively", name, existing)
		}
	}
	return nil
}

func UpsertProvider(path string, profile ProviderProfile, setActive bool) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	profile.Name = strings.TrimSpace(profile.Name)
	if profile.Name == "" {
		return FileConfig{}, fmt.Errorf("provider name is required")
	}

	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := ValidatePersistedProviderNames(cfg); err != nil {
		return FileConfig{}, err
	}
	for _, existing := range cfg.Providers {
		if sameProviderIdentity(existing.Name, profile.Name) && strings.TrimSpace(existing.Name) != profile.Name {
			return FileConfig{}, fmt.Errorf("provider %q already exists as %q; provider names must be unique case-insensitively", profile.Name, existing.Name)
		}
	}

	mergeProvider(&cfg, profile)
	// mergeProfile deliberately ignores APIKeyStored — during resolve-time
	// layering a project config must not be able to claim the user's stored
	// keys. This user-config WRITE path re-applies the marker: capturing a key
	// via SecureProviderProfile onto a previously env/no-key profile must
	// persist apiKeyStored, or the secret sits in the credential store while
	// every ApplyStoredAPIKey gate skips it (PR #560 review).
	if profile.APIKeyStored {
		for index := range cfg.Providers {
			if cfg.Providers[index].Name == profile.Name {
				cfg.Providers[index].APIKeyStored = true
				break
			}
		}
	}
	if setActive || strings.TrimSpace(cfg.ActiveProvider) == "" {
		cfg.ActiveProvider = profile.Name
	}

	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// EnsuredProvider reports the outcome of EnsureCatalogProvider: the profile name
// that serves the catalog entry, whether it was newly created, and which provider
// is active after the call (unchanged unless it was blank).
type EnsuredProvider struct {
	Name    string
	Created bool
	Active  string
}

// EnsureCatalogProvider guarantees a provider profile exists in the config at
// path for the given catalog entry. OAuth login flows call this right after
// storing a token: a login is only reachable from the provider list and
// `zero providers use` when a profile exists, but a login must never replace or
// deactivate the user's current active provider — so an existing profile whose
// Name or CatalogID already matches is left completely untouched (its name,
// credentials, and model are the user's), and a created profile is NOT marked
// active unless no provider was active at all.
func EnsureCatalogProvider(path string, catalogID string) (EnsuredProvider, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return EnsuredProvider{}, fmt.Errorf("config path is required")
	}
	descriptor, err := providercatalog.Require(catalogID)
	if err != nil {
		return EnsuredProvider{}, err
	}

	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return EnsuredProvider{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return EnsuredProvider{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := ValidatePersistedProviderNames(cfg); err != nil {
		return EnsuredProvider{}, err
	}
	if owner, owned, err := catalogProviderOwner(cfg.Providers, descriptor.ID); err != nil {
		return EnsuredProvider{}, err
	} else if owned {
		return EnsuredProvider{Name: owner.Name, Active: cfg.ActiveProvider}, nil
	}

	profile := ProviderProfile{
		Name:         descriptor.ID,
		ProviderKind: providerKindForCatalogTransport(descriptor.Transport),
		CatalogID:    descriptor.ID,
		BaseURL:      descriptor.DefaultBaseURL,
		Model:        descriptor.DefaultModel,
	}
	written, err := UpsertProvider(path, profile, false)
	if err != nil {
		return EnsuredProvider{}, err
	}
	return EnsuredProvider{Name: profile.Name, Created: true, Active: written.ActiveProvider}, nil
}

// MarkProviderAPIKeyStored records that provider's API key now lives in the
// credential store. It also clears inline/env key fields so the stored key is the
// runtime credential; an old apiKeyEnv value must not keep overriding a freshly
// captured key from `zero auth openrouter` or provider setup.
func MarkProviderAPIKeyStored(path string, provider string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("config path is required")
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return fmt.Errorf("provider name is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	var cfg FileConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	if err := ValidatePersistedProviderNames(cfg); err != nil {
		return err
	}
	for index := range cfg.Providers {
		if strings.TrimSpace(cfg.Providers[index].Name) == provider {
			cfg.Providers[index].APIKey = ""
			cfg.Providers[index].APIKeyEnv = ""
			cfg.Providers[index].APIKeyStored = true
			return writeConfigFile(path, cfg)
		}
	}
	return fmt.Errorf("provider %q not found", provider)
}

func SetActiveProvider(path string, name string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return FileConfig{}, fmt.Errorf("provider name is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := FileConfig{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}

	for _, provider := range cfg.Providers {
		if strings.TrimSpace(provider.Name) == name {
			cfg.ActiveProvider = provider.Name
			if err := writeConfigFile(path, cfg); err != nil {
				return FileConfig{}, err
			}
			return cfg, nil
		}
	}

	return FileConfig{}, fmt.Errorf("provider %q not found", name)
}

// ProviderPersisted reports whether a provider profile named name actually has
// a row in the config file at path. A provider can appear in the resolved/
// in-memory provider list without ever being written to config.json — e.g.
// applyProviderEnv synthesizes an "openai" profile purely from an ambient
// OPENAI_API_KEY environment variable on every Resolve() call, without ever
// persisting it. RemoveProvider/SetActiveProvider/SetProviderModel only ever
// look at what's on disk, so a caller offering to mutate a provider by name
// should check this first: "not on disk" needs different handling (nothing to
// persist/remove there) than a name that doesn't exist anywhere at all.
func ProviderPersisted(path string, name string) (bool, error) {
	path = strings.TrimSpace(path)
	name = strings.TrimSpace(name)
	if path == "" || name == "" {
		return false, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("stat config %s: %w", path, err)
	}
	cfg, err := loadConfigFile(path)
	if err != nil {
		return false, err
	}
	for _, provider := range cfg.Providers {
		if strings.TrimSpace(provider.Name) == name {
			return true, nil
		}
	}
	return false, nil
}

// ProviderRow returns the persisted user-config row whose Name matches name
// exactly, and whether one was found. Unlike ProviderPersisted's yes/no, this
// hands back the row itself (CatalogID, APIKeyStored, ...) so callers that
// need more than presence — e.g. deciding whether a stored key marker must be
// carried over to a surviving case-variant row before it is removed — don't
// each re-implement FileConfig parsing.
func ProviderRow(path string, name string) (ProviderProfile, bool, error) {
	path = strings.TrimSpace(path)
	name = strings.TrimSpace(name)
	if path == "" || name == "" {
		return ProviderProfile{}, false, nil
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return ProviderProfile{}, false, nil
	} else if err != nil {
		return ProviderProfile{}, false, fmt.Errorf("stat config %s: %w", path, err)
	}
	cfg, err := loadConfigFile(path)
	if err != nil {
		return ProviderProfile{}, false, err
	}
	for _, provider := range cfg.Providers {
		if strings.TrimSpace(provider.Name) == name {
			return provider, true, nil
		}
	}
	return ProviderProfile{}, false, nil
}

// TransferProviderAPIKeyStoredMarker sets the APIKeyStored marker on the row
// named to, without touching any other field on it. The credential store keys
// its secrets case-folded, so removing a case-variant row that owned the
// marker leaves the shared secret orphaned unless a surviving case-variant
// row is marked to take over reading it. No-op (false, nil) when to isn't
// found or already carries the marker.
func TransferProviderAPIKeyStoredMarker(path string, to string) (bool, error) {
	path = strings.TrimSpace(path)
	to = strings.TrimSpace(to)
	if path == "" || to == "" {
		return false, nil
	}
	cfg, err := loadConfigFile(path)
	if err != nil {
		return false, err
	}
	for index := range cfg.Providers {
		if strings.TrimSpace(cfg.Providers[index].Name) == to {
			if cfg.Providers[index].APIKeyStored {
				return false, nil
			}
			cfg.Providers[index].APIKeyStored = true
			return true, writeConfigFile(path, cfg)
		}
	}
	return false, fmt.Errorf("provider %q not found", to)
}

// RemoveProvider deletes the named provider profile from the config at path.
// When the removed profile was active, activeProvider hands off to the first
// remaining provider (or clears when none remain) so the config never points at
// a profile that no longer exists. The caller owns cleaning up the credential
// store entry — config stays pure of secret I/O on the read path, and remove
// keeps that symmetry by only touching config.json.
func RemoveProvider(path string, name string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return FileConfig{}, fmt.Errorf("provider name is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := FileConfig{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}

	// Persisted provider identity is exact. Resolution may fold names from
	// runtime sources, but config mutations must target the requested row. This
	// lookup intentionally precedes validation so an exact removal can repair a
	// case-duplicate config; writeConfigFile validates the resulting config.
	index := -1
	for i, provider := range cfg.Providers {
		if strings.TrimSpace(provider.Name) == name {
			index = i
			break
		}
	}
	if index < 0 {
		return FileConfig{}, fmt.Errorf("provider %q not found", name)
	}
	activeIndex := -1
	activeFoldedIndex := -1
	activeFoldedMatches := 0
	for i, provider := range cfg.Providers {
		providerName := strings.TrimSpace(provider.Name)
		if providerName == strings.TrimSpace(cfg.ActiveProvider) {
			activeIndex = i
		}
		if sameProviderIdentity(providerName, cfg.ActiveProvider) {
			activeFoldedIndex = i
			activeFoldedMatches++
		}
	}
	removedWasActive := activeIndex == index || (activeIndex < 0 && activeFoldedMatches == 1 && activeFoldedIndex == index)
	cfg.Providers = append(cfg.Providers[:index], cfg.Providers[index+1:]...)
	if removedWasActive {
		cfg.ActiveProvider = ""
		if len(cfg.Providers) > 0 {
			cfg.ActiveProvider = cfg.Providers[0].Name
		}
	}
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// RenameProvider renames a provider profile, keeping everything keyed by the
// profile name consistent: the activeProvider pointer follows the rename, and a
// key in the encrypted credential store (APIKeyStored) is migrated to the new
// name BEFORE the config is rewritten — the store write must succeed first so a
// failed migration never strands the config pointing at a key that no longer
// resolves. OAuth tokens are deliberately not migrated: the runtime's login
// candidates fall back to the profile's CatalogID, which every OAuth-capable
// catalog profile carries, so a rename keeps the login reachable.
func RenameProvider(path string, oldName string, newName string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" || newName == "" {
		return FileConfig{}, fmt.Errorf("provider names are required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := FileConfig{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	if err := ValidatePersistedProviderNames(cfg); err != nil {
		return FileConfig{}, err
	}

	// oldName is matched exactly, like ProviderPersisted/SetActiveProvider.
	// newName collides case-insensitively because the credential store retains
	// legacy case-insensitive keys.
	index := -1
	for i, provider := range cfg.Providers {
		providerName := strings.TrimSpace(provider.Name)
		if providerName == oldName {
			index = i
			continue
		}
		if sameProviderIdentity(providerName, newName) {
			return FileConfig{}, fmt.Errorf("provider %q already exists", newName)
		}
	}
	if index < 0 {
		return FileConfig{}, fmt.Errorf("provider %q not found", oldName)
	}
	if sameProviderIdentity(oldName, newName) && cfg.Providers[index].Name == newName {
		return cfg, nil
	}

	previousName := cfg.Providers[index].Name
	keyMigrated := false
	if cfg.Providers[index].APIKeyStored {
		if err := migrateStoredProviderKey(path, previousName, newName); err != nil {
			return FileConfig{}, fmt.Errorf("migrate stored key for %q: %w", oldName, err)
		}
		keyMigrated = true
	}
	if sameProviderIdentity(cfg.ActiveProvider, previousName) {
		cfg.ActiveProvider = newName
	}
	cfg.Providers[index].Name = newName
	if err := writeConfigFile(path, cfg); err != nil {
		if keyMigrated {
			// Compensate best-effort: config.json still names the OLD profile, so
			// move the key back where that config can find it — otherwise a failed
			// rewrite strands the key under a name no profile carries.
			_ = migrateStoredProviderKey(path, newName, previousName)
		}
		return FileConfig{}, err
	}
	return cfg, nil
}

// ProviderEdit is a field-level edit of one saved provider, applied by
// EditProvider in a single atomic write. Name is the CURRENT profile name
// (matched exactly); NewName renames (case-only renames included).
// Empty BaseURL/Model/APIKey mean "leave unchanged"; Description is applied
// VERBATIM (the editor always knows the full desired text, so clearing works).
type ProviderEdit struct {
	Name         string
	NewName      string
	BaseURL      string
	Model        string
	APIKey       string
	APIKeyStored bool
	Description  string
}

// EditProvider applies a provider edit in ONE read-modify-write: rename
// (activeProvider follows; a stored key migrates, with a best-effort rollback
// if the config write fails), field updates, the stored-key marker, and the
// verbatim description. A single write keeps the operation atomic — the
// previous rename+upsert+describe sequence could fail halfway and leave
// config.json renamed while every in-memory consumer still held the old name —
// and a case-only rename (groq -> Groq) remains an in-place update instead of
// an appended duplicate profile.
func EditProvider(path string, edit ProviderEdit) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	oldName := strings.TrimSpace(edit.Name)
	if oldName == "" {
		return FileConfig{}, fmt.Errorf("provider name is required")
	}
	newName := strings.TrimSpace(edit.NewName)
	if newName == "" {
		newName = oldName
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg := FileConfig{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	if err := ValidatePersistedProviderNames(cfg); err != nil {
		return FileConfig{}, err
	}

	index := -1
	newIdentity := credstore.NormalizeProvider(newName)
	for i, provider := range cfg.Providers {
		providerName := strings.TrimSpace(provider.Name)
		if providerName == oldName {
			index = i
			continue
		}
		if credstore.NormalizeProvider(providerName) == newIdentity {
			return FileConfig{}, fmt.Errorf("provider %q already exists", newName)
		}
	}
	if index < 0 {
		return FileConfig{}, fmt.Errorf("provider %q not found", oldName)
	}

	previousName := cfg.Providers[index].Name
	renamed := previousName != newName
	keyMigrated := false
	// A rename moves the stored key along: either the profile's existing entry,
	// or a replacement key the caller just captured — the contract is that a
	// captured key is stored under the CURRENT name before EditProvider runs, so
	// one migration covers both. migrateStoredProviderKey no-ops on case-only
	// renames (the store normalizes names), so it cannot delete the key it just
	// moved.
	if renamed && (cfg.Providers[index].APIKeyStored || edit.APIKeyStored) {
		if err := migrateStoredProviderKey(path, previousName, newName); err != nil {
			return FileConfig{}, fmt.Errorf("migrate stored key for %q: %w", oldName, err)
		}
		keyMigrated = true
	}
	if renamed && sameProviderIdentity(cfg.ActiveProvider, previousName) {
		cfg.ActiveProvider = newName
	}

	profile := &cfg.Providers[index]
	profile.Name = newName
	if baseURL := strings.TrimSpace(edit.BaseURL); baseURL != "" {
		profile.BaseURL = baseURL
	}
	if model := strings.TrimSpace(edit.Model); model != "" {
		profile.Model = model
	}
	if apiKey := strings.TrimSpace(edit.APIKey); apiKey != "" {
		profile.APIKey = apiKey
	}
	if edit.APIKeyStored {
		profile.APIKeyStored = true
	}
	profile.Description = strings.TrimSpace(edit.Description)

	if err := writeConfigFile(path, cfg); err != nil {
		if keyMigrated {
			// Compensate best-effort: config.json still names the OLD profile, so
			// move the key back where that config can find it.
			_ = migrateStoredProviderKey(path, newName, previousName)
		}
		return FileConfig{}, err
	}
	return cfg, nil
}

// migrateStoredProviderKey moves a credential-store entry to a new provider
// name: write-new-then-delete-old, so an interruption can leave a duplicate but
// never a missing key. A missing source entry is a no-op (the marker may be
// stale); only a failed WRITE aborts the rename.
func migrateStoredProviderKey(configPath string, oldName string, newName string) error {
	// The store normalizes names case-insensitively, so a case-only rename
	// (groq -> Groq) targets ONE entry: Set(new) rewrites it in place and
	// Delete(old) would then remove the key that was just "moved". Nothing to
	// migrate — the existing entry already serves the new name.
	if sameProviderIdentity(oldName, newName) {
		return nil
	}
	store, err := ProviderKeyStoreAt(filepath.Dir(configPath))
	if err != nil {
		return err
	}
	key, ok, err := store.Get(oldName)
	if err != nil {
		return err
	}
	if !ok || strings.TrimSpace(key) == "" {
		return nil
	}
	if err := store.Set(newName, key); err != nil {
		return err
	}
	_, _ = store.Delete(oldName)
	return nil
}

func SetProviderModel(path string, name string, model string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return FileConfig{}, fmt.Errorf("provider name is required")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return FileConfig{}, fmt.Errorf("model is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg := FileConfig{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}

	// Persisted provider identity is exact. Resolution may fold names from
	// runtime sources, but config mutations must target the requested row.
	for index := range cfg.Providers {
		if strings.TrimSpace(cfg.Providers[index].Name) == name {
			cfg.Providers[index].Model = model
			if err := writeConfigFile(path, cfg); err != nil {
				return FileConfig{}, err
			}
			return cfg, nil
		}
	}

	return FileConfig{}, fmt.Errorf("provider %q not found", name)
}

func SetFavoriteModels(path string, models []string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}

	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg.Preferences.FavoriteModels = normalizeFavoriteModels(models)
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// SetRecentModels persists the automatic recent-model-switch history,
// mirroring SetFavoriteModels (read-modify-atomic-write). Unlike favorites,
// order is preserved (newest first) rather than sorted, since it reflects
// switch recency, not an alphabetical preference list.
func SetRecentModels(path string, entries []RecentModelEntry) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}

	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}

	cfg.Preferences.RecentModels = NormalizeRecentModels(entries)
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// SetRecapsEnabled persists the idle recap preference, mirroring
// SetFavoriteModels (read-modify-atomic-write).
func SetRecapsEnabled(path string, enabled bool) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	v := enabled
	cfg.Preferences.Recaps = &v
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// SetTheme persists the TUI theme preference, mirroring SetFavoriteModels
// (read-modify-atomic-write). A blank theme clears the stored preference.
func SetTheme(path string, theme string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg.Preferences.Theme = strings.TrimSpace(theme)
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// SetPet persists only the terminal-pet preference while preserving every
// unrelated user setting through the config writer's atomic replace path.
func SetPet(path string, pet string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	cfg := FileConfig{}
	data := []byte("{}")
	if existing, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(existing, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
		data = existing
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	pet = strings.TrimSpace(pet)
	cfg.Preferences.Pet = pet
	data, err := setPetPreferenceJSON(data, pet)
	if err != nil {
		return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
	}
	if err := writeConfigData(path, data); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// SetSTTModel persists the dictation model and its provider, mirroring
// SetTheme (read-modify-atomic-write). provider must be one of the known STT
// provider kinds; a local provider stores the model as stt.localModelPath,
// otherwise as stt.model. A blank model clears the stored value for that slot.
func SetSTTModel(path string, provider STTProviderKind, model string) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if provider != "" {
		cfg.STT.Provider = provider
	}
	model = strings.TrimSpace(model)
	if cfg.STT.STTProvider() == STTProviderLocal {
		cfg.STT.LocalModelPath = model
	} else {
		cfg.STT.Model = model
	}
	if err := validateSTTConfig(cfg.STT); err != nil {
		return FileConfig{}, err
	}
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// SetSTTLocalEngine persists the paths of an auto-downloaded local engine +
// model and switches dictation to the local provider, mirroring SetTheme
// (read-modify-atomic-write). streaming selects the pipeline matching the
// downloaded model (a streaming transducer vs a batch model). Called after a
// download completes.
func SetSTTLocalEngine(path, binary, serverBinary, modelPath string, streaming bool) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg.STT.Provider = STTProviderLocal
	// Point BOTH pipelines at the local engine. Without resetting StreamProvider, a
	// previously-chosen cloud value (deepgram/openai) would still win in
	// buildStreamingTranscriber, so the live transcript would keep hitting the cloud
	// after the user switched to a downloaded local model.
	cfg.STT.StreamProvider = STTProviderLocal
	cfg.STT.LocalBinary = strings.TrimSpace(binary)
	cfg.STT.LocalServerBinary = strings.TrimSpace(serverBinary)
	cfg.STT.LocalModelPath = strings.TrimSpace(modelPath)
	// Match the pipeline to the downloaded model: a streaming transducer drives
	// the websocket server for a live transcript; a batch model uses the offline
	// binary.
	s := streaming
	cfg.STT.Streaming = &s
	if err := validateSTTConfig(cfg.STT); err != nil {
		return FileConfig{}, err
	}
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

// SetSTTProvider persists just the dictation batch provider, mirroring SetTheme.
func SetSTTProvider(path string, provider STTProviderKind) (FileConfig, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return FileConfig{}, fmt.Errorf("config path is required")
	}
	cfg := FileConfig{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			return FileConfig{}, fmt.Errorf("invalid config JSON %s: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return FileConfig{}, fmt.Errorf("read config %s: %w", path, err)
	}
	cfg.STT.Provider = provider
	if err := validateSTTConfig(cfg.STT); err != nil {
		return FileConfig{}, err
	}
	if err := writeConfigFile(path, cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

func normalizeFavoriteModels(models []string) []string {
	seen := map[string]bool{}
	favorites := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		favorites = append(favorites, model)
	}
	sort.Strings(favorites)
	return favorites
}

// NormalizeRecentModels trims, drops entries with no model id, de-duplicates
// by provider+model pair (keeping the first/newest occurrence), and caps the
// result to MaxRecentModels. Order is preserved — the caller is responsible
// for passing entries newest-first. Exported so callers outside this package
// (e.g. the TUI, which keeps its own in-memory copy of recent history) apply
// the exact same normalization rules as the persisted config, instead of
// maintaining a second, independently-drifting copy of this logic.
func NormalizeRecentModels(entries []RecentModelEntry) []RecentModelEntry {
	seen := map[string]bool{}
	recent := make([]RecentModelEntry, 0, len(entries))
	for _, entry := range entries {
		provider := strings.TrimSpace(entry.Provider)
		model := strings.TrimSpace(entry.Model)
		if model == "" {
			continue
		}
		key := strings.ToLower(provider) + "\x00" + model
		if seen[key] {
			continue
		}
		seen[key] = true
		recent = append(recent, RecentModelEntry{Provider: provider, Model: model})
		if len(recent) >= MaxRecentModels {
			break
		}
	}
	return recent
}

func writeConfigFile(path string, cfg FileConfig) error {
	if err := ValidatePersistedProviderNames(cfg); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config JSON: %w", err)
	}
	return writeConfigData(path, data)
}

func writeConfigData(path string, data []byte) error {
	dir := filepath.Dir(path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create config directory %s: %w", dir, err)
		}
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	// Write-to-temp + rename: an in-place write interrupted mid-way (crash,
	// disk full) would leave the user's only config truncated or corrupt.
	tmp, err := os.CreateTemp(dir, ".zero-config-*.tmp")
	if err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure config permissions %s: %w", path, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}
