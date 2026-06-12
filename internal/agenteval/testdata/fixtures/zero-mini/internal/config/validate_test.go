package config

import "testing"

func TestValidateRequiresDefaultProvider(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "empty default",
			cfg:  Config{Providers: map[string]string{"local": "fixture"}},
		},
		{
			name: "unknown default",
			cfg: Config{
				DefaultProvider: "missing",
				Providers:       map[string]string{"local": "fixture"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(test.cfg); err == nil {
				t.Fatal("Validate returned nil, want provider validation error")
			}
		})
	}
}
