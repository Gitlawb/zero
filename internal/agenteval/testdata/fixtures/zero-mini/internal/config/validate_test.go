package config

import "testing"

func TestValidateRequiresDefaultProvider(t *testing.T) {
	err := Validate(Config{Providers: map[string]string{"local": "fixture"}})
	if err == nil {
		t.Fatal("Validate returned nil, want missing default provider error")
	}
}
