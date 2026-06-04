package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/modelregistry"
)

func (m model) modelListText() string {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return "Models\nFailed to load model catalog: " + err.Error()
	}

	activeID := activeModelID(registry, m.modelName)
	lines := []string{
		"Models",
		"Active model: " + displayValue(m.modelName, "none"),
		"provider: " + displayValue(m.providerName, "none"),
		"Available models:",
	}
	models := registry.List(modelregistry.ListOptions{})
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Provider == models[j].Provider {
			return models[i].ID < models[j].ID
		}
		return models[i].Provider < models[j].Provider
	})
	for _, model := range models {
		marker := " "
		if activeID != "" && model.ID == activeID {
			marker = "*"
		}
		lines = append(lines, fmt.Sprintf("%s %s (%s) - %s", marker, model.ID, model.Provider, model.DisplayName))
	}
	return strings.Join(lines, "\n")
}

func activeModelID(registry modelregistry.Registry, modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return ""
	}
	if model, ok := registry.Get(modelName); ok {
		return model.ID
	}
	return strings.ToLower(modelName)
}
