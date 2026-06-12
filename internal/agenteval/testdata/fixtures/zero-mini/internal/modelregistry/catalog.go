package modelregistry

type Provider struct {
	Name    string
	Aliases []string
}

func DefaultProviders() map[string]Provider {
	return map[string]Provider{
		"openai": {Name: "OpenAI", Aliases: []string{"gpt-4.1"}},
		"local":  {Name: "Local", Aliases: []string{"fixture-small"}},
	}
}
