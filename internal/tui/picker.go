package tui

import (
	"strings"

	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/providermodelcatalog"
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
	Group  string
	Label  string
	Value  string
	Meta   string
	Remote bool
	Local  bool
}

// commandPicker is a generic single-select overlay reused by /model, /effort,
// and /mode (invoked with no argument). It owns only list state; the chosen
// value is applied through the existing command handlers.
type commandPicker struct {
	kind     pickerKind
	title    string
	items    []pickerItem
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

// newModelPicker lists active (non-deprecated) models, preselecting the active
// one. Returns nil when the catalog is unavailable so the caller falls back to
// the plain status text.
func (m model) newModelPicker() *commandPicker {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return nil
	}
	activeModel := strings.TrimSpace(m.modelName)
	items := []pickerItem{}
	if activeModel != "" {
		items = append(items, m.modelPickerRecentItem(registry, activeModel))
	}

	if provider, ok := m.activeProviderDescriptor(); ok {
		items = append(items, m.providerCatalogModelPickerItems(provider, activeModel)...)
	} else {
		for _, entry := range registry.List(modelregistry.ListOptions{}) {
			if entry.ID == activeModel {
				continue
			}
			items = append(items, registryModelPickerItem(entry, "Catalog"))
		}
	}
	if len(items) == 0 {
		return nil
	}
	return &commandPicker{kind: pickerModel, title: "select model", items: items, selected: 0}
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
	for _, candidate := range []string{
		m.providerProfile.CatalogID,
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
