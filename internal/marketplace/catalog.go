package marketplace

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/plugins"
)

type VerificationStatus string

const (
	VerificationSigned   VerificationStatus = "signed"
	VerificationUnsigned VerificationStatus = "unsigned"
	VerificationStale    VerificationStatus = "stale"
	VerificationInvalid  VerificationStatus = "invalid"
)

type ReviewStatus string

const (
	ReviewStatusReviewed   ReviewStatus = "reviewed"
	ReviewStatusCommunity  ReviewStatus = "community"
	ReviewStatusUnreviewed ReviewStatus = "unreviewed"
)

type Catalog struct {
	SchemaVersion int             `json:"schemaVersion"`
	ID            string          `json:"id"`
	Owner         string          `json:"owner"`
	Description   string          `json:"description,omitempty"`
	Plugins       []CatalogPlugin `json:"plugins"`
}

type CatalogPlugin struct {
	ID          string        `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Author      CatalogAuthor `json:"author"`
	License     string        `json:"license"`
	Homepage    string        `json:"homepage,omitempty"`
	Tags        []string      `json:"tags,omitempty"`
	Category    string        `json:"category,omitempty"`
	Review      ReviewRecord  `json:"review"`
	Releases    []Release     `json:"releases"`
}

type CatalogAuthor struct {
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
	URL   string `json:"url,omitempty"`
}

type ReviewRecord struct {
	Status   ReviewStatus `json:"status"`
	Date     string       `json:"date,omitempty"`
	Reviewer string       `json:"reviewer,omitempty"`
	URL      string       `json:"url,omitempty"`
}

type Release struct {
	Version    string             `json:"version"`
	Repository string             `json:"repository"`
	Commit     string             `json:"commit"`
	Subdir     string             `json:"subdir,omitempty"`
	TreeHash   string             `json:"treeHash"`
	Components ComponentInventory `json:"components"`
}

type ComponentInventory struct {
	Tools   []ToolComponent  `json:"tools,omitempty"`
	Hooks   []HookComponent  `json:"hooks,omitempty"`
	Skills  []NamedComponent `json:"skills,omitempty"`
	Prompts []NamedComponent `json:"prompts,omitempty"`
}

type ToolComponent struct {
	Name       string                 `json:"name"`
	Permission plugins.ToolPermission `json:"permission"`
}

type HookComponent struct {
	Name  string            `json:"name"`
	Event plugins.HookEvent `json:"event"`
}

type NamedComponent struct {
	Name string `json:"name"`
}

type Verification struct {
	Status         VerificationStatus `json:"status"`
	KeyFingerprint string             `json:"keyFingerprint,omitempty"`
	Error          string             `json:"error,omitempty"`
}

type RiskReport struct {
	Tools       []ToolComponent  `json:"tools,omitempty"`
	Hooks       []HookComponent  `json:"hooks,omitempty"`
	Skills      []NamedComponent `json:"skills,omitempty"`
	Prompts     []NamedComponent `json:"prompts,omitempty"`
	Permissions []string         `json:"permissions,omitempty"`
}

type InstalledPlugin struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`
	Catalog     string `json:"catalog,omitempty"`
	Version     string `json:"version,omitempty"`
	Commit      string `json:"commit,omitempty"`
	Subdir      string `json:"subdir,omitempty"`
	Hash        string `json:"hash,omitempty"`
	Pinned      bool   `json:"pinned,omitempty"`
	Enabled     bool   `json:"enabled"`
	Quarantined bool   `json:"quarantined,omitempty"`
}

var (
	idPattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	semverPattern   = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)
	commitPattern   = regexp.MustCompile(`^[A-Fa-f0-9]{40}$`)
	treeHashPattern = regexp.MustCompile(`^sha256:[A-Fa-f0-9]{64}$`)
)

// ParseCatalog parses and validates catalog.json. Validation is intentionally
// strict: catalog metadata is signed, so loose interpretation would make install
// comparisons ambiguous.
func ParseCatalog(data []byte) (Catalog, error) {
	var catalog Catalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return Catalog{}, fmt.Errorf("parse catalog: %w", err)
	}
	if err := ValidateCatalog(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func ValidateCatalog(catalog Catalog) error {
	if catalog.SchemaVersion != 1 {
		return fmt.Errorf("schemaVersion: expected 1")
	}
	if err := validateID("id", catalog.ID); err != nil {
		return err
	}
	if strings.TrimSpace(catalog.Owner) == "" {
		return fmt.Errorf("owner: required")
	}

	seenPlugins := map[string]struct{}{}
	for index, plugin := range catalog.Plugins {
		field := fmt.Sprintf("plugins.%d", index)
		if err := validatePlugin(field, plugin); err != nil {
			return err
		}
		if _, exists := seenPlugins[plugin.ID]; exists {
			return fmt.Errorf("%s.id: duplicate plugin id %q", field, plugin.ID)
		}
		seenPlugins[plugin.ID] = struct{}{}
	}
	return nil
}

func validatePlugin(field string, plugin CatalogPlugin) error {
	if err := validateID(field+".id", plugin.ID); err != nil {
		return err
	}
	if strings.TrimSpace(plugin.Name) == "" {
		return fmt.Errorf("%s.name: required", field)
	}
	if strings.TrimSpace(plugin.Author.Name) == "" {
		return fmt.Errorf("%s.author.name: required", field)
	}
	if strings.TrimSpace(plugin.License) == "" {
		return fmt.Errorf("%s.license: required", field)
	}
	if err := validateReview(field+".review", plugin.Review); err != nil {
		return err
	}
	if len(plugin.Releases) == 0 {
		return fmt.Errorf("%s.releases: at least one release is required", field)
	}
	seenVersions := map[string]struct{}{}
	for index, release := range plugin.Releases {
		releaseField := fmt.Sprintf("%s.releases.%d", field, index)
		if err := validateRelease(releaseField, release); err != nil {
			return err
		}
		if _, exists := seenVersions[release.Version]; exists {
			return fmt.Errorf("%s.version: duplicate release version %q", releaseField, release.Version)
		}
		seenVersions[release.Version] = struct{}{}
	}
	return nil
}

func validateReview(field string, review ReviewRecord) error {
	switch review.Status {
	case ReviewStatusReviewed, ReviewStatusCommunity, ReviewStatusUnreviewed:
	default:
		return fmt.Errorf("%s.status: expected reviewed, community, or unreviewed", field)
	}
	if strings.TrimSpace(review.Date) == "" {
		return fmt.Errorf("%s.date: required", field)
	}
	if _, err := time.Parse("2006-01-02", review.Date); err != nil {
		return fmt.Errorf("%s.date: expected YYYY-MM-DD", field)
	}
	if strings.TrimSpace(review.Reviewer) == "" {
		return fmt.Errorf("%s.reviewer: required", field)
	}
	reviewURL := strings.TrimSpace(review.URL)
	if reviewURL == "" {
		return fmt.Errorf("%s.url: required", field)
	}
	parsed, err := url.Parse(reviewURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s.url: expected absolute URL", field)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return fmt.Errorf("%s.url: expected http or https URL", field)
	}
	return nil
}

func validateRelease(field string, release Release) error {
	if !semverPattern.MatchString(release.Version) {
		return fmt.Errorf("%s.version: expected semantic version", field)
	}
	if err := validateReleaseRepository(field+".repository", release.Repository); err != nil {
		return err
	}
	if !commitPattern.MatchString(release.Commit) {
		return fmt.Errorf("%s.commit: expected 40-character git commit SHA", field)
	}
	if !treeHashPattern.MatchString(release.TreeHash) {
		return fmt.Errorf("%s.treeHash: expected sha256:<64 hex chars>", field)
	}
	if err := validateSafeSubdir(field+".subdir", release.Subdir); err != nil {
		return err
	}
	return validateComponents(field+".components", release.Components)
}

func validateReleaseRepository(field string, repository string) error {
	source, err := ParseCatalogSource(repository)
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	switch source.Kind {
	case CatalogSourceGitHub, CatalogSourceGit:
		return nil
	case CatalogSourceHTTPS:
		return fmt.Errorf("%s: expected a git repository source, got HTTPS catalog/document URL", field)
	case CatalogSourceLocal:
		return nil
	default:
		return fmt.Errorf("%s: unsupported source kind %q", field, source.Kind)
	}
}

func ValidateRemoteCatalogReleaseSources(catalog Catalog) error {
	for pluginIndex, plugin := range catalog.Plugins {
		for releaseIndex, release := range plugin.Releases {
			field := fmt.Sprintf("plugins.%d.releases.%d.repository", pluginIndex, releaseIndex)
			source, err := ParseCatalogSource(release.Repository)
			if err != nil {
				return fmt.Errorf("%s: %w", field, err)
			}
			if source.Kind == CatalogSourceLocal {
				return fmt.Errorf("%s: expected an absolute git repository source, got local path", field)
			}
		}
	}
	return nil
}

func validateComponents(field string, components ComponentInventory) error {
	for index, tool := range components.Tools {
		item := fmt.Sprintf("%s.tools.%d", field, index)
		if err := validateID(item+".name", tool.Name); err != nil {
			return err
		}
		switch tool.Permission {
		case "", plugins.PermissionPrompt, plugins.PermissionAllow, plugins.PermissionDeny:
		default:
			return fmt.Errorf("%s.permission: expected allow, prompt, or deny", item)
		}
	}
	for index, hook := range components.Hooks {
		item := fmt.Sprintf("%s.hooks.%d", field, index)
		if err := validateID(item+".name", hook.Name); err != nil {
			return err
		}
		if !allowedMarketplaceHookEvent(hook.Event) {
			return fmt.Errorf("%s.event: unsupported hook event %q", item, hook.Event)
		}
	}
	for index, skill := range components.Skills {
		if err := validateID(fmt.Sprintf("%s.skills.%d.name", field, index), skill.Name); err != nil {
			return err
		}
	}
	for index, prompt := range components.Prompts {
		if err := validateID(fmt.Sprintf("%s.prompts.%d.name", field, index), prompt.Name); err != nil {
			return err
		}
	}
	return nil
}

func allowedMarketplaceHookEvent(event plugins.HookEvent) bool {
	switch event {
	case plugins.HookBeforeTool, plugins.HookAfterTool, plugins.HookSessionStart, plugins.HookSessionEnd:
		return true
	default:
		return false
	}
}

func validateID(field string, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s: required", field)
	}
	if !idPattern.MatchString(value) {
		return fmt.Errorf("%s: invalid identifier %q", field, value)
	}
	return nil
}

func validateSafeSubdir(field string, subdir string) error {
	subdir = strings.TrimSpace(subdir)
	if subdir == "" {
		return nil
	}
	if strings.Contains(subdir, `\`) || path.IsAbs(subdir) {
		return fmt.Errorf("%s: expected safe relative path", field)
	}
	clean := path.Clean(subdir)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
		return fmt.Errorf("%s: expected safe relative path", field)
	}
	return nil
}

func VerifyCatalogSignature(data []byte, signature []byte, publicKey ed25519.PublicKey) Verification {
	fingerprint := publicKeyFingerprint(publicKey)
	if len(signature) == 0 {
		return Verification{Status: VerificationUnsigned, KeyFingerprint: fingerprint}
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return Verification{Status: VerificationInvalid, Error: "invalid public key"}
	}
	if !ed25519.Verify(publicKey, data, signature) {
		return Verification{Status: VerificationInvalid, KeyFingerprint: fingerprint, Error: "signature mismatch"}
	}
	return Verification{Status: VerificationSigned, KeyFingerprint: fingerprint}
}

func publicKeyFingerprint(publicKey ed25519.PublicKey) string {
	if len(publicKey) == 0 {
		return ""
	}
	sum := sha256.Sum256(publicKey)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func Search(catalog Catalog, query string) []CatalogPlugin {
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return append([]CatalogPlugin{}, catalog.Plugins...)
	}
	matches := []CatalogPlugin{}
	for _, plugin := range catalog.Plugins {
		haystack := strings.ToLower(strings.Join(pluginSearchFields(plugin), " "))
		ok := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				ok = false
				break
			}
		}
		if ok {
			matches = append(matches, plugin)
		}
	}
	return matches
}

func pluginSearchFields(plugin CatalogPlugin) []string {
	fields := []string{plugin.ID, plugin.Name, plugin.Description, plugin.Author.Name, plugin.Category, plugin.License}
	fields = append(fields, plugin.Tags...)
	for _, release := range plugin.Releases {
		for _, tool := range release.Components.Tools {
			fields = append(fields, tool.Name, string(tool.Permission), "tool")
		}
		for _, hook := range release.Components.Hooks {
			fields = append(fields, hook.Name, string(hook.Event), "hook")
		}
		for _, skill := range release.Components.Skills {
			fields = append(fields, skill.Name, "skill")
		}
		for _, prompt := range release.Components.Prompts {
			fields = append(fields, prompt.Name, "prompt")
		}
	}
	sort.Strings(fields)
	return fields
}

func RiskForRelease(release Release) RiskReport {
	permissions := []string{}
	seen := map[string]bool{}
	for _, tool := range release.Components.Tools {
		permission := string(tool.Permission)
		if permission == "" {
			permission = string(plugins.PermissionPrompt)
		}
		if !seen[permission] {
			seen[permission] = true
			permissions = append(permissions, permission)
		}
	}
	sort.Strings(permissions)
	return RiskReport{
		Tools:       append([]ToolComponent{}, release.Components.Tools...),
		Hooks:       append([]HookComponent{}, release.Components.Hooks...),
		Skills:      append([]NamedComponent{}, release.Components.Skills...),
		Prompts:     append([]NamedComponent{}, release.Components.Prompts...),
		Permissions: permissions,
	}
}

type CatalogSourceKind string

const (
	CatalogSourceGitHub CatalogSourceKind = "github"
	CatalogSourceGit    CatalogSourceKind = "git"
	CatalogSourceHTTPS  CatalogSourceKind = "https"
	CatalogSourceLocal  CatalogSourceKind = "local"
)

type CatalogSource struct {
	Kind      CatalogSourceKind `json:"kind"`
	Raw       string            `json:"raw"`
	Canonical string            `json:"canonical"`
}

var githubShorthandPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func ParseCatalogSource(raw string) (CatalogSource, error) {
	source := strings.TrimSpace(raw)
	if source == "" {
		return CatalogSource{}, fmt.Errorf("source is required")
	}
	if strings.Contains(source, "://") {
		parsed, err := url.Parse(source)
		if err != nil {
			return CatalogSource{}, err
		}
		if parsed.User != nil {
			return CatalogSource{}, fmt.Errorf("embedded credentials are not allowed")
		}
		switch parsed.Scheme {
		case "https":
			kind := CatalogSourceHTTPS
			if strings.HasSuffix(parsed.Path, ".git") {
				kind = CatalogSourceGit
			}
			return CatalogSource{Kind: kind, Raw: raw, Canonical: source}, nil
		case "http":
			if !isLoopbackHost(parsed.Hostname()) {
				return CatalogSource{}, fmt.Errorf("unsupported source scheme %q", parsed.Scheme)
			}
			return CatalogSource{Kind: CatalogSourceHTTPS, Raw: raw, Canonical: source}, nil
		case "git", "ssh", "git+ssh", "file":
			return CatalogSource{Kind: CatalogSourceGit, Raw: raw, Canonical: source}, nil
		default:
			return CatalogSource{}, fmt.Errorf("unsupported source scheme %q", parsed.Scheme)
		}
	}
	if githubShorthandPattern.MatchString(source) {
		return CatalogSource{
			Kind:      CatalogSourceGitHub,
			Raw:       raw,
			Canonical: "https://github.com/" + source + ".git",
		}, nil
	}
	if strings.Contains(source, "@") && strings.Contains(source, ":") {
		return CatalogSource{Kind: CatalogSourceGit, Raw: raw, Canonical: source}, nil
	}
	return CatalogSource{Kind: CatalogSourceLocal, Raw: raw, Canonical: source}, nil
}
