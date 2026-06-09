package tui

import (
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/providercatalog"
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
	entries := registry.List(modelregistry.ListOptions{})
	if len(entries) == 0 {
		return nil
	}
	items := make([]pickerItem, 0, len(entries))
	selected := 0
	for i, entry := range entries {
		label := entry.DisplayName
		if label == "" {
			label = entry.ID
		}
		item := pickerItem{Label: label + "  " + entry.ID, Value: entry.ID}
		// Right meta + locality dot come straight from data the registry and
		// provider catalog already expose; rows without it just omit the meta.
		if window := entry.ContextLimits.ContextWindow; window > 0 {
			item.Meta = formatContextWindow(window)
		}
		if descriptor, ok := providercatalog.Get(string(entry.Provider)); ok {
			item.Remote = !descriptor.Local
			item.Local = descriptor.Local
			if len(descriptor.AuthEnvVars) > 0 {
				if item.Meta != "" {
					item.Meta += " · "
				}
				item.Meta += descriptor.AuthEnvVars[0]
			}
		}
		items = append(items, item)
		if entry.ID == m.modelName {
			selected = i
		}
	}
	return &commandPicker{kind: pickerModel, title: "select model", items: items, selected: selected}
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
