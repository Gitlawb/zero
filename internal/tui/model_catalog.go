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
	models := registry.List(modelregistry.ListOptions{})
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Provider == models[j].Provider {
			return models[i].ID < models[j].ID
		}
		return models[i].Provider < models[j].Provider
	})
	modelLines := []string{}
	for _, model := range models {
		marker := " "
		if activeID != "" && model.ID == activeID {
			marker = "*"
		}
		modelLines = append(modelLines, fmt.Sprintf("%s %s (%s) - %s", marker, model.ID, model.Provider, model.DisplayName))
	}
	return renderCommandOutput(commandOutput{
		Title:  "Models",
		Status: commandStatusOK,
		Sections: []commandSection{
			{
				Title: "Active",
				Lines: []string{
					"Active model: " + displayValue(m.modelName, "none"),
					"provider: " + displayValue(m.providerName, "none"),
					"effort: " + m.effortDisplay(),
				},
			},
			{
				Title: "Available models:",
				Lines: modelLines,
			},
		},
		Hints: []string{"use /model <id> to switch this TUI session"},
	})
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
