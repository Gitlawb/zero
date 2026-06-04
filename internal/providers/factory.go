package providers

import (
	"fmt"
	"net/http"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/providers/openai"
	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// Options configures provider construction.
type Options struct {
	UserAgent  string
	HTTPClient *http.Client
}

// New creates a runtime provider for a resolved provider profile.
func New(profile config.ProviderProfile, options Options) (zeroruntime.Provider, error) {
	switch profile.ProviderKind {
	case config.ProviderKindOpenAI, config.ProviderKindOpenAICompatible:
		return openai.New(openai.Options{
			APIKey:     profile.APIKey,
			BaseURL:    profile.BaseURL,
			Model:      profile.Model,
			HTTPClient: options.HTTPClient,
			UserAgent:  options.UserAgent,
		})
	default:
		return nil, fmt.Errorf("unsupported provider kind %q", profile.ProviderKind)
	}
}
