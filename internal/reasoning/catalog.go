package reasoning

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// modelsdev_snapshot.json is a trimmed snapshot of https://models.dev/api.json:
// for a set of first-party providers, each model's api id mapped to its
// reasoning capability (the `reasoning` flag and `reasoning_options`). Routers
// (OpenRouter/Azure/etc.) are intentionally excluded — they carry generic or
// stale option lists, so a lookup resolves to the first-party provider instead.
// Refresh by re-trimming api.json; runtime refresh is a separate change.
//
//go:embed modelsdev_snapshot.json
var snapshotBytes []byte

// snapshotDoc is the on-disk shape of the embedded snapshot.
type snapshotDoc struct {
	Providers map[string]map[string]Capability `json:"providers"`
}

// Catalog is a reasoning-capability lookup keyed by provider slug and api model
// id. It is read-only after construction.
type Catalog struct {
	byProvider map[string]map[string]Capability
}

// ParseCatalog builds a Catalog from a models.dev-style snapshot document.
func ParseCatalog(data []byte) (Catalog, error) {
	var doc snapshotDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		return Catalog{}, fmt.Errorf("reasoning: parse catalog: %w", err)
	}
	if len(doc.Providers) == 0 {
		return Catalog{}, fmt.Errorf("reasoning: catalog has no providers")
	}
	return Catalog{byProvider: doc.Providers}, nil
}

// embedded is the catalog parsed from the bundled snapshot. A malformed embed is
// a build/release error, so it panics at init rather than failing silently.
var embedded = mustParseEmbedded()

func mustParseEmbedded() Catalog {
	c, err := ParseCatalog(snapshotBytes)
	if err != nil {
		panic("reasoning: invalid embedded snapshot: " + err.Error())
	}
	return c
}

// Embedded returns the catalog parsed from the bundled models.dev snapshot.
func Embedded() Catalog { return embedded }

// Lookup returns the reasoning capability for a model identified by its Zero
// provider kind and api model id (the provider's wire name). The match is exact
// on the api model id; ok is false when the provider or model is not in the
// snapshot, in which case the caller falls back to its next capability source.
func (c Catalog) Lookup(provider, apiModel string) (Capability, bool) {
	apiModel = strings.TrimSpace(apiModel)
	if apiModel == "" {
		return Capability{}, false
	}
	for _, slug := range providerSlugs(provider) {
		models, ok := c.byProvider[slug]
		if !ok {
			continue
		}
		if cap, ok := models[apiModel]; ok {
			// Deep copy so callers cannot mutate the shared catalog through the
			// returned Controls / Values slices or budget pointers.
			return cap.clone(), true
		}
	}
	return Capability{}, false
}

// providerSlugs maps a Zero provider kind to the models.dev provider slug to
// look up. Gemini and Google both resolve to "google" (AI Studio), the
// first-party source for Gemini models.
func providerSlugs(provider string) []string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "gemini":
		return []string{"google"}
	default:
		return []string{strings.ToLower(strings.TrimSpace(provider))}
	}
}
