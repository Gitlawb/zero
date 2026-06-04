package tui

import (
	"fmt"
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
	for _, model := range registry.List(modelregistry.ListOptions{}) {
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
