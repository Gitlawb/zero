package cli

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
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
	privateKeyPath  string
	allowUnverified bool
	version         string
	yes             bool
	check           bool
	refresh         bool
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
	case "update", "refresh":
		return runMarketplaceUpdate(args[1:], stdout, stderr, deps)
	case "sign":
		return runMarketplaceSign(args[1:], stdout, stderr)
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
	catalogEntry, ok, err := findRegisteredCatalog(options.catalogID, cwd, stderr)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if !ok {
		return writeExecUsageError(stderr, fmt.Sprintf("marketplace catalog %q is not registered", options.catalogID))
	}
	var catalog marketplace.Catalog
	var verification marketplace.Verification
	if options.refresh {
		catalog, verification, _, err = updateCatalogCache(context.Background(), catalogEntry)
		if err != nil {
			return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
		}
		if err := updateCatalogVerificationStatus(catalogEntry, verification.Status, cwd); err != nil {
			return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
		}
	} else {
		catalog, verification, err = loadCatalogEntry(context.Background(), catalogEntry)
		if err != nil {
			return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
		}
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
		return writeExecUsageError(stderr, "usage: zero plugins install <id[@catalog]> [--version <version>] [--scope user|project] [--yes] [--allow-unverified] [--json]")
	}
	if !options.yes {
		return writeExecUsageError(stderr, "marketplace installs require --yes")
	}
	pluginID, catalogID := splitMarketplaceTarget(target)
	cwd, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), exitCrash)
	}
	resolved, err := resolveMarketplacePlugin(cwd, pluginID, catalogID, stderr)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if err := requireCatalogVerification(resolved.entry.ID, resolved.verification, options.allowUnverified); err != nil {
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
		Pinned:             strings.TrimSpace(options.version) != "",
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

func runPluginMarketplaceInfo(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	target, options, help, err := parseMarketplaceInstallArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if _, err := fmt.Fprintln(stdout, "Usage:\n  zero plugins info <id[@catalog]> [--version <version>] [--allow-unverified] [--json]"); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if target == "" {
		return writeExecUsageError(stderr, "usage: zero plugins info <id[@catalog]> [--version <version>] [--allow-unverified] [--json]")
	}
	pluginID, catalogID := splitMarketplaceTarget(target)
	cwd, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), exitCrash)
	}
	resolved, err := resolveMarketplacePlugin(cwd, pluginID, catalogID, stderr)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if err := requireCatalogVerification(resolved.entry.ID, resolved.verification, options.allowUnverified); err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	release, ok := selectRelease(resolved.plugin, options.version)
	if !ok {
		return writeExecUsageError(stderr, fmt.Sprintf("marketplace plugin %q has no version %q", pluginID, options.version))
	}
	payload := struct {
		Catalog      string                    `json:"catalog"`
		Verification marketplace.Verification  `json:"verification"`
		Plugin       marketplace.CatalogPlugin `json:"plugin"`
		Release      marketplace.Release       `json:"release"`
		Risk         marketplace.RiskReport    `json:"risk"`
	}{Catalog: resolved.catalog.ID, Verification: resolved.verification, Plugin: resolved.plugin, Release: release, Risk: marketplace.RiskForRelease(release)}
	if options.json {
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "%s@%s from %s (%s)\n", resolved.plugin.ID, release.Version, resolved.catalog.ID, resolved.verification.Status); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runPluginMarketplaceUpdate(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	target, options, help, err := parseMarketplaceUpdateArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if _, err := fmt.Fprintln(stdout, "Usage:\n  zero plugins update [id[@catalog]] [--scope user|project] [--check] [--yes] [--allow-unverified] [--json]"); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if !options.check && !options.yes {
		return writeExecUsageError(stderr, "marketplace updates require --yes")
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
	lock, err := plugins.ReadLock(dir)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	cwd, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), exitCrash)
	}

	targets, err := marketplaceUpdateTargets(target, lock)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	results := make([]pluginUpdateResult, 0, len(targets))
	for _, updateTarget := range targets {
		result, err := runMarketplaceUpdateTarget(cwd, dir, updateTarget, lock[updateTarget.pluginID], options, stderr)
		if err != nil {
			return writeExecUsageError(stderr, err.Error())
		}
		results = append(results, result)
	}

	if options.json {
		payload := struct {
			Results []pluginUpdateResult `json:"results"`
		}{Results: results}
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatPluginUpdateResults(results, options.check)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

type marketplaceUpdateTarget struct {
	pluginID  string
	catalogID string
}

type pluginUpdateResult struct {
	ID              string `json:"id"`
	Catalog         string `json:"catalog,omitempty"`
	CurrentVersion  string `json:"currentVersion,omitempty"`
	Version         string `json:"version,omitempty"`
	CurrentHash     string `json:"currentHash,omitempty"`
	Hash            string `json:"hash,omitempty"`
	Status          string `json:"status"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Pinned          bool   `json:"pinned,omitempty"`
}

func marketplaceUpdateTargets(target string, lock map[string]plugins.LockEntry) ([]marketplaceUpdateTarget, error) {
	if strings.TrimSpace(target) != "" {
		pluginID, catalogID := splitMarketplaceTarget(target)
		if strings.TrimSpace(pluginID) == "" {
			return nil, fmt.Errorf("plugin id is required")
		}
		entry, ok := lock[pluginID]
		if !ok || strings.TrimSpace(entry.Catalog) == "" {
			return nil, fmt.Errorf("plugin %q has no marketplace catalog metadata", pluginID)
		}
		if catalogID == "" {
			catalogID = entry.Catalog
		}
		return []marketplaceUpdateTarget{{pluginID: pluginID, catalogID: catalogID}}, nil
	}
	ids := make([]string, 0, len(lock))
	for id, entry := range lock {
		if strings.TrimSpace(entry.Catalog) != "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	targets := make([]marketplaceUpdateTarget, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, marketplaceUpdateTarget{pluginID: id, catalogID: lock[id].Catalog})
	}
	return targets, nil
}

func runMarketplaceUpdateTarget(cwd string, dir string, target marketplaceUpdateTarget, entry plugins.LockEntry, options marketplaceCommandOptions, stderr io.Writer) (pluginUpdateResult, error) {
	result := pluginUpdateResult{
		ID:             target.pluginID,
		Catalog:        target.catalogID,
		CurrentVersion: entry.Version,
		CurrentHash:    entry.Hash,
		Pinned:         entry.Pinned,
	}
	if entry.Pinned && strings.TrimSpace(options.version) == "" {
		result.Version = entry.Version
		result.Hash = entry.Hash
		result.Status = "pinned"
		return result, nil
	}
	resolved, err := resolveMarketplacePlugin(cwd, target.pluginID, target.catalogID, stderr)
	if err != nil {
		return pluginUpdateResult{}, err
	}
	if err := requireCatalogVerification(resolved.entry.ID, resolved.verification, options.allowUnverified); err != nil {
		return pluginUpdateResult{}, err
	}
	release, ok := selectRelease(resolved.plugin, options.version)
	if !ok {
		return pluginUpdateResult{}, fmt.Errorf("marketplace plugin %q has no version %q", target.pluginID, options.version)
	}
	result.Catalog = resolved.catalog.ID
	result.Version = release.Version
	result.Hash = release.TreeHash
	result.UpdateAvailable = release.Version != entry.Version || release.TreeHash != entry.Hash
	if options.check {
		if result.UpdateAvailable {
			result.Status = "available"
		} else {
			result.Status = "current"
		}
		return result, nil
	}
	if !result.UpdateAvailable {
		result.Status = "current"
		return result, nil
	}
	install, err := plugins.Install(context.Background(), plugins.InstallOptions{
		Source:             release.Repository,
		Dir:                dir,
		Force:              true,
		ExpectedID:         resolved.plugin.ID,
		ExpectedVersion:    release.Version,
		ExpectedComponents: marketplaceInstallInventory(release.Components),
		ExpectedHash:       release.TreeHash,
		Commit:             release.Commit,
		Subdir:             release.Subdir,
		Catalog:            resolved.catalog.ID,
		Pinned:             strings.TrimSpace(options.version) != "",
	})
	if err != nil {
		return pluginUpdateResult{}, err
	}
	result.Hash = install.Hash
	result.Status = "updated"
	return result, nil
}

func formatPluginUpdateResults(results []pluginUpdateResult, check bool) string {
	if len(results) == 0 {
		return "No marketplace-managed plugins found."
	}
	lines := []string{}
	if check {
		lines = append(lines, "Marketplace plugin update check:")
	} else {
		lines = append(lines, "Marketplace plugin updates:")
	}
	for _, result := range results {
		line := fmt.Sprintf("  %s: %s", result.ID, result.Status)
		if result.Version != "" {
			line += " -> " + result.Version
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func runPluginVerify(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	id, options, help, err := parseMarketplaceIDArgs(args, "plugins verify")
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if _, err := fmt.Fprintln(stdout, "Usage:\n  zero plugins verify <id> [--json]"); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if id == "" {
		return writeExecUsageError(stderr, "usage: zero plugins verify <id> [--json]")
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
	result, err := plugins.VerifyInstalled(dir, id)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	if options.json {
		if err := writePrettyJSON(stdout, result); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Verified plugin %s (%s).\n", result.ID, result.Hash); err != nil {
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
	if options.scope == marketplace.ScopeProject {
		excludeProject, trustCheckErrored := resolveTrust(cwd)
		if excludeProject {
			emitTrustNotice(stderr, trustSkip{excludedProjectConfig: true, trustCheckErrored: trustCheckErrored})
			return writeExecUsageError(stderr, "project marketplace catalogs require a trusted workspace; run `zero trust`")
		}
	}
	catalog, verification, cachePath, err := loadCatalogForRegistration(context.Background(), source, parsedSource, options.scope, cwd, options.publicKeyPath)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	if err := requireCatalogVerification(catalog.ID, verification, options.allowUnverified); err != nil {
		return writeExecUsageError(stderr, err.Error())
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
		Scope:              options.scope,
		CachePath:          cachePath,
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
	catalogs, err := registeredCatalogsForCLI(cwd, stderr)
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

func runMarketplaceUpdate(args []string, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	id, options, help, err := parseMarketplaceIDArgs(args, "marketplace update")
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeMarketplaceUpdateHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if id == "" {
		return writeExecUsageError(stderr, "usage: zero plugins marketplace update <id> [--json]")
	}
	cwd, err := deps.getwd()
	if err != nil {
		return writeAppError(stderr, "failed to resolve workspace: "+err.Error(), exitCrash)
	}
	entry, ok, err := findRegisteredCatalog(id, cwd, stderr)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if !ok {
		return writeExecUsageError(stderr, fmt.Sprintf("marketplace catalog %q is not registered", id))
	}
	catalog, verification, cached, err := updateCatalogCache(context.Background(), entry)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	if err := updateCatalogVerificationStatus(entry, verification.Status, cwd); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if options.json {
		payload := struct {
			ID           string                   `json:"id"`
			Cached       bool                     `json:"cached"`
			Verification marketplace.Verification `json:"verification"`
		}{ID: catalog.ID, Cached: cached, Verification: verification}
		if err := writePrettyJSON(stdout, redaction.RedactValue(payload, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if cached {
		if _, err := fmt.Fprintf(stdout, "Updated marketplace catalog %s (%s).\n", catalog.ID, verification.Status); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Marketplace catalog %s is local (%s).\n", catalog.ID, verification.Status); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runMarketplaceSign(args []string, stdout io.Writer, stderr io.Writer) int {
	path, options, help, err := parseMarketplaceSignArgs(args)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}
	if help {
		if err := writeMarketplaceSignHelp(stdout); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if path == "" || options.privateKeyPath == "" {
		return writeExecUsageError(stderr, "usage: zero plugins marketplace sign <path> --private-key <file> [--json]")
	}
	data, _, err := marketplace.ReadLocalCatalog(path)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	if _, err := marketplace.ParseCatalog(data); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	privateKey, err := readEd25519PrivateKey(options.privateKeyPath)
	if err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitUsage)
	}
	signature := ed25519.Sign(privateKey, data)
	signaturePath := marketplace.SignaturePathForCatalog(path)
	if err := os.WriteFile(signaturePath, signature, 0o644); err != nil {
		return writeAppError(stderr, redaction.ErrorMessage(err, redaction.Options{}), exitCrash)
	}
	if options.json {
		if err := writePrettyJSON(stdout, map[string]any{"path": signaturePath, "signed": true}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintf(stdout, "Signed marketplace catalog %s.\n", path); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func validateCatalogPath(path string, publicKeyPath string) (marketplace.Catalog, marketplace.Verification, error) {
	return validateCatalogPathWithPublicKey(path, publicKeyPath, nil)
}

func validateCatalogPathWithPublicKey(path string, publicKeyPath string, publicKey ed25519.PublicKey) (marketplace.Catalog, marketplace.Verification, error) {
	data, signature, err := marketplace.ReadLocalCatalog(path)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, err
	}
	return validateCatalogBytesWithPublicKey(data, signature, publicKeyPath, publicKey)
}

func validateCatalogBytes(data []byte, signature []byte, publicKeyPath string) (marketplace.Catalog, marketplace.Verification, error) {
	return validateCatalogBytesWithPublicKey(data, signature, publicKeyPath, nil)
}

func validateCatalogBytesWithPublicKey(data []byte, signature []byte, publicKeyPath string, publicKey ed25519.PublicKey) (marketplace.Catalog, marketplace.Verification, error) {
	catalog, err := marketplace.ParseCatalog(data)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, err
	}
	if len(publicKey) > 0 {
		return catalog, marketplace.VerifyCatalogSignature(data, signature, publicKey), nil
	}
	if publicKeyPath == "" {
		return catalog, marketplace.Verification{Status: marketplace.VerificationUnsigned}, nil
	}
	filePublicKey, err := readEd25519PublicKey(publicKeyPath)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, err
	}
	return catalog, marketplace.VerifyCatalogSignature(data, signature, filePublicKey), nil
}

func catalogExpectsSignature(publicKeyPath string, publicKey ed25519.PublicKey) bool {
	return strings.TrimSpace(publicKeyPath) != "" || len(publicKey) > 0
}

func validateCatalogSourceRepositories(catalog marketplace.Catalog, source marketplace.CatalogSource) error {
	if source.Kind == marketplace.CatalogSourceLocal {
		return nil
	}
	return marketplace.ValidateRemoteCatalogReleaseSources(catalog)
}

func loadCatalogForRegistration(ctx context.Context, source string, parsedSource marketplace.CatalogSource, scope marketplace.Scope, cwd string, publicKeyPath string) (marketplace.Catalog, marketplace.Verification, string, error) {
	if parsedSource.Kind == marketplace.CatalogSourceLocal {
		catalog, verification, err := validateCatalogPath(source, publicKeyPath)
		return catalog, verification, "", err
	}
	data, signature, err := marketplace.FetchCatalog(ctx, source)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, "", err
	}
	catalog, verification, err := validateCatalogBytes(data, signature, publicKeyPath)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, "", err
	}
	if err := validateCatalogSourceRepositories(catalog, parsedSource); err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, "", err
	}
	if verification.Status == marketplace.VerificationInvalid {
		return marketplace.Catalog{}, marketplace.Verification{}, "", fmt.Errorf("invalid marketplace catalog signature: %s", verification.Error)
	}
	cachePath, err := marketplace.CachePathForScope(scope, cwd, catalog.ID, nil)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, "", err
	}
	if err := marketplace.SaveCachedCatalog(cachePath, data, signature); err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, "", err
	}
	return catalog, verification, cachePath, nil
}

func loadCatalogEntry(ctx context.Context, entry marketplace.RegisteredCatalog) (marketplace.Catalog, marketplace.Verification, error) {
	return loadCatalogEntryWithRefresh(ctx, entry, true)
}

func loadCatalogEntryCachedOnly(entry marketplace.RegisteredCatalog) (marketplace.Catalog, marketplace.Verification, error) {
	return loadCatalogEntryWithRefresh(context.Background(), entry, false)
}

func loadCatalogEntryWithRefresh(ctx context.Context, entry marketplace.RegisteredCatalog, refreshMissingCache bool) (marketplace.Catalog, marketplace.Verification, error) {
	source, err := marketplace.ParseCatalogSource(entry.Source)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, err
	}
	if source.Kind == marketplace.CatalogSourceLocal {
		catalog, verification, err := validateCatalogPathWithPublicKey(entry.Source, entry.PublicKeyPath, entry.PublicKey)
		if err != nil {
			return marketplace.Catalog{}, marketplace.Verification{}, err
		}
		if verification.Status == marketplace.VerificationInvalid {
			return marketplace.Catalog{}, marketplace.Verification{}, fmt.Errorf("invalid marketplace catalog signature: %s", verification.Error)
		}
		if catalogExpectsSignature(entry.PublicKeyPath, entry.PublicKey) && verification.Status != marketplace.VerificationSigned {
			return marketplace.Catalog{}, marketplace.Verification{}, fmt.Errorf("signed marketplace catalog %q is missing a valid signature", entry.ID)
		}
		return catalog, verification, nil
	}
	cachePath := entry.CachePath
	if cachePath == "" {
		return marketplace.Catalog{}, marketplace.Verification{}, fmt.Errorf("catalog %q has no local cache; run `zero plugins marketplace update %s`", entry.ID, entry.ID)
	}
	if _, _, err := marketplace.ReadLocalCatalog(cachePath); err != nil {
		if !refreshMissingCache {
			return marketplace.Catalog{}, marketplace.Verification{}, fmt.Errorf("catalog %q has no local cache; run `zero plugins marketplace update %s`: %w", entry.ID, entry.ID, err)
		}
		if catalog, verification, _, updateErr := updateCatalogCache(ctx, entry); updateErr != nil {
			return marketplace.Catalog{}, marketplace.Verification{}, fmt.Errorf("catalog %q has no local cache; update failed: %w", entry.ID, updateErr)
		} else {
			return catalog, verification, nil
		}
	}
	catalog, verification, err := validateCatalogPathWithPublicKey(cachePath, entry.PublicKeyPath, entry.PublicKey)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, err
	}
	if err := validateCatalogSourceRepositories(catalog, source); err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, err
	}
	if catalog.ID != entry.ID {
		return marketplace.Catalog{}, marketplace.Verification{}, fmt.Errorf("cached catalog id %q does not match registry id %q", catalog.ID, entry.ID)
	}
	if verification.Status == marketplace.VerificationInvalid {
		return marketplace.Catalog{}, marketplace.Verification{}, fmt.Errorf("invalid marketplace catalog signature: %s", verification.Error)
	}
	if catalogExpectsSignature(entry.PublicKeyPath, entry.PublicKey) && verification.Status != marketplace.VerificationSigned {
		return marketplace.Catalog{}, marketplace.Verification{}, fmt.Errorf("signed marketplace catalog %q is missing a valid signature", entry.ID)
	}
	if entry.VerificationStatus == marketplace.VerificationStale {
		verification.Status = marketplace.VerificationStale
	}
	return catalog, verification, nil
}

func updateCatalogCache(ctx context.Context, entry marketplace.RegisteredCatalog) (marketplace.Catalog, marketplace.Verification, bool, error) {
	source, err := marketplace.ParseCatalogSource(entry.Source)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, false, err
	}
	if source.Kind == marketplace.CatalogSourceLocal {
		catalog, verification, err := validateCatalogPathWithPublicKey(entry.Source, entry.PublicKeyPath, entry.PublicKey)
		if err != nil {
			return marketplace.Catalog{}, marketplace.Verification{}, false, err
		}
		if catalog.ID != entry.ID {
			return marketplace.Catalog{}, marketplace.Verification{}, false, fmt.Errorf("local catalog id %q does not match registry id %q", catalog.ID, entry.ID)
		}
		if verification.Status == marketplace.VerificationInvalid {
			return marketplace.Catalog{}, marketplace.Verification{}, false, fmt.Errorf("invalid marketplace catalog signature: %s", verification.Error)
		}
		if catalogExpectsSignature(entry.PublicKeyPath, entry.PublicKey) && verification.Status != marketplace.VerificationSigned {
			return marketplace.Catalog{}, marketplace.Verification{}, false, fmt.Errorf("signed marketplace catalog %q is missing a valid signature", entry.ID)
		}
		return catalog, verification, false, nil
	}
	data, signature, err := marketplace.FetchCatalog(ctx, entry.Source)
	if err != nil {
		return loadStaleCachedCatalog(entry, err)
	}
	catalog, verification, err := validateCatalogBytesWithPublicKey(data, signature, entry.PublicKeyPath, entry.PublicKey)
	if err != nil {
		return loadStaleCachedCatalog(entry, err)
	}
	if err := validateCatalogSourceRepositories(catalog, source); err != nil {
		return loadStaleCachedCatalog(entry, err)
	}
	if catalog.ID != entry.ID {
		return loadStaleCachedCatalog(entry, fmt.Errorf("fetched catalog id %q does not match registry id %q", catalog.ID, entry.ID))
	}
	if verification.Status == marketplace.VerificationInvalid {
		return loadStaleCachedCatalog(entry, fmt.Errorf("invalid marketplace catalog signature: %s", verification.Error))
	}
	if catalogExpectsSignature(entry.PublicKeyPath, entry.PublicKey) && verification.Status != marketplace.VerificationSigned {
		return loadStaleCachedCatalog(entry, fmt.Errorf("signed marketplace catalog %q is missing a valid signature", entry.ID))
	}
	if entry.CachePath == "" {
		return marketplace.Catalog{}, marketplace.Verification{}, false, fmt.Errorf("catalog %q has no cache path", entry.ID)
	}
	if err := marketplace.SaveCachedCatalog(entry.CachePath, data, signature); err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, false, err
	}
	return catalog, verification, true, nil
}

func loadStaleCachedCatalog(entry marketplace.RegisteredCatalog, cause error) (marketplace.Catalog, marketplace.Verification, bool, error) {
	if entry.CachePath == "" {
		return marketplace.Catalog{}, marketplace.Verification{}, false, cause
	}
	catalog, verification, err := validateCatalogPathWithPublicKey(entry.CachePath, entry.PublicKeyPath, entry.PublicKey)
	if err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, false, cause
	}
	source, sourceErr := marketplace.ParseCatalogSource(entry.Source)
	if sourceErr != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, false, cause
	}
	if err := validateCatalogSourceRepositories(catalog, source); err != nil {
		return marketplace.Catalog{}, marketplace.Verification{}, false, cause
	}
	if catalog.ID != entry.ID {
		return marketplace.Catalog{}, marketplace.Verification{}, false, cause
	}
	if verification.Status == marketplace.VerificationInvalid {
		return marketplace.Catalog{}, marketplace.Verification{}, false, cause
	}
	if catalogExpectsSignature(entry.PublicKeyPath, entry.PublicKey) && verification.Status != marketplace.VerificationSigned {
		return marketplace.Catalog{}, marketplace.Verification{}, false, cause
	}
	verification.Status = marketplace.VerificationStale
	if cause != nil {
		verification.Error = cause.Error()
	}
	return catalog, verification, false, nil
}

func requireCatalogVerification(catalogID string, verification marketplace.Verification, allowUnverified bool) error {
	switch verification.Status {
	case marketplace.VerificationSigned:
		return nil
	case marketplace.VerificationInvalid:
		if verification.Error != "" {
			return fmt.Errorf("invalid marketplace catalog %q signature: %s", catalogID, verification.Error)
		}
		return fmt.Errorf("invalid marketplace catalog %q signature", catalogID)
	case marketplace.VerificationUnsigned, marketplace.VerificationStale, "":
		if allowUnverified {
			return nil
		}
		return fmt.Errorf("unsigned marketplace catalog %q requires --allow-unverified", catalogID)
	default:
		return fmt.Errorf("unsupported marketplace catalog %q verification status %q", catalogID, verification.Status)
	}
}

func registeredCatalogs(cwd string, includeProject bool) ([]marketplace.RegisteredCatalog, error) {
	officialCache, _ := marketplace.CachePathForScope(marketplace.ScopeUser, cwd, marketplace.OfficialCatalogID, nil)
	catalogs := []marketplace.RegisteredCatalog{{
		ID:                 marketplace.OfficialCatalogID,
		Source:             marketplace.OfficialCatalogSource,
		PublicKey:          marketplace.OfficialCatalogPublicKey,
		VerificationStatus: marketplace.VerificationSigned,
		Scope:              marketplace.ScopeUser,
		CachePath:          officialCache,
	}}
	scopes := []marketplace.Scope{marketplace.ScopeUser}
	if includeProject {
		scopes = append(scopes, marketplace.ScopeProject)
	}
	for _, scope := range scopes {
		path, err := marketplace.RegistryPathForScope(scope, cwd, nil)
		if err != nil {
			return nil, err
		}
		registry, err := marketplace.LoadRegistry(path)
		if err != nil {
			return nil, err
		}
		for _, entry := range registry.Catalogs {
			entry.Scope = scope
			if entry.VerificationStatus == "" {
				entry.VerificationStatus = marketplace.VerificationUnsigned
			}
			cachePath, err := marketplace.CachePathForScope(scope, cwd, entry.ID, nil)
			if err != nil {
				return nil, err
			}
			entry.CachePath = cachePath
			catalogs = append(catalogs, entry)
		}
	}
	return catalogs, nil
}

func registeredCatalogsForCLI(cwd string, stderr io.Writer) ([]marketplace.RegisteredCatalog, error) {
	excludeProject, trustCheckErrored := resolveTrust(cwd)
	emitTrustNotice(stderr, trustSkip{
		excludedProjectConfig: excludeProject && projectMarketplaceRegistryExists(cwd),
		trustCheckErrored:     trustCheckErrored,
	})
	return registeredCatalogs(cwd, !excludeProject)
}

func findRegisteredCatalog(id string, cwd string, stderr io.Writer) (marketplace.RegisteredCatalog, bool, error) {
	catalogs, err := registeredCatalogsForCLI(cwd, stderr)
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
	entry        marketplace.RegisteredCatalog
	catalog      marketplace.Catalog
	verification marketplace.Verification
	plugin       marketplace.CatalogPlugin
}

func resolveMarketplacePlugin(cwd string, pluginID string, catalogID string, stderr io.Writer) (resolvedMarketplacePlugin, error) {
	if strings.TrimSpace(pluginID) == "" {
		return resolvedMarketplacePlugin{}, fmt.Errorf("plugin id is required")
	}
	catalogs, err := registeredCatalogsForCLI(cwd, stderr)
	if err != nil {
		return resolvedMarketplacePlugin{}, err
	}
	matches := []resolvedMarketplacePlugin{}
	for _, entry := range catalogs {
		if catalogID != "" && entry.ID != catalogID {
			continue
		}
		catalog, verification, err := loadCatalogEntry(context.Background(), entry)
		if err != nil {
			if catalogID != "" {
				return resolvedMarketplacePlugin{}, err
			}
			continue
		}
		for _, plugin := range catalog.Plugins {
			if plugin.ID == pluginID {
				matches = append(matches, resolvedMarketplacePlugin{entry: entry, catalog: catalog, verification: verification, plugin: plugin})
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
	releases := append([]marketplace.Release{}, plugin.Releases...)
	sort.SliceStable(releases, func(left int, right int) bool {
		return compareSemver(releases[left].Version, releases[right].Version) > 0
	})
	return releases[0], true
}

func compareSemver(left string, right string) int {
	leftParts := semverCore(left)
	rightParts := semverCore(right)
	for index := 0; index < 3; index++ {
		if leftParts[index] > rightParts[index] {
			return 1
		}
		if leftParts[index] < rightParts[index] {
			return -1
		}
	}
	return strings.Compare(left, right)
}

func semverCore(version string) [3]int {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if before, _, ok := strings.Cut(version, "-"); ok {
		version = before
	}
	if before, _, ok := strings.Cut(version, "+"); ok {
		version = before
	}
	parts := strings.Split(version, ".")
	var result [3]int
	for index := 0; index < len(parts) && index < 3; index++ {
		value, _ := strconv.Atoi(parts[index])
		result[index] = value
	}
	return result
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

func readEd25519PrivateKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}
	trimmed := strings.TrimSpace(string(data))
	if decoded, err := hex.DecodeString(strings.TrimPrefix(trimmed, "0x")); err == nil && len(decoded) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(decoded), nil
	}
	if decoded, err := base64.StdEncoding.DecodeString(trimmed); err == nil && len(decoded) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(decoded), nil
	}
	if len(data) == ed25519.PrivateKeySize {
		return ed25519.PrivateKey(data), nil
	}
	return nil, fmt.Errorf("private key must be raw, hex, or base64 Ed25519 private key bytes")
}

func updateCatalogVerificationStatus(entry marketplace.RegisteredCatalog, status marketplace.VerificationStatus, cwd string) error {
	if entry.ID == marketplace.OfficialCatalogID {
		return nil
	}
	path, err := marketplace.RegistryPathForScope(entry.Scope, cwd, nil)
	if err != nil {
		return err
	}
	registry, err := marketplace.LoadRegistry(path)
	if err != nil {
		return err
	}
	for index := range registry.Catalogs {
		if registry.Catalogs[index].ID == entry.ID {
			registry.Catalogs[index].VerificationStatus = status
			return marketplace.SaveRegistry(path, registry)
		}
	}
	return nil
}

func projectMarketplaceRegistryExists(workspaceRoot string) bool {
	if workspaceRoot == "" {
		return false
	}
	path, err := marketplace.RegistryPathForScope(marketplace.ScopeProject, workspaceRoot, nil)
	if err != nil {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
			options.refresh = true
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
		case "--allow-unverified":
			options.allowUnverified = true
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

func parseMarketplaceUpdateArgs(args []string) (string, marketplaceCommandOptions, bool, error) {
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
		case "--check":
			options.check = true
		case "--allow-unverified":
			options.allowUnverified = true
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
				return target, options, false, execUsageError{fmt.Sprintf("unknown plugins update flag %q", arg)}
			}
			if target != "" {
				return target, options, false, execUsageError{"plugins update accepts at most one plugin id"}
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

func parseMarketplaceSignArgs(args []string) (string, marketplaceCommandOptions, bool, error) {
	options := marketplaceCommandOptions{}
	path := ""
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch arg {
		case "-h", "--help", "help":
			return path, options, true, nil
		case "--json":
			options.json = true
		case "--private-key":
			index++
			if index >= len(args) || strings.TrimSpace(args[index]) == "" {
				return path, options, false, execUsageError{"--private-key requires a file"}
			}
			options.privateKeyPath = args[index]
		default:
			if strings.HasPrefix(arg, "-") {
				return path, options, false, execUsageError{fmt.Sprintf("unknown marketplace sign flag %q", arg)}
			}
			if path != "" {
				return path, options, false, execUsageError{"marketplace sign accepts a single path"}
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
  update <id>        Refresh a remote catalog cache
  sign <path>        Write detached catalog.sig
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
      --allow-unverified       Allow unsigned catalog install
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

func writeMarketplaceUpdateHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins marketplace update <id> [--json]
`)
	return err
}

func writeMarketplaceSignHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins marketplace sign <path> --private-key <file> [--json]
`)
	return err
}

func writeMarketplaceValidateHelp(w io.Writer) error {
	_, err := fmt.Fprint(w, `Usage:
  zero plugins marketplace validate <path> [--json] [--public-key <file>]
`)
	return err
}
