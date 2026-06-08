package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeValidateFixture(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestValidateFileReturnsParseIssueForMalformedJSON(t *testing.T) {
	path := writeValidateFixture(t, `{"activeProvider": "openai",`)

	_, issues := ValidateFile(path)
	if len(issues) == 0 {
		t.Fatalf("expected parse issue, got none")
	}
	if !strings.Contains(issues[0].Message, "invalid config JSON") {
		t.Fatalf("expected parse issue message, got %#v", issues)
	}
}

func TestValidateFileSurfacesSemanticIssue(t *testing.T) {
	path := writeValidateFixture(t, `{
		"activeProvider": "main",
		"providers": [
			{"name": "main", "provider_kind": "openai", "baseURL": "https://example.test/v1", "model": "gpt-4.1"}
		]
	}`)

	cfg, issues := ValidateFile(path)
	if cfg.ActiveProvider != "main" {
		t.Fatalf("expected parsed config, got %#v", cfg)
	}
	if len(issues) == 0 {
		t.Fatalf("expected semantic issue for openai custom baseURL, got none")
	}
}

func TestValidateFileRedactsSecretInIssue(t *testing.T) {
	path := writeValidateFixture(t, `{
		"activeProvider": "main",
		"providers": [
			{"name": "main", "provider_kind": "openai", "baseURL": "https://example.test/v1", "apiKey": "sk-proj-secret1234567890", "model": "gpt-4.1"}
		]
	}`)

	_, issues := ValidateFile(path)
	if len(issues) == 0 {
		t.Fatalf("expected semantic issue, got none")
	}
	for _, issue := range issues {
		if strings.Contains(issue.Message, "sk-proj-secret") {
			t.Fatalf("issue leaked apiKey: %q", issue.Message)
		}
	}
}

func TestValidateFileMissingModelWarns(t *testing.T) {
	path := writeValidateFixture(t, `{
		"activeProvider": "main",
		"providers": [
			{"name": "main", "provider_kind": "anthropic"}
		]
	}`)

	_, issues := ValidateFile(path)
	if len(issues) == 0 {
		t.Fatalf("expected requires-model issue, got none")
	}
	if !strings.Contains(issues[0].Message, "requires model") {
		t.Fatalf("expected requires-model issue, got %#v", issues)
	}
}

func TestValidateFileValidConfigHasNoIssues(t *testing.T) {
	path := writeValidateFixture(t, `{
		"activeProvider": "main",
		"providers": [
			{"name": "main", "provider_kind": "openai", "model": "gpt-4.1"}
		]
	}`)

	_, issues := ValidateFile(path)
	if len(issues) != 0 {
		t.Fatalf("expected no issues for valid config, got %#v", issues)
	}
}
