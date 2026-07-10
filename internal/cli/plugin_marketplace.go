package cli

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gitlawb/zero/internal/marketplace"
	"github.com/Gitlawb/zero/internal/plugins"
	"github.com/Gitlawb/zero/internal/redaction"
)

type marketplaceCommandOptions struct {
	json            bool
	scope           marketplace.Scope
	catalogID       string
	publicKeyPath   string
	allowUnverified bool
	version         string
	yes             bool
}

func runPluginMarketplace(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	if len(args) == 0 {
		return writeExecUsageError(stderr, "marketplace subcommand required. Use `zero plugins marketplace list`.")
	}
	switch args[0] {
	case "-h", "--help", "help":
		if err := writeMarketplaceHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	case "add":
		return runMarketplaceAdd(args[1:], stdout, stderr, deps)
	case "list":
		return runMarketplaceList(args[1:], stdout, stderr, deps)
	case "remove", "rm":
		return runMarketplaceRemove(args[1:], stdout, stderr, deps)
	case "validate":
		return runMarketplaceValidate(args[1:], stdout, stderr)
	default:
		return writeExecUsageError(stderr, fmt.Sprintf("unknown marketplace subcommand %q", args[0]))
	}
}

func runPluginBrowse(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	query, options, help, err := parseBrowseArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writePluginBrowseHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if options.catalogID == "" {
		options.catalogID = marketplace.OfficialCatalogID
	}
	cwd, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), exitCrash)
	}
	catalogEntry, ok, err := findRegisteredCatalog(options.catalogID, cwd)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if !ok {
		return writeExecUsageError(stderr, fmt.Sprintf("marketplace catalog %q is not registered", options.catalogID))
	}
	catalog, verification, err := loadLocalCatalogEntry(catalogEntry)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	matches := marketplace.Search(catalog, query)
	if options.json {
		payload := struct {
			Catalog      string                      `json:"catalog"`
			Verification marketplace.Verification    `json:"verification"`
			Plugins      []marketplace.CatalogPlugin `json:"plugins"`
		}{Catalog: catalog.ID, Verification: verification, Plugins: matches}
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, redaction.RedactString(formatMarketplaceBrowse(catalog.ID, matches, verification), redaction.Options{})); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runPluginMarketplaceInstall(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	target, options, help, err := parseMarketplaceInstallArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writePluginMarketplaceInstallHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if target == "" {
		return writeExecUsageError(stderr, "usage: zero plugins install <id[@catalog]> [--version <version>] [--scope user|project] [--yes] [--json]")
	}
	if !options.yes {
		return writeExecUsageError(stderr, "marketplace installs require --yes")
	}
	pluginID, catalogID := splitMarketplaceTarget(target)
	cwd, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), exitCrash)
	}
	resolved, err := resolveMarketplacePlugin(cwd, pluginID, catalogID)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	release, ok := selectRelease(resolved.plugin, options.version)
	if !ok {
		if options.version == "" {
			return writeExecUsageError(stderr, fmt.Sprintf("marketplace plugin %q has no releases", pluginID))
		}
		return writeExecUsageError(stderr, fmt.Sprintf("marketplace plugin %q has no version %q", pluginID, options.version))
	}
	if options.scope == "" {
		options.scope = marketplace.ScopeUser
	}
	pluginScope := plugins.SourceUser
	if options.scope == marketplace.ScopeProject {
		pluginScope = plugins.SourceProject
	}
	dir, err := pluginDirForScope(deps, pluginScope)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	result, err := plugins.Install(context.Background(), plugins.InstallOptions{
		Source:             release.Repository,
		Dir:                dir,
		ExpectedID:         resolved.plugin.ID,
		ExpectedVersion:    release.Version,
		ExpectedComponents: marketplaceInstallInventory(release.Components),
		ExpectedHash:       release.TreeHash,
		Commit:             release.Commit,
		Subdir:             release.Subdir,
		Catalog:            resolved.catalog.ID,
	})
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	payload := struct {
		ID           string                 `json:"id"`
		Name         string                 `json:"name"`
		Version      string                 `json:"version"`
		ManifestPath string                 `json:"manifestPath"`
		Hash         string                 `json:"hash"`
		Source       string                 `json:"source"`
		Updated      bool                   `json:"updated"`
		PreviousHash string                 `json:"previousHash,omitempty"`
		Catalog      string                 `json:"catalog"`
		Risk         marketplace.RiskReport `json:"risk"`
	}{
		ID:           result.ID,
		Name:         result.Name,
		Version:      result.Version,
		ManifestPath: result.ManifestPath,
		Hash:         result.Hash,
		Source:       result.Source,
		Updated:      result.Updated,
		PreviousHash: result.PreviousHash,
		Catalog:      resolved.catalog.ID,
		Risk:         marketplace.RiskForRelease(release),
	}
	if options.json {
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Installed plugin %s@%s from %s.\n  hash: %s\n", result.ID, result.Version, resolved.catalog.ID, result.Hash); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func marketplaceInstallInventory(components marketplace.ComponentInventory) *plugins.InstallComponentInventory {
	inventory := plugins.InstallComponentInventory{
		Tools:   make([]plugins.InstallToolComponent, 0, len(components.Tools)),
		Hooks:   make([]plugins.InstallHookComponent, 0, len(components.Hooks)),
		Skills:  make([]string, 0, len(components.Skills)),
		Prompts: make([]string, 0, len(components.Prompts)),
	}
	for _, tool := range components.Tools {
		inventory.Tools = append(inventory.Tools, plugins.InstallToolComponent{Name: tool.Name, Permission: tool.Permission})
	}
	for _, hook := range components.Hooks {
		inventory.Hooks = append(inventory.Hooks, plugins.InstallHookComponent{Name: hook.Name, Event: hook.Event})
	}
	for _, skill := range components.Skills {
		inventory.Skills = append(inventory.Skills, skill.Name)
	}
	for _, prompt := range components.Prompts {
		inventory.Prompts = append(inventory.Prompts, prompt.Name)
	}
	return &inventory
}

func runMarketplaceValidate(args []string, stdout io.Writer, stderr io.Writer) int {
	path, options, help, err := parseMarketplacePathArgs(args, "marketplace validate")
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeMarketplaceValidateHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if path == "" {
		return writeExecUsageError(stderr, "usage: zero plugins marketplace validate <path> [--json] [--public-key <file>]")
	}
	catalog, verification, err := validateCatalogPath(path, options.publicKeyPath)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	if options.json {
		payload := struct {
			Catalog      marketplace.Catalog      `json:"catalog"`
			Verification marketplace.Verification `json:"verification"`
		}{Catalog: catalog, Verification: verification}
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Validated marketplace catalog %s (%s).\n", catalog.ID, verification.Status); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runMarketplaceAdd(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	source, options, help, err := parseMarketplaceAddArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeMarketplaceAddHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if source == "" {
		return writeExecUsageError(stderr, "usage: zero plugins marketplace add <source> [--scope user|project] [--public-key <file>] [--allow-unverified] [--json]")
	}
	if options.scope == "" {
		options.scope = marketplace.ScopeUser
	}
	cwd, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), exitCrash)
	}
	parsedSource, err := marketplace.ParseCatalogSource(source)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if parsedSource.Kind != marketplace.CatalogSourceLocal {
		return writeExecUsageError(stderr, "only local catalog files can be added before a marketplace cache is created")
	}
	catalog, verification, err := validateCatalogPath(source, options.publicKeyPath)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	if verification.Status != marketplace.VerificationSigned && !options.allowUnverified {
		return writeExecUsageError(stderr, "unsigned marketplace catalogs require --allow-unverified")
	}
	path, err := marketplace.RegistryPathForScope(options.scope, cwd, nil)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	registry, err := marketplace.LoadRegistry(path)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	entry := marketplace.RegisteredCatalog{
		ID:                 catalog.ID,
		Source:             source,
		PublicKeyPath:      options.publicKeyPath,
		VerificationStatus: verification.Status,
	}
	if err := registry.Add(entry); err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if err := marketplace.SaveRegistry(path, registry); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, redaction.RedactValue(entry, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Added marketplace catalog %s (%s).\n", entry.ID, entry.VerificationStatus); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runMarketplaceList(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	options, help, err := parseMarketplaceListArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeMarketplaceListHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	cwd, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), exitCrash)
	}
	catalogs, err := registeredCatalogs(cwd)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if options.json {
		payload := struct {
			Catalogs []marketplace.RegisteredCatalog `json:"catalogs"`
		}{Catalogs: catalogs}
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, redaction.RedactString(formatMarketplaceList(catalogs), redaction.Options{})); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runMarketplaceRemove(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	id, options, help, err := parseMarketplaceIDArgs(args, "marketplace remove")
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeMarketplaceRemoveHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if id == "" {
		return writeExecUsageError(stderr, "usage: zero plugins marketplace remove <id> [--scope user|project] [--json]")
	}
	if id == marketplace.OfficialCatalogID {
		return writeExecUsageError(stderr, "official marketplace catalog cannot be removed")
	}
	cwd, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), exitCrash)
	}
	if options.scope == "" {
		options.scope = marketplace.ScopeUser
	}
	path, err := marketplace.RegistryPathForScope(options.scope, cwd, nil)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	registry, err := marketplace.LoadRegistry(path)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	removed := false
	filtered := registry.Catalogs[:0:0]
	for _, catalog := range registry.Catalogs {
		if catalog.ID == id {
			removed = true
			continue
		}
		filtered = append(filtered, catalog)
	}
	if !removed {
		return writeExecUsageError(stderr, fmt.Sprintf("marketplace catalog %q is not registered", id))
	}
	registry.Catalogs = filtered
	if err := marketplace.SaveRegistry(path, registry); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, map[string]any{"id": id, "removed": true}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Removed marketplace catalog %s.\n", id); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func validateCatalogPath(path string, publicKeyPath string) (marketplace.Catalog, marketplace.Verification, error) {
	data, signature, err := marketplace.ReadLocalCatalog(path)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, err
	}
	catalog, err := marketplace.ParseCatalog(data)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, err
	}
	if publicKeyPath == "" {
		return catalog, marketplace.Verification{Status: marketplace.VerificationUnsigned}, nil
	}
	publicKey, err := readEd25519PublicKey(publicKeyPath)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, err
	}
	return catalog, marketplace.VerifyCatalogSignature(data, signature, publicKey), nil
}

func loadLocalCatalogEntry(entry marketplace.RegisteredCatalog) (marketplace.Catalog, marketplace.Verification, error) {
	source, err := marketplace.ParseCatalogSource(entry.Source)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, err
	}
	if source.Kind != marketplace.CatalogSourceLocal {
		return marketplace.Catalog{}, marketplace.Verification{}, fmt.Errorf("catalog %q has no local cache; run `zero plugins marketplace update %s`", entry.ID, entry.ID)
	}
	return validateCatalogPath(entry.Source, entry.PublicKeyPath)
}

func registeredCatalogs(cwd string) ([]marketplace.RegisteredCatalog, error) {
	catalogs := []marketplace.RegisteredCatalog{{
		ID:                 marketplace.OfficialCatalogID,
		Source:             marketplace.OfficialCatalogSource,
		VerificationStatus: marketplace.VerificationUnsigned,
	}}
	for _, scope := range []marketplace.Scope{marketplace.ScopeUser, marketplace.ScopeProject} {
		path, err := marketplace.RegistryPathForScope(scope, cwd, nil)
		if err != nil {
			return nil, err
		}
		registry, err := marketplace.LoadRegistry(path)
		if err != nil {
			return nil, err
		}
		catalogs = append(catalogs, registry.Catalogs...)
	}
	return catalogs, nil
}

func findRegisteredCatalog(id string, cwd string) (marketplace.RegisteredCatalog, bool, error) {
	catalogs, err := registeredCatalogs(cwd)
	if err != nil {
		return marketplace.RegisteredCatalog{}, false, err
	}
	for _, catalog := range catalogs {
		if catalog.ID == id {
			return catalog, true, nil
		}
	}
	return marketplace.RegisteredCatalog{}, false, nil
}

type resolvedMarketplacePlugin struct {
	entry   marketplace.RegisteredCatalog
	catalog marketplace.Catalog
	plugin  marketplace.CatalogPlugin
}

func resolveMarketplacePlugin(cwd string, pluginID string, catalogID string) (resolvedMarketplacePlugin, error) {
	if strings.TrimSpace(pluginID) == "" {
		return resolvedMarketplacePlugin{}, fmt.Errorf("plugin id is required")
	}
	catalogs, err := registeredCatalogs(cwd)
	if err != nil {
		return resolvedMarketplacePlugin{}, err
	}
	matches := []resolvedMarketplacePlugin{}
	for _, entry := range catalogs {
		if catalogID != "" && entry.ID != catalogID {
			continue
		}
		catalog, _, err := loadLocalCatalogEntry(entry)
		if err != nil {
			if catalogID != "" {
				return resolvedMarketplacePlugin{}, err
			}
			continue
		}
		for _, plugin := range catalog.Plugins {
			if plugin.ID == pluginID {
				matches = append(matches, resolvedMarketplacePlugin{entry: entry, catalog: catalog, plugin: plugin})
			}
		}
	}
	if len(matches) == 0 {
		if catalogID != "" {
			return resolvedMarketplacePlugin{}, fmt.Errorf("marketplace plugin %q not found in catalog %q", pluginID, catalogID)
		}
		return resolvedMarketplacePlugin{}, fmt.Errorf("marketplace plugin %q not found", pluginID)
	}
	if len(matches) > 1 {
		ids := make([]string, 0, len(matches))
		for _, match := range matches {
			ids = append(ids, match.catalog.ID)
		}
		return resolvedMarketplacePlugin{}, fmt.Errorf("marketplace plugin %q is ambiguous; use %s@<catalog> (%s)", pluginID, pluginID, strings.Join(ids, ", "))
	}
	return matches[0], nil
}

func splitMarketplaceTarget(target string) (string, string) {
	pluginID := strings.TrimSpace(target)
	catalogID := ""
	if before, after, found := strings.Cut(pluginID, "@"); found {
		pluginID = before
		catalogID = after
	}
	return pluginID, catalogID
}

func selectRelease(plugin marketplace.CatalogPlugin, version string) (marketplace.Release, bool) {
	if len(plugin.Releases) == 0 {
		return marketplace.Release{}, false
	}
	if strings.TrimSpace(version) != "" {
		for _, release := range plugin.Releases {
			if release.Version == version {
				return release, true
			}
		}
		return marketplace.Release{}, false
	}
	return plugin.Releases[0], true
}

func readEd25519PublicKey(path string) (ed25519.PublicKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}
	trimmed := strings.TrimSpace(string(data))
	if decoded, err := hex.DecodeString(strings.TrimPrefix(trimmed, "0x")); err == nil && len(decoded) == ed25519.PublicKeySize {
		return ed25519.PublicKey(decoded), nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(decoded) == ed25519.PublicKeySize {
		return ed25519.PublicKey(decoded), nil
	}
	if len(data) == ed25519.PublicKeySize {
		return ed25519.PublicKey(data), nil
	}
	return nil, fmt.Errorf("public key must be raw, hex, or base64 Ed25519 public key bytes")
}

func parseBrowseArgs(args []string) (string, marketplaceCommandOptions, bool, error) {
	options := marketplaceCommandOptions{}
	query := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-h", "--help", "help":
			return query, options, true, nil
		case "--json":
			options.json = true
		case "--catalog":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return query, options, false, execUsageError{"--catalog requires an id"}
			}
			options.catalogID = args[index]
		case "--refresh":
			return query, options, false, execUsageError{"--refresh is not available before marketplace cache update support"}
		default:
			if strings.HasPrefix(arg, "-") {
				return query, options, false, execUsageError{fmt.Sprintf("unknown plugins browse flag %q", arg)}
			}
			if query != "" {
				return query, options, false, execUsageError{"plugins browse accepts at most one query"}
			}
			query = arg
		}
	}
	return query, options, false, nil
}

func parseMarketplaceAddArgs(args []string) (string, marketplaceCommandOptions, bool, error) {
	options := marketplaceCommandOptions{}
	source := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-h", "--help", "help":
			return source, options, true, nil
		case "--json":
			options.json = true
		case "--allow-unverified":
			options.allowUnverified = true
		case "--scope":
			index++
			if index >= len(args) {
				return source, options, false, execUsageError{"--scope requires user or project"}
			}
			scope, err := parseMarketplaceScope(args[index])
			if err != nil {
				return source, options, false, err
			}
			options.scope = scope
		case "--public-key":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return source, options, false, execUsageError{"--public-key requires a file"}
			}
			options.publicKeyPath = args[index]
		default:
			if strings.HasPrefix(arg, "-") {
				return source, options, false, execUsageError{fmt.Sprintf("unknown marketplace add flag %q", arg)}
			}
			if source != "" {
				return source, options, false, execUsageError{"marketplace add accepts a single source"}
			}
			source = arg
		}
	}
	return source, options, false, nil
}

func parseMarketplaceInstallArgs(args []string) (string, marketplaceCommandOptions, bool, error) {
	options := marketplaceCommandOptions{}
	target := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-h", "--help", "help":
			return target, options, true, nil
		case "--json":
			options.json = true
		case "--yes", "-y":
			options.yes = true
		case "--version":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return target, options, false, execUsageError{"--version requires a value"}
			}
			options.version = args[index]
		case "--scope":
			index++
			if index >= len(args) {
				return target, options, false, execUsageError{"--scope requires user or project"}
			}
			scope, err := parseMarketplaceScope(args[index])
			if err != nil {
				return target, options, false, err
			}
			options.scope = scope
		default:
			if strings.HasPrefix(arg, "-") {
				return target, options, false, execUsageError{fmt.Sprintf("unknown plugins install flag %q", arg)}
			}
			if target != "" {
				return target, options, false, execUsageError{"plugins install accepts a single plugin id"}
			}
			target = arg
		}
	}
	return target, options, false, nil
}

func parseMarketplacePathArgs(args []string, label string) (string, marketplaceCommandOptions, bool, error) {
	options := marketplaceCommandOptions{}
	path := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-h", "--help", "help":
			return path, options, true, nil
		case "--json":
			options.json = true
		case "--public-key":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return path, options, false, execUsageError{"--public-key requires a file"}
			}
			options.publicKeyPath = args[index]
		default:
			if strings.HasPrefix(arg, "-") {
				return path, options, false, execUsageError{fmt.Sprintf("unknown %s flag %q", label, arg)}
			}
			if path != "" {
				return path, options, false, execUsageError{label + " accepts a single path"}
			}
			path = arg
		}
	}
	return path, options, false, nil
}

func parseMarketplaceListArgs(args []string) (marketplaceCommandOptions, bool, error) {
	options := marketplaceCommandOptions{}
	for _, arg := range args {
		switch arg {
		case "-h", "--help", "help":
			return options, true, nil
		case "--json":
			options.json = true
		default:
			return options, false, execUsageError{fmt.Sprintf("unknown marketplace list flag %q", arg)}
		}
	}
	return options, false, nil
}

func parseMarketplaceIDArgs(args []string, label string) (string, marketplaceCommandOptions, bool, error) {
	options := marketplaceCommandOptions{}
	id := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-h", "--help", "help":
			return id, options, true, nil
		case "--json":
			options.json = true
		case "--scope":
			index++
			if index >= len(args) {
				return id, options, false, execUsageError{"--scope requires user or project"}
			}
			scope, err := parseMarketplaceScope(args[index])
			if err != nil {
				return id, options, false, err
			}
			options.scope = scope
		default:
			if strings.HasPrefix(arg, "-") {
				return id, options, false, execUsageError{fmt.Sprintf("unknown %s flag %q", label, arg)}
			}
			if id != "" {
				return id, options, false, execUsageError{label + " accepts a single id"}
			}
			id = arg
		}
	}
	return id, options, false, nil
}

func parseMarketplaceScope(value string) (marketplace.Scope, error) {
	switch marketplace.Scope(value) {
	case marketplace.ScopeUser, marketplace.ScopeProject:
		return marketplace.Scope(value), nil
	default:
		return "", execUsageError{"--scope must be user or project"}
	}
}

func formatMarketplaceBrowse(catalogID string, plugins []marketplace.CatalogPlugin, verification marketplace.Verification) string {
	lines := []string{fmt.Sprintf("Marketplace Plugins (%s, %s):", catalogID, verification.Status)}
	if len(plugins) == 0 {
		return strings.Join(append(lines, "  No plugins found."), "\n")
	}
	for _, plugin := range plugins {
		version := ""
		if len(plugin.Releases) > 0 {
			version = "@" + plugin.Releases[0].Version
		}
		lines = append(lines, fmt.Sprintf("  %s%s %s [%s] - %s", plugin.ID, version, plugin.Name, plugin.Review.Status, plugin.Description))
	}
	return strings.Join(lines, "\n")
}

func formatMarketplaceList(catalogs []marketplace.RegisteredCatalog) string {
	lines := []string{"Marketplace Catalogs:"}
	for _, catalog := range catalogs {
		status := catalog.VerificationStatus
		if status == "" {
			status = marketplace.VerificationUnsigned
		}
		lines = append(lines, fmt.Sprintf("  %s [%s] %s", catalog.ID, status, catalog.Source))
	}
	return strings.Join(lines, "\n")
}

func writeMarketplaceHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins marketplace <command>

Commands:
  add <source>       Add a plugin catalog
  list               List configured plugin catalogs
  remove <id>        Remove a custom plugin catalog
  validate <path>    Validate a catalog.json file
`)
	return err
}

func writePluginBrowseHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins browse [query] [--catalog <id>] [--json]
`)
	return err
}

func writePluginMarketplaceInstallHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins install <id[@catalog]> [flags]

Flags:
      --version <version>      Install exact release and pin lock metadata
      --scope user|project     Install scope (default user)
      --yes                    Confirm install from catalog metadata
      --json                   Print JSON
`)
	return err
}

func writeMarketplaceAddHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins marketplace add <source> [flags]

Flags:
      --scope user|project      Registry scope (default user)
      --public-key <file>       Ed25519 public key for signature verification
      --allow-unverified        Allow unsigned catalogs
      --json                    Print JSON
`)
	return err
}

func writeMarketplaceListHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins marketplace list [--json]
`)
	return err
}

func writeMarketplaceRemoveHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins marketplace remove <id> [--scope user|project] [--json]
`)
	return err
}

func writeMarketplaceValidateHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins marketplace validate <path> [--json] [--public-key <file>]
`)
	return err
}
