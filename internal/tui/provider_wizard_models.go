package tui

import (
	"github.com/Gitlawb/zero/internal/providercatalog"
	"github.com/Gitlawb/zero/internal/providermodelcatalog"
)

func providerWizardModelOptions(provider providercatalog.Descriptor) []providerWizardModel {
	catalogModels := providermodelcatalog.Models(provider)
	models := make([]providerWizardModel, 0, len(catalogModels))
	for _, model := range catalogModels {
		meta := ""
		if ctx := formatContextWindow(model.ContextWindow); ctx != "" {
			meta = ctx + " ctx"
		}
		models = append(models, providerWizardModel{
			ID:          model.ID,
			Description: model.Description,
			Meta:        meta,
		})
	}
	return models
}
