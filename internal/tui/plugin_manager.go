package tui

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gitlawb/zero/internal/marketplace"
	"github.com/Gitlawb/zero/internal/plugins"
)

const (
	pluginManagerOverlayMaxWidth = 142
	pluginManagerOverlayMinWidth = 58
	pluginManagerMaxVisible      = 12
)

type pluginManagerState struct {
	selected int
	query    string
	confirm  *pluginCommandConfirmation
}

type pluginCommandConfirmation struct {
	args    []string
	message string
}

type pluginManagerItemKind int

const (
	pluginManagerItemInstalled pluginManagerItemKind = iota
	pluginManagerItemDiagnostic
	pluginManagerItemMarketplacePlugin
	pluginManagerItemCatalog
	pluginManagerItemBrowse
	pluginManagerItemInstall
	pluginManagerItemAddMarketplace
	pluginManagerItemRemoveMarketplace
	pluginManagerItemVerify
	pluginManagerItemListMarketplaces
	pluginManagerItemUpdateMarketplaces
	pluginManagerItemSignCatalog
)

type pluginManagerItem struct {
	Kind      pluginManagerItemKind
	Name      string
	Label     string
	Meta      string
	Detail    string
	Input     string
	CatalogID string
	Scope     string
	Version   string
	Pinned    bool
	Verified  marketplace.VerificationStatus
}

type pluginManagerSnapshot = PluginSnapshot

func (m model) openPluginManager() (model, tea.Cmd) {
	m.pluginManager = &pluginManagerState{}
	m.clearSuggestions()
	if m.pluginCommand == nil {
		return m, nil
	}
	return m.startPluginCommand(pluginCommandRequest{origin: pluginCommandOriginManager, args: []string{"list"}})
}

func (m model) handlePluginManagerKey(msg tea.KeyMsg) (model, tea.Cmd) {
	if m.pluginManager == nil {
		return m, nil
	}
	if m.pluginManager.confirm != nil {
		switch {
		case keyIs(msg, tea.KeyEsc):
			m.pluginManager.confirm = nil
			return m, nil
		case keyIs(msg, tea.KeyEnter):
			confirm := m.pluginManager.confirm
			m.pluginManager.confirm = nil
			return m.runPluginManagerCommand(confirm.args, false)
		default:
			return m, nil
		}
	}
	switch {
	case keyIs(msg, tea.KeyEsc):
		m.pluginManager = nil
	case keyIs(msg, tea.KeyUp):
		m.movePluginManager(-1)
	case keyIs(msg, tea.KeyDown) || keyIs(msg, tea.KeyTab):
		m.movePluginManager(1)
	case keyIs(msg, tea.KeyEnter):
		return m.choosePluginManagerItem()
	case keyBackspace(msg) || keyIs(msg, tea.KeyDelete):
		m.deletePluginManagerQueryRune()
	case keyCtrl(msg, 'u'):
		m.pluginManager.query = ""
		m.pluginManager.selected = 0
	case len(keyRunes(msg)) > 0 && !keyAlt(msg):
		m.appendPluginManagerQuery(keyRunes(msg)...)
	case keyText(msg) != "":
		switch strings.ToLower(keyText(msg)) {
		case "v":
			if item, ok := m.currentPluginManagerItem(); ok && item.Kind == pluginManagerItemInstalled {
				return m.runPluginManagerCommand([]string{"verify", item.Name}, false)
			}
		case "i":
			if item, ok := m.currentPluginManagerItem(); ok && item.Kind == pluginManagerItemMarketplacePlugin {
				return m.runPluginManagerCommand(pluginManagerMarketplaceInstallArgs(item, marketplace.ScopeUser), true)
			}
		case "p":
			if item, ok := m.currentPluginManagerItem(); ok {
				switch item.Kind {
				case pluginManagerItemInstalled:
					args := []string{"pin", item.Name, "--scope", item.Scope}
					if item.Pinned {
						args = []string{"unpin", item.Name, "--scope", item.Scope}
					} else if item.Version != "" {
						args = append(args, "--version", item.Version)
					}
					return m.runPluginManagerCommand(args, true)
				case pluginManagerItemMarketplacePlugin:
					return m.runPluginManagerCommand(pluginManagerMarketplaceInstallArgs(item, marketplace.ScopeProject), true)
				}
			}
		case "e":
			if item, ok := m.currentPluginManagerItem(); ok && item.Kind == pluginManagerItemInstalled {
				return m.runPluginManagerCommand([]string{"enable", item.Name, "--scope", item.Scope}, true)
			}
		case "d":
			if item, ok := m.currentPluginManagerItem(); ok && item.Kind == pluginManagerItemInstalled {
				return m.runPluginManagerCommand([]string{"disable", item.Name, "--scope", item.Scope}, true)
			}
		case "u":
			if item, ok := m.currentPluginManagerItem(); ok {
				switch item.Kind {
				case pluginManagerItemInstalled:
					args := []string{"update", item.Name, "--scope", item.Scope, "--yes"}
					return m.runPluginManagerCommand(args, true)
				case pluginManagerItemCatalog:
					return m.runPluginManagerCommand([]string{"marketplace", "update", item.Name, "--scope", item.Scope}, true)
				}
			}
		case "r":
			if item, ok := m.currentPluginManagerItem(); ok {
				switch item.Kind {
				case pluginManagerItemInstalled:
					return m.runPluginManagerCommand([]string{"remove", item.Name, "--scope", item.Scope}, true)
				case pluginManagerItemCatalog:
					if item.Name != marketplace.OfficialCatalogID {
						return m.runPluginManagerCommand([]string{"marketplace", "remove", item.Name, "--scope", item.Scope}, true)
					}
				}
			}
		}
	}
	return m, nil
}

func (m model) movePluginManager(delta int) model {
	if m.pluginManager == nil {
		return m
	}
	count := len(m.pluginManagerItems())
	if count == 0 {
		m.pluginManager.selected = 0
		return m
	}
	m.pluginManager.selected = ((m.pluginManager.selected+delta)%count + count) % count
	return m
}

func (m model) appendPluginManagerQuery(runes ...rune) {
	if m.pluginManager == nil {
		return
	}
	for _, r := range runes {
		if unicode.IsControl(r) {
			continue
		}
		m.pluginManager.query += string(r)
	}
	m.pluginManager.selected = 0
}

func (m model) deletePluginManagerQueryRune() {
	if m.pluginManager == nil || m.pluginManager.query == "" {
		return
	}
	runes := []rune(m.pluginManager.query)
	m.pluginManager.query = string(runes[:len(runes)-1])
	m.pluginManager.selected = 0
}

func (m model) choosePluginManagerItem() (model, tea.Cmd) {
	item, ok := m.currentPluginManagerItem()
	if !ok || strings.TrimSpace(item.Input) == "" {
		return m, nil
	}
	switch item.Kind {
	case pluginManagerItemInstalled:
		return m.runPluginManagerCommand([]string{"info", item.Name}, false)
	case pluginManagerItemMarketplacePlugin:
		return m.runPluginManagerCommand([]string{"info", item.Name + "@" + item.CatalogID}, false)
	case pluginManagerItemCatalog:
		return m.runPluginManagerCommand([]string{"browse", "--catalog", item.Name}, false)
	}
	return m.prefillPluginManagerCommand(item.Input), nil
}

func (m model) prefillPluginManagerCommand(input string) model {
	m.pluginManager = nil
	m.input.SetValue(input)
	m.input.SetCursor(len([]rune(input)))
	m.resetComposerFromInput()
	m.clearSuggestions()
	return m
}

func (m model) runPluginManagerCommand(args []string, confirm bool) (model, tea.Cmd) {
	if confirm {
		if m.pluginManager != nil {
			m.pluginManager.confirm = &pluginCommandConfirmation{
				args:    append([]string{}, args...),
				message: pluginManagerConfirmationMessage(args),
			}
		}
		return m, nil
	}
	request := pluginCommandRequest{origin: pluginCommandOriginManager, args: append([]string{}, args...)}
	if m.pluginManager != nil {
		request.managerSelected = m.pluginManager.selected
		request.managerQuery = m.pluginManager.query
	}
	return m.startPluginCommand(request)
}

func pluginManagerConfirmationMessage(args []string) string {
	command := "zero plugins " + strings.Join(args, " ")
	if len(args) > 0 && args[0] == "install" && pluginManagerArgContains(args, "--allow-unverified") {
		return "Warning: installing from unsigned/stale catalog with --allow-unverified. Confirm: " + command
	}
	return "Confirm plugin action: " + command
}

func pluginManagerArgContains(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func (m model) currentPluginManagerItem() (pluginManagerItem, bool) {
	if m.pluginManager == nil {
		return pluginManagerItem{}, false
	}
	items := m.pluginManagerItems()
	if len(items) == 0 {
		return pluginManagerItem{}, false
	}
	m.pluginManager.selected = clampInt(m.pluginManager.selected, 0, len(items)-1)
	return items[m.pluginManager.selected], true
}

func (m model) pluginManagerItems() []pluginManagerItem {
	query := ""
	if m.pluginManager != nil {
		query = strings.ToLower(strings.TrimSpace(m.pluginManager.query))
	}
	snapshot := m.pluginManagerSnapshot()
	items := make([]pluginManagerItem, 0, len(snapshot.Plugins)+len(snapshot.Diagnostics)+len(snapshot.MarketplacePlugins)+len(snapshot.Catalogs)+8)
	installed := pluginManagerInstalledIndex(snapshot)
	loaded := map[string]bool{}
	for _, plugin := range snapshot.Plugins {
		loaded[plugin.ID+"|"+string(plugin.Source)] = true
		state := pluginManagerInstalledState(snapshot, plugin, installed)
		item := pluginManagerItem{
			Kind:     pluginManagerItemInstalled,
			Name:     plugin.ID,
			Label:    plugin.ID,
			Meta:     pluginManagerInstalledMeta(plugin, state),
			Detail:   pluginManagerPluginDetail(plugin, state),
			Input:    "/plugins info " + plugin.ID,
			Scope:    string(plugin.Source),
			Version:  state.Version,
			Pinned:   state.Pinned,
			Verified: state.Verification,
		}
		if pluginManagerItemMatches(item, query) {
			items = append(items, item)
		}
	}
	for _, lock := range snapshot.Installed {
		key := lock.ID + "|" + string(lock.Source)
		if lock.Error == "" && (lock.ID == "" || loaded[key]) {
			continue
		}
		label := lock.ID
		if label == "" {
			label = string(lock.Source)
		}
		meta := compactJoin([]string{"broken", string(lock.Source), lock.Catalog, lock.Version}, " · ")
		detail := lock.Error
		if detail == "" {
			detail = "plugins.lock entry exists but the plugin did not load"
		}
		item := pluginManagerItem{
			Kind:    pluginManagerItemDiagnostic,
			Name:    label,
			Label:   label,
			Meta:    meta,
			Detail:  detail,
			Scope:   string(lock.Source),
			Version: lock.Version,
			Pinned:  lock.Pinned,
		}
		if pluginManagerItemMatches(item, query) {
			items = append(items, item)
		}
	}
	for _, diagnostic := range snapshot.Diagnostics {
		label := strings.TrimSpace(diagnostic.PluginID)
		if label == "" {
			label = strings.TrimSpace(diagnostic.PluginPath)
		}
		if label == "" {
			label = string(diagnostic.Kind)
		}
		item := pluginManagerItem{
			Kind:   pluginManagerItemDiagnostic,
			Name:   label,
			Label:  label,
			Meta:   fmt.Sprintf("broken · %s", diagnostic.Kind),
			Detail: diagnostic.Message,
		}
		if pluginManagerItemMatches(item, query) {
			items = append(items, item)
		}
	}
	for _, entry := range snapshot.MarketplacePlugins {
		item := pluginManagerMarketplaceItem(entry)
		if pluginManagerItemMatches(item, query) {
			items = append(items, item)
		}
	}
	for _, catalog := range snapshot.Catalogs {
		item := pluginManagerCatalogItem(catalog)
		if pluginManagerItemMatches(item, query) {
			items = append(items, item)
		}
	}
	for _, item := range []pluginManagerItem{
		{Kind: pluginManagerItemBrowse, Name: "browse", Label: "Browse marketplaces", Meta: "zero plugins browse", Detail: "search registered catalogs before install", Input: "/plugins browse"},
		{Kind: pluginManagerItemInstall, Name: "install", Label: "Install plugin", Meta: "zero plugins install <id@catalog> --scope user --yes [--allow-unverified]", Detail: "install reviewed release; unsigned or stale catalogs require explicit allow", Input: "/plugins install "},
		{Kind: pluginManagerItemAddMarketplace, Name: "add", Label: "Add marketplace", Meta: "zero plugins marketplace add <source>", Detail: "register signed catalog source; unsigned catalogs require explicit allow", Input: "/plugins marketplace add "},
		{Kind: pluginManagerItemRemoveMarketplace, Name: "remove-marketplace", Label: "Remove marketplace", Meta: "zero plugins marketplace remove <id>", Detail: "remove user or project catalog registration", Input: "/plugins marketplace remove "},
		{Kind: pluginManagerItemVerify, Name: "verify", Label: "Verify installed", Meta: "zero plugins verify <id>", Detail: "check installed content against plugins.lock", Input: "/plugins verify "},
		{Kind: pluginManagerItemListMarketplaces, Name: "marketplaces", Label: "List marketplaces", Meta: "zero plugins marketplace list", Detail: "show registered catalogs and verification status", Input: "/plugins marketplace list"},
		{Kind: pluginManagerItemUpdateMarketplaces, Name: "update", Label: "Update catalogs", Meta: "zero plugins marketplace update <id>", Detail: "refresh remote catalog cache and signature status", Input: "/plugins marketplace update "},
		{Kind: pluginManagerItemSignCatalog, Name: "sign", Label: "Sign catalog", Meta: "zero plugins marketplace sign <path>", Detail: "write detached Ed25519 catalog signature", Input: "/plugins marketplace sign "},
	} {
		if pluginManagerItemMatches(item, query) {
			items = append(items, item)
		}
	}
	return items
}

type pluginManagerInstalledSummary struct {
	Catalog      string
	Version      string
	Commit       string
	Enabled      bool
	Pinned       bool
	Verification marketplace.VerificationStatus
}

func pluginManagerInstalledIndex(snapshot pluginManagerSnapshot) map[string]PluginInstalledSnapshot {
	index := map[string]PluginInstalledSnapshot{}
	for _, item := range snapshot.Installed {
		if strings.TrimSpace(item.ID) == "" {
			continue
		}
		index[item.ID+"|"+string(item.Source)] = item
	}
	return index
}

func pluginManagerInstalledState(snapshot pluginManagerSnapshot, plugin plugins.LoadedPlugin, installed map[string]PluginInstalledSnapshot) pluginManagerInstalledSummary {
	state := pluginManagerInstalledSummary{
		Version:      plugin.Version,
		Enabled:      plugin.Enabled,
		Verification: marketplace.VerificationSigned,
	}
	lock, ok := installed[plugin.ID+"|"+string(plugin.Source)]
	if ok {
		state.Catalog = lock.Catalog
		if lock.Version != "" {
			state.Version = lock.Version
		}
		state.Commit = lock.Commit
		state.Pinned = lock.Pinned
		if lock.Enabled != nil {
			state.Enabled = *lock.Enabled
		}
	}
	for _, catalog := range snapshot.Catalogs {
		if catalog.ID == state.Catalog {
			state.Verification = catalog.Verification.Status
			break
		}
	}
	return state
}

func pluginManagerInstalledMeta(plugin plugins.LoadedPlugin, state pluginManagerInstalledSummary) string {
	parts := []string{"enabled"}
	if !state.Enabled {
		parts[0] = "disabled"
	}
	parts = append(parts, string(plugin.Source))
	if state.Version != "" {
		parts = append(parts, state.Version)
	}
	if state.Catalog != "" {
		parts = append(parts, state.Catalog)
	}
	if state.Pinned {
		parts = append(parts, "pinned")
	}
	if state.Verification == marketplace.VerificationStale {
		parts = append(parts, "stale catalog")
	}
	return strings.Join(parts, " · ")
}

func pluginManagerMarketplaceItem(entry PluginMarketplaceSnapshot) pluginManagerItem {
	version := entry.Release.Version
	review := string(entry.Plugin.Review.Status)
	if review == "" {
		review = "unreviewed"
	}
	verification := entry.Verification.Status
	if verification == "" {
		verification = marketplace.VerificationUnsigned
	}
	meta := []string{entry.Plugin.ID + "@" + entry.CatalogID, version, review, entry.Plugin.License, string(verification)}
	if entry.Installed {
		meta = append(meta, "installed")
	}
	if entry.Pinned {
		meta = append(meta, "pinned")
	}
	name := strings.TrimSpace(entry.Plugin.Name)
	if name == "" {
		name = entry.Plugin.ID
	}
	input := "/plugins info " + entry.Plugin.ID + "@" + entry.CatalogID
	return pluginManagerItem{
		Kind:      pluginManagerItemMarketplacePlugin,
		Name:      entry.Plugin.ID,
		Label:     name,
		Meta:      compactJoin(meta, " · "),
		Detail:    pluginManagerMarketplaceDetail(entry),
		Input:     input,
		CatalogID: entry.CatalogID,
		Scope:     string(entry.CatalogScope),
		Version:   version,
		Pinned:    entry.Pinned,
		Verified:  verification,
	}
}

func pluginManagerCatalogItem(catalog PluginCatalogSnapshot) pluginManagerItem {
	verification := catalog.Verification.Status
	if verification == "" {
		verification = marketplace.VerificationUnsigned
	}
	meta := []string{string(catalog.Scope), string(verification), catalog.Source}
	if catalog.LoadError != "" {
		meta = append([]string{"load error"}, meta...)
	}
	detail := catalog.Source
	if catalog.Description != "" {
		detail = catalog.Description + " · " + detail
	}
	if catalog.LoadError != "" {
		detail = catalog.LoadError
	}
	return pluginManagerItem{
		Kind:     pluginManagerItemCatalog,
		Name:     catalog.ID,
		Label:    catalog.ID,
		Meta:     compactJoin(meta, " · "),
		Detail:   detail,
		Input:    "/plugins browse --catalog " + catalog.ID,
		Scope:    string(catalog.Scope),
		Verified: verification,
	}
}

func pluginManagerMarketplaceInstallArgs(item pluginManagerItem, scope marketplace.Scope) []string {
	return []string{"install", item.Name + "@" + item.CatalogID, "--scope", string(scope), "--yes"}
}

func pluginManagerMarketplaceDetail(entry PluginMarketplaceSnapshot) string {
	risk := []string{
		pluralCount(len(entry.Risk.Tools), "tool"),
		pluralCount(len(entry.Risk.Hooks), "hook"),
		pluralCount(len(entry.Risk.Skills), "skill"),
		pluralCount(len(entry.Risk.Prompts), "prompt"),
	}
	parts := []string{}
	if entry.Plugin.Description != "" {
		parts = append(parts, entry.Plugin.Description)
	}
	parts = append(parts, "risk "+strings.Join(risk, ", "))
	if entry.Plugin.Review.Reviewer != "" || entry.Plugin.Review.Date != "" {
		parts = append(parts, "review "+compactJoin([]string{string(entry.Plugin.Review.Status), entry.Plugin.Review.Date, entry.Plugin.Review.Reviewer}, " "))
	}
	if entry.Release.Repository != "" {
		parts = append(parts, entry.Release.Repository)
	}
	return strings.Join(parts, " · ")
}

func compactJoin(parts []string, sep string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, sep)
}

func pluginManagerItemMatches(item pluginManagerItem, query string) bool {
	if query == "" {
		return true
	}
	fields := []string{item.Name, item.Label, item.Meta, item.Detail, item.Input, item.CatalogID, item.Scope, item.Version}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

func (m model) pluginManagerOverlay(width int) string {
	if m.pluginManager == nil {
		return ""
	}
	if width <= 0 {
		width = defaultStartupWidth
	}
	overlayWidth := minInt(width, pluginManagerOverlayMaxWidth)
	if overlayWidth < pluginManagerOverlayMinWidth {
		overlayWidth = width
	}
	innerWidth := maxInt(1, overlayWidth-4)
	items := m.pluginManagerItems()
	if len(items) > 0 {
		m.pluginManager.selected = clampInt(m.pluginManager.selected, 0, len(items)-1)
	}

	snapshot := m.pluginManagerSnapshot()
	lines := []string{
		fillPaletteLine(zeroTheme.ink.Bold(true).Render(pluginManagerSummary(snapshot)), innerWidth, transparentSurface),
		fillPaletteLine(renderPluginManagerSearchLine(m.pluginManager.query, innerWidth), innerWidth, transparentSurface),
	}
	if len(snapshot.Plugins) == 0 {
		lines = append(lines, zeroTheme.faint.Render("  No local Zero plugins loaded."))
	}
	if snapshot.TrustNotice != "" {
		lines = append(lines, zeroTheme.amber.Render("  "+snapshot.TrustNotice))
	}
	if snapshot.LoadError != "" {
		lines = append(lines, zeroTheme.amber.Render("  "+snapshot.LoadError))
	}
	if m.pluginManager.confirm != nil {
		lines = append(lines, zeroTheme.amber.Render("  "+m.pluginManager.confirm.message))
		lines = append(lines, zeroTheme.faint.Render("  Enter confirm   Esc cancel"))
	}
	itemLines := m.renderPluginManagerItemLines(innerWidth, items)
	lines = append(lines, itemLines...)
	if detail := m.pluginManagerSelectionDetail(innerWidth); len(detail) > 0 {
		lines = append(lines, zeroTheme.line.Render(strings.Repeat("─", innerWidth)))
		lines = append(lines, detail...)
	}
	lines = append(lines, zeroTheme.line.Render(strings.Repeat("─", innerWidth)))
	lines = append(lines, fillPaletteLine(zeroTheme.faint.Render("type search   up/down navigate   Enter info   Alt+v verify   Alt+d disable   Esc close"), innerWidth, transparentSurface))
	return centerRenderedBlock(styledBlockFillTitle(overlayWidth, "Manage plugins", lines, zeroTheme.lineStrong, lipgloss.NewStyle()), width)
}

func renderPluginManagerSearchLine(query string, width int) string {
	query = strings.TrimSpace(query)
	prompt := zeroTheme.userPrompt.Render("search > ")
	if query == "" {
		return fitStyledLine(prompt+zeroTheme.faint.Render("plugins, catalogs, marketplace actions..."), width)
	}
	return fitStyledLine(prompt+zeroTheme.ink.Render(query), width)
}

func (m model) renderPluginManagerItemLines(width int, items []pluginManagerItem) []string {
	if len(items) == 0 {
		return []string{fillPaletteLine(zeroTheme.faint.Render("  no plugin actions"), width, transparentSurface)}
	}
	maxVisible := minInt(pluginManagerMaxVisible, len(items))
	start := selectableListStart(len(items), maxVisible, m.pluginManager.selected)
	visible := items[start : start+maxVisible]
	lines := make([]string, 0, len(visible))
	lastGroup := ""
	for offset, item := range visible {
		index := start + offset
		if group := pluginManagerItemGroup(item.Kind); group != "" && group != lastGroup {
			lines = append(lines, zeroTheme.accent.Bold(true).Render(group))
			lastGroup = group
		}
		surface := transparentSurface
		marker := surface(zeroTheme.faintest).Render("  ")
		if index == m.pluginManager.selected {
			surface = zeroTheme.onSel
			marker = surface(zeroTheme.accent).Render("› ")
		}
		left := marker + surface(zeroTheme.ink).Render(item.Label)
		right := ""
		if item.Meta != "" {
			right = surface(zeroTheme.faint).Render(item.Meta)
		}
		gap := width - lipgloss.Width(left) - lipgloss.Width(right)
		line := left + surface(zeroTheme.ink).Render(strings.Repeat(" ", maxInt(1, gap))) + right
		lines = append(lines, fillPaletteLine(line, width, surface))
	}
	return lines
}

func pluginManagerItemGroup(kind pluginManagerItemKind) string {
	switch kind {
	case pluginManagerItemInstalled:
		return "Installed"
	case pluginManagerItemDiagnostic:
		return "Installed issues"
	case pluginManagerItemMarketplacePlugin:
		return "Discover"
	case pluginManagerItemCatalog:
		return "Catalogs"
	case pluginManagerItemBrowse, pluginManagerItemInstall, pluginManagerItemAddMarketplace, pluginManagerItemRemoveMarketplace, pluginManagerItemVerify, pluginManagerItemListMarketplaces, pluginManagerItemUpdateMarketplaces, pluginManagerItemSignCatalog:
		return "Actions"
	default:
		return "Actions"
	}
}

func (m model) pluginManagerSelectionDetail(width int) []string {
	item, ok := m.currentPluginManagerItem()
	if !ok {
		return nil
	}
	lines := []string{}
	if item.Detail != "" {
		lines = append(lines, fillPaletteLine(zeroTheme.faint.Render(item.Detail), width, transparentSurface))
	}
	if item.Input != "" {
		lines = append(lines, fillPaletteLine(zeroTheme.ink.Render(item.Input), width, transparentSurface))
	}
	switch item.Kind {
	case pluginManagerItemInstalled:
		lines = append(lines, fillPaletteLine(zeroTheme.faint.Render("Enter info   Alt+v verify   Alt+e enable   Alt+d disable   Alt+u update   Alt+r remove"), width, transparentSurface))
		lines = append(lines, fillPaletteLine(zeroTheme.faint.Render("Alt+p pin/unpin"), width, transparentSurface))
	case pluginManagerItemMarketplacePlugin:
		lines = append(lines, fillPaletteLine(zeroTheme.faint.Render("Enter info   Alt+i install user   Alt+p install project"), width, transparentSurface))
	case pluginManagerItemCatalog:
		lines = append(lines, fillPaletteLine(zeroTheme.faint.Render("Enter browse   Alt+u refresh   Alt+r remove catalog"), width, transparentSurface))
	}
	return lines
}

func (m model) pluginManagerSnapshot() pluginManagerSnapshot {
	if m.pluginSnapshotReady {
		return m.pluginSnapshot
	}
	return pluginManagerSnapshot{}
}

func pluginManagerSummary(snapshot pluginManagerSnapshot) string {
	parts := []string{pluralCount(len(snapshot.Plugins), "plugin")}
	if len(snapshot.MarketplacePlugins) > 0 {
		parts = append(parts, pluralCount(len(snapshot.MarketplacePlugins), "marketplace plugin"))
	}
	if len(snapshot.Catalogs) > 0 {
		parts = append(parts, pluralCount(len(snapshot.Catalogs), "catalog"))
	}
	if len(snapshot.Diagnostics) > 0 {
		parts = append(parts, pluralCount(len(snapshot.Diagnostics), "diagnostic"))
	}
	if snapshot.ProjectPluginsShown {
		parts = append(parts, "project enabled")
	} else {
		parts = append(parts, "user scope")
	}
	return strings.Join(parts, " · ")
}

func pluginManagerPluginDetail(plugin plugins.LoadedPlugin, state pluginManagerInstalledSummary) string {
	counts := []string{
		pluralCount(len(plugin.Tools), "tool"),
		pluralCount(len(plugin.Prompts), "prompt"),
		pluralCount(len(plugin.Skills), "skill"),
		pluralCount(len(plugin.Hooks), "hook"),
	}
	name := strings.TrimSpace(plugin.Name)
	if name == "" {
		name = plugin.ID
	}
	parts := []string{fmt.Sprintf("%s: %s", name, strings.Join(counts, ", "))}
	if state.Catalog != "" {
		parts = append(parts, "catalog "+state.Catalog)
	}
	if state.Pinned {
		parts = append(parts, "pinned")
	}
	if state.Commit != "" {
		parts = append(parts, "commit "+state.Commit[:minInt(len(state.Commit), 12)])
	}
	return strings.Join(parts, " · ")
}

func (m model) pluginsText(args string) string {
	snapshot := m.pluginManagerSnapshot()
	lines := strings.Split(plugins.FormatList(snapshot.Plugins, snapshot.Diagnostics), "\n")
	if snapshot.TrustNotice != "" {
		lines = append(lines, snapshot.TrustNotice)
	}
	if snapshot.LoadError != "" {
		lines = append(lines, snapshot.LoadError)
	}
	return renderCommandOutput(commandOutput{
		Title:  "Plugins",
		Status: pluginCommandStatus(snapshot),
		Sections: []commandSection{{
			Title: "Requested",
			Fields: []commandField{
				{Key: "command", Value: strings.TrimSpace(args)},
			},
		}, {
			Title: "Installed",
			Lines: lines,
		}},
		Hints: []string{
			"open manager: /plugins",
			"CLI: zero plugins browse | zero plugins install <id@catalog> --scope user --yes [--allow-unverified] | zero plugins verify <id>",
		},
	})
}

func (m model) startPluginTranscriptCommand(args string) (model, tea.Cmd) {
	args = strings.TrimSpace(args)
	if args == "" {
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "plugins", text: m.pluginsText(args)})
		return m, nil
	}
	parsedArgs, err := splitMCPCommandArgs(args)
	if err != nil {
		text := strings.Join([]string{
			"Plugin action failed",
			err.Error(),
			"",
			m.pluginsText(args),
		}, "\n")
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "plugins", text: text})
		return m, nil
	}
	return m.startPluginCommand(pluginCommandRequest{origin: pluginCommandOriginTranscript, raw: args, args: parsedArgs})
}

func (m model) startPluginCommand(request pluginCommandRequest) (model, tea.Cmd) {
	if m.pluginCommand == nil {
		result := PluginCommandResult{
			ExitCode: 1,
			Error:    "Plugin action unavailable",
			Snapshot: m.pluginSnapshot,
		}
		return m.applyPluginCommandResultMessage(pluginCommandResultMsg{request: request, result: result}), nil
	}
	m.cancelPluginCommand()
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	m.pluginCommandSeq++
	request.id = m.pluginCommandSeq
	request.args = append([]string{}, request.args...)
	m.pluginCommandCancel = cancel
	runner := m.pluginCommand
	return m, func() tea.Msg {
		return pluginCommandResultMsg{
			request: request,
			result:  runner(ctx, request.args),
		}
	}
}

func (m *model) cancelPluginCommand() {
	if m.pluginCommandCancel != nil {
		m.pluginCommandCancel()
		m.pluginCommandCancel = nil
		m.pluginCommandSeq++
	}
}

func (m model) applyPluginCommandResultMessage(msg pluginCommandResultMsg) model {
	if msg.request.id != 0 && msg.request.id != m.pluginCommandSeq {
		return m
	}
	m.pluginCommandCancel = nil
	if hasPluginSnapshot(msg.result.Snapshot) || !m.pluginSnapshotReady {
		m.pluginSnapshot = msg.result.Snapshot
		m.pluginSnapshotReady = true
	}
	switch msg.request.origin {
	case pluginCommandOriginManager:
		text := m.pluginCommandResultText(strings.Join(msg.request.args, " "), msg.result)
		m.pluginManager = &pluginManagerState{selected: msg.request.managerSelected, query: msg.request.managerQuery}
		if items := m.pluginManagerItems(); len(items) > 0 {
			m.pluginManager.selected = clampInt(m.pluginManager.selected, 0, len(items)-1)
		}
		if text != "" && strings.Join(msg.request.args, " ") != "list" {
			m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "plugins", text: text})
		}
	default:
		text := m.pluginCommandResultText(msg.request.raw, msg.result)
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "plugins", text: text})
	}
	return m
}

func (m model) pluginCommandResultText(args string, result PluginCommandResult) string {
	if result.ExitCode != 0 || strings.TrimSpace(result.Error) != "" {
		message := strings.TrimSpace(result.Error)
		if message == "" {
			message = strings.TrimSpace(result.Output)
		}
		if message == "" {
			message = "Plugin command failed"
		}
		return strings.Join([]string{
			"Plugin action failed",
			message,
			"",
			m.pluginsText(args),
		}, "\n")
	}
	output := strings.TrimSpace(result.Output)
	if output == "" {
		output = "zero plugins " + args
	}
	lines := []string{"Plugin action complete", output}
	if result.RestartRequired {
		lines = append(lines, "Restart Zero to apply plugin changes.")
	}
	lines = append(lines, "", m.pluginsText(args))
	return strings.Join(lines, "\n")
}

func hasPluginSnapshot(snapshot PluginSnapshot) bool {
	return len(snapshot.Plugins) > 0 ||
		len(snapshot.Diagnostics) > 0 ||
		len(snapshot.Installed) > 0 ||
		len(snapshot.Catalogs) > 0 ||
		len(snapshot.MarketplacePlugins) > 0 ||
		strings.TrimSpace(snapshot.TrustNotice) != "" ||
		strings.TrimSpace(snapshot.LoadError) != "" ||
		snapshot.ProjectPluginsShown
}

func pluginCommandStatus(snapshot pluginManagerSnapshot) commandStatus {
	if snapshot.LoadError != "" || len(snapshot.Diagnostics) > 0 {
		return commandStatusWarning
	}
	return commandStatusInfo
}
