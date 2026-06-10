package tui

import (
	"context"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/providermodelcatalog"
	"github.com/Gitlawb/zero/internal/providermodeldiscovery"
)

// pickerKind identifies which command a picker selection feeds back into.
type pickerKind int

const (
	pickerModel pickerKind = iota
	pickerEffort
	pickerMode
)

// pickerItem is one selectable row: Label is shown, Value is passed to the
// underlying command handler when chosen. Meta is the optional right-aligned
// readout (ctx window · key env); the dot flags mark provider locality for
// model rows (accent = remote, blue = local).
type pickerItem struct {
	Group    string
	Label    string
	Value    string
	Meta     string
	Remote   bool
	Local    bool
	Favorite bool
}

// commandPicker is a generic single-select overlay reused by /model, /effort,
// and /mode (invoked with no argument). It owns only list state; the chosen
// value is applied through the existing command handlers.
type commandPicker struct {
	kind     pickerKind
	title    string
	items    []pickerItem
	allItems []pickerItem
	query    string
	selected int
}

func (p *commandPicker) move(delta int) {
	n := len(p.items)
	if n == 0 {
		return
	}
	p.selected = ((p.selected+delta)%n + n) % n
}

func (p *commandPicker) current() (pickerItem, bool) {
	if p.selected < 0 || p.selected >= len(p.items) {
		return pickerItem{}, false
	}
	return p.items[p.selected], true
}

func (p *commandPicker) appendQuery(runes []rune) {
	for _, r := range runes {
		if r < 32 {
			continue
		}
		p.query += string(r)
	}
	p.applyQuery()
}

func (p *commandPicker) deleteQueryRune() {
	if p.query == "" {
		return
	}
	runes := []rune(p.query)
	p.query = string(runes[:len(runes)-1])
	p.applyQuery()
}

func (p *commandPicker) clearQuery() {
	p.query = ""
	p.applyQuery()
}

func (p *commandPicker) applyQuery() {
	source := p.allItems
	if len(source) == 0 {
		source = p.items
	}
	query := strings.ToLower(strings.TrimSpace(p.query))
	if query == "" {
		p.items = append([]pickerItem{}, source...)
		p.selected = clampInt(p.selected, 0, maxInt(0, len(p.items)-1))
		return
	}
	filtered := make([]pickerItem, 0, len(source))
	for _, item := range source {
		if strings.Contains(strings.ToLower(strings.Join([]string{item.Group, item.Label, item.Value, item.Meta}, " ")), query) {
			filtered = append(filtered, item)
		}
	}
	p.items = filtered
	p.selected = 0
}

// newModelPicker lists active (non-deprecated) models, preselecting the active
// one. Returns nil when the catalog is unavailable so the caller falls back to
// the plain status text.
func (m model) newModelPicker() *commandPicker {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return nil
	}
	activeModel := strings.TrimSpace(m.modelName)
	recent := []pickerItem{}
	if activeModel != "" {
		recent = append(recent, m.modelPickerRecentItem(registry, activeModel))
	}

	catalog := []pickerItem{}
	if provider, ok := m.activeProviderDescriptor(); ok {
		catalog = append(catalog, m.providerCatalogModelPickerItems(provider, activeModel)...)
	} else {
		for _, entry := range registry.List(modelregistry.ListOptions{}) {
			if entry.ID == activeModel {
				continue
			}
			catalog = append(catalog, registryModelPickerItem(entry, "Catalog"))
		}
	}
	items := m.assembleModelPickerItems(recent, catalog)
	if len(items) == 0 {
		return nil
	}
	return &commandPicker{kind: pickerModel, title: "select model", items: items, allItems: append([]pickerItem{}, items...), selected: 0}
}

func (m model) assembleModelPickerItems(recent []pickerItem, catalog []pickerItem) []pickerItem {
	result := []pickerItem{}
	seen := map[string]bool{}
	all := append(append([]pickerItem{}, recent...), catalog...)
	for _, item := range all {
		if item.Value == "" || !m.favoriteModels[item.Value] || seen[item.Value] {
			continue
		}
		item.Group = "Favorites"
		item.Favorite = true
		result = append(result, item)
		seen[item.Value] = true
	}
	for _, item := range recent {
		if item.Value == "" || seen[item.Value] {
			continue
		}
		item.Group = "Recent"
		item.Favorite = m.favoriteModels[item.Value]
		result = append(result, item)
		seen[item.Value] = true
	}
	for _, item := range catalog {
		if item.Value == "" || seen[item.Value] {
			continue
		}
		item.Favorite = m.favoriteModels[item.Value]
		result = append(result, item)
		seen[item.Value] = true
	}
	return result
}

func (m model) modelPickerRecentItem(registry modelregistry.Registry, modelID string) pickerItem {
	if entry, ok := registry.Resolve(modelID); ok {
		item := registryModelPickerItem(entry, "Recent")
		item.Value = modelID
		return item
	}
	if provider, ok := m.activeProviderDescriptor(); ok {
		for _, model := range providermodelcatalog.Models(provider) {
			if model.ID == modelID {
				item := providerModelPickerItem(provider, model, "Recent")
				item.Value = modelID
				return item
			}
		}
		return providerModelPickerItem(provider, providermodelcatalog.Model{ID: modelID}, "Recent")
	}
	return pickerItem{Group: "Recent", Label: modelPickerDisplayName(modelID, ""), Value: modelID}
}

func (m model) providerCatalogModelPickerItems(provider providercatalog.Descriptor, activeModel string) []pickerItem {
	if m.modelPickerLiveProviderID == provider.ID && len(m.modelPickerLiveModels) > 0 {
		return m.liveProviderModelPickerItems(provider, activeModel)
	}
	models := providermodelcatalog.Models(provider)
	items := make([]pickerItem, 0, len(models))
	group := provider.Name + " catalog"
	for _, model := range models {
		if strings.TrimSpace(model.ID) == "" || model.ID == activeModel {
			continue
		}
		items = append(items, providerModelPickerItem(provider, model, group))
	}
	return items
}

func (m model) liveProviderModelPickerItems(provider providercatalog.Descriptor, activeModel string) []pickerItem {
	items := make([]pickerItem, 0, len(m.modelPickerLiveModels))
	group := provider.Name + " catalog"
	for _, model := range m.modelPickerLiveModels {
		if strings.TrimSpace(model.ID) == "" || model.ID == activeModel {
			continue
		}
		items = append(items, discoveredModelPickerItem(provider, model, group))
	}
	return items
}

func registryModelPickerItem(entry modelregistry.ModelEntry, group string) pickerItem {
	item := pickerItem{
		Group: group,
		Label: firstProviderDisplayValue(entry.DisplayName, entry.ID),
		Value: entry.ID,
	}
	if window := entry.ContextLimits.ContextWindow; window > 0 {
		item.Meta = formatContextWindow(window)
	}
	if descriptor, ok := providercatalog.Get(string(entry.Provider)); ok {
		applyProviderPickerMeta(&item, descriptor)
	}
	return item
}

func providerModelPickerItem(provider providercatalog.Descriptor, model providermodelcatalog.Model, group string) pickerItem {
	item := pickerItem{
		Group: group,
		Label: modelPickerDisplayName(model.ID, model.Description),
		Value: model.ID,
	}
	if ctx := formatContextWindow(model.ContextWindow); ctx != "" {
		item.Meta = ctx
	}
	applyProviderPickerMeta(&item, provider)
	return item
}

func discoveredModelPickerItem(provider providercatalog.Descriptor, model providermodeldiscovery.Model, group string) pickerItem {
	item := pickerItem{
		Group: group,
		Label: modelPickerDisplayName(model.ID, model.Description),
		Value: model.ID,
	}
	if ctx := formatContextWindow(model.ContextWindow); ctx != "" {
		item.Meta = ctx
	}
	applyProviderPickerMeta(&item, provider)
	return item
}

func applyProviderPickerMeta(item *pickerItem, provider providercatalog.Descriptor) {
	item.Remote = !provider.Local
	item.Local = provider.Local
	if len(provider.AuthEnvVars) > 0 {
		if item.Meta != "" {
			item.Meta += " · "
		}
		item.Meta += provider.AuthEnvVars[0]
	}
}

func modelPickerDisplayName(id string, description string) string {
	if description = strings.TrimSpace(description); description != "" && !providerWizardGenericModelDescription(description) {
		return description
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return "model"
	}
	name := id
	if slash := strings.LastIndex(name, "/"); slash >= 0 && slash < len(name)-1 {
		name = name[slash+1:]
	}
	name = strings.NewReplacer("-", " ", "_", " ", ":", " ").Replace(name)
	words := strings.Fields(name)
	for index, word := range words {
		words[index] = modelPickerTitleWord(word)
	}
	return strings.Join(words, " ")
}

func modelPickerTitleWord(word string) string {
	if word == "" {
		return ""
	}
	lower := strings.ToLower(word)
	switch lower {
	case "api", "gpt", "glm", "vl":
		return strings.ToUpper(lower)
	default:
		if strings.HasPrefix(lower, "gpt") || strings.HasPrefix(lower, "glm") {
			return strings.ToUpper(lower[:3]) + word[3:]
		}
		return strings.ToUpper(word[:1]) + word[1:]
	}
}

func (m model) activeProviderDescriptor() (providercatalog.Descriptor, bool) {
	if descriptor, ok := providercatalog.Get(m.providerProfile.CatalogID); ok && !genericProviderCatalogID(descriptor.ID) {
		return descriptor, true
	}
	if descriptor, ok := providerDescriptorByBaseURL(m.providerProfile.BaseURL); ok {
		return descriptor, true
	}
	for _, candidate := range []string{
		m.providerProfile.Name,
		m.providerName,
		m.providerProfile.Provider,
		string(m.providerProfile.ProviderKind),
	} {
		if descriptor, ok := providercatalog.Get(candidate); ok {
			return descriptor, true
		}
	}
	return providercatalog.Descriptor{}, false
}

func providerDescriptorByBaseURL(baseURL string) (providercatalog.Descriptor, bool) {
	normalized := normalizeProviderBaseURL(baseURL)
	if normalized == "" {
		return providercatalog.Descriptor{}, false
	}
	for _, descriptor := range providercatalog.All() {
		if genericProviderCatalogID(descriptor.ID) {
			continue
		}
		if normalizeProviderBaseURL(descriptor.DefaultBaseURL) == normalized {
			return descriptor, true
		}
	}
	return providercatalog.Descriptor{}, false
}

func normalizeProviderBaseURL(baseURL string) string {
	return strings.TrimRight(strings.ToLower(strings.TrimSpace(baseURL)), "/")
}

func genericProviderCatalogID(id string) bool {
	return strings.HasPrefix(strings.TrimSpace(id), "custom-")
}

type modelPickerModelsDiscoveredMsg struct {
	providerID string
	models     []providermodeldiscovery.Model
	err        error
}

func (m model) modelPickerDiscoveryCmd() tea.Cmd {
	provider, ok := m.activeProviderDescriptor()
	if !ok {
		return nil
	}
	profile := m.modelPickerDiscoveryProfile(provider)
	discover := m.discoverProviderModels
	if discover == nil {
		discover = func(ctx context.Context, profile config.ProviderProfile) ([]providermodeldiscovery.Model, error) {
			return providermodeldiscovery.DiscoverCatalog(ctx, provider, profile, providermodeldiscovery.Options{})
		}
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.ctx, 8*time.Second)
		defer cancel()
		models, err := discover(ctx, profile)
		return modelPickerModelsDiscoveredMsg{providerID: provider.ID, models: models, err: err}
	}
}

func (m model) modelPickerDiscoveryProfile(provider providercatalog.Descriptor) config.ProviderProfile {
	profile := m.normalizeProfileForProvider(provider)
	if strings.TrimSpace(profile.Model) == "" {
		profile.Model = provider.DefaultModel
	}
	return profile
}

func (m model) normalizeProfileForProvider(provider providercatalog.Descriptor) config.ProviderProfile {
	profile := m.providerProfile
	normalizeIdentity := profileMatchesProviderBaseURL(profile, provider) ||
		genericProviderCatalogID(profile.Name) ||
		genericProviderCatalogID(profile.CatalogID) ||
		strings.TrimSpace(profile.Name) == "" ||
		strings.TrimSpace(profile.CatalogID) == ""
	if normalizeIdentity {
		profile.Name = provider.ID
		profile.CatalogID = provider.ID
	}
	if strings.TrimSpace(profile.BaseURL) == "" {
		profile.BaseURL = provider.DefaultBaseURL
	}
	if strings.TrimSpace(string(profile.ProviderKind)) == "" {
		profile.ProviderKind = providerWizardProviderKind(provider)
	}
	if strings.TrimSpace(profile.APIFormat) == "" {
		profile.APIFormat = providerWizardAPIFormat(provider)
	}
	if len(provider.AuthEnvVars) > 0 && (strings.TrimSpace(profile.APIKeyEnv) == "" || normalizeIdentity) {
		profile.APIKeyEnv = provider.AuthEnvVars[0]
	}
	if strings.TrimSpace(profile.APIKey) == "" && strings.TrimSpace(profile.APIKeyEnv) != "" {
		profile.APIKey = strings.TrimSpace(os.Getenv(profile.APIKeyEnv))
	}
	return profile
}

func profileMatchesProviderBaseURL(profile config.ProviderProfile, provider providercatalog.Descriptor) bool {
	baseURL := normalizeProviderBaseURL(profile.BaseURL)
	return baseURL != "" && baseURL == normalizeProviderBaseURL(provider.DefaultBaseURL)
}

func (m model) applyModelPickerModelsDiscovered(msg modelPickerModelsDiscoveredMsg) model {
	provider, ok := m.activeProviderDescriptor()
	if !ok || provider.ID != msg.providerID || msg.err != nil || len(msg.models) == 0 {
		return m
	}
	m.modelPickerLiveProviderID = msg.providerID
	m.modelPickerLiveModels = append([]providermodeldiscovery.Model{}, msg.models...)
	if m.picker != nil && m.picker.kind == pickerModel {
		selectedValue := ""
		query := m.picker.query
		if item, ok := m.picker.current(); ok {
			selectedValue = item.Value
		}
		m.picker = m.newModelPicker()
		m.picker.query = query
		m.picker.applyQuery()
		m.selectPickerValue(selectedValue)
	}
	return m
}

func (m model) toggleModelFavorite() model {
	if m.picker == nil || m.picker.kind != pickerModel {
		return m
	}
	item, ok := m.picker.current()
	if !ok || strings.TrimSpace(item.Value) == "" {
		return m
	}
	if m.favoriteModels == nil {
		m.favoriteModels = map[string]bool{}
	}
	if m.favoriteModels[item.Value] {
		delete(m.favoriteModels, item.Value)
	} else {
		m.favoriteModels[item.Value] = true
	}
	selectedValue := item.Value
	query := m.picker.query
	m.picker = m.newModelPicker()
	m.picker.query = query
	m.picker.applyQuery()
	m.selectPickerValue(selectedValue)
	return m
}

func (m *model) selectPickerValue(value string) {
	if m.picker == nil || value == "" {
		return
	}
	for index, item := range m.picker.items {
		if item.Value == value {
			m.picker.selected = index
			return
		}
	}
}

// newEffortPicker lists the reasoning efforts the active model supports plus an
// "auto" option, preselecting the current preference. Returns nil when the model
// exposes no effort controls so the caller falls back to status text.
func (m model) newEffortPicker() *commandPicker {
	efforts := m.availableReasoningEfforts()
	if len(efforts) == 0 {
		return nil
	}
	items := []pickerItem{{Label: "auto", Value: "auto"}}
	selected := 0
	for _, effort := range efforts {
		items = append(items, pickerItem{Label: string(effort), Value: string(effort)})
		if m.reasoningEffort != "" && effort == m.reasoningEffort {
			selected = len(items) - 1
		}
	}
	return &commandPicker{kind: pickerEffort, title: "select reasoning effort", items: items, selected: selected}
}

// newModePicker lists the agent modes, preselecting none (modes don't carry a
// single "active" identity).
func (m model) newModePicker() *commandPicker {
	modes := modelregistry.Modes()
	if len(modes) == 0 {
		return nil
	}
	items := make([]pickerItem, 0, len(modes))
	for _, mode := range modes {
		label := mode.Name
		if mode.Description != "" {
			label += " — " + mode.Description
		}
		items = append(items, pickerItem{Label: label, Value: mode.Name})
	}
	return &commandPicker{kind: pickerMode, title: "select mode", items: items, selected: 0}
}
