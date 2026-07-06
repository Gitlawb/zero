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
	// An openai-compatible CUSTOM endpoint has no catalog default to fall back
	// on, so a missing model is still a real issue — and the message must tell
	// the user how to fix it.
	path := writeValidateFixture(t, `{
		"activeProvider": "main",
		"providers": [
			{"name": "main", "provider_kind": "openai-compatible", "baseURL": "https://gateway.example/v1"}
		]
	}`)

	_, issues := ValidateFile(path)
	if len(issues) == 0 {
		t.Fatalf("expected requires-model issue, got none")
	}
	if !strings.Contains(issues[0].Message, "requires model") {
		t.Fatalf("expected requires-model issue, got %#v", issues)
	}
	if !strings.Contains(issues[0].Message, "config.json") {
		t.Fatalf("requires-model issue should carry an actionable hint, got %#v", issues)
	}
}

func TestValidateFileDefaultsOfficialKindModels(t *testing.T) {
	// Official-API kinds (anthropic/google) fall back to their catalog default
	// model, so a hand-written model-less profile validates clean instead of
	// bricking zero config / bare zero setup — the only commands that could
	// have fixed it (the reported google case).
	path := writeValidateFixture(t, `{
		"activeProvider": "google",
		"providers": [
			{"name": "google", "provider_kind": "google", "apiKey": "AIza-x"},
			{"name": "anthropic", "provider_kind": "anthropic", "apiKey": "sk-ant-x"}
		]
	}`)

	_, issues := ValidateFile(path)
	for _, issue := range issues {
		if strings.Contains(issue.Message, "requires model") {
			t.Fatalf("official-kind profiles must default their model, got issue %#v", issue)
		}
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

func TestValidateFileRejectsFavoriteModelsOutsideProviderInventory(t *testing.T) {
	path := writeValidateFixture(t, `{
		"activeProvider": "openai",
		"providers": [
			{
				"name": "openai",
				"provider_kind": "openai",
				"model": "gpt-4.1",
				"models": [{"id": "gpt-4.1"}]
			},
			{
				"name": "local",
				"provider_kind": "openai-compatible",
				"baseURL": "http://localhost:11434/v1",
				"model": "qwen3"
			},
			{
				"name": "openrouter",
				"provider_kind": "openai-compatible",
				"baseURL": "https://openrouter.ai/api/v1",
				"model": "anthropic/claude-sonnet-4.5",
				"models": [
					{"id": "anthropic/claude-sonnet-4.5"},
					{"id": "meta-llama/llama-3.1/405b-instruct"}
				]
			}
		],
		"preferences": {
			"favoriteModels": [
				"openai/gpt-4.1",
				"openai/not-served",
				"local/not-discovered-yet",
				"openrouter/anthropic/claude-sonnet-4.5",
				"openrouter/meta-llama/llama-3.1/405b-instruct",
				"missing/model",
				"bare-model"
			]
		}
	}`)

	_, issues := ValidateFile(path)
	var favoriteIssues []Issue
	for _, issue := range issues {
		if issue.FieldPath == "preferences.favoriteModels" {
			favoriteIssues = append(favoriteIssues, issue)
		}
	}
	if len(favoriteIssues) != 3 {
		t.Fatalf("favorite issues = %#v, want not-served, missing/model, and bare-model", favoriteIssues)
	}
	for _, want := range []string{"openai/not-served", "missing/model", "bare-model"} {
		found := false
		for _, issue := range favoriteIssues {
			if strings.Contains(issue.Message, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("favorite issues = %#v, missing %q", favoriteIssues, want)
		}
	}
}

func TestValidateFileRejectsProviderModelOutsideDiscoveredModels(t *testing.T) {
	path := writeValidateFixture(t, `{
		"activeProvider": "openai",
		"providers": [
			{
				"name": "openai",
				"provider_kind": "openai",
				"model": "gpt-missing",
				"models": [{"id": "gpt-4.1"}]
			},
			{
				"name": "local",
				"provider_kind": "openai-compatible",
				"baseURL": "http://localhost:11434/v1",
				"model": "not-discovered-yet"
			}
		]
	}`)

	_, issues := ValidateFile(path)
	found := false
	for _, issue := range issues {
		if issue.FieldPath == "providers.openai.model" && strings.Contains(issue.Message, "gpt-missing") {
			found = true
		}
		if strings.Contains(issue.Message, "not-discovered-yet") {
			t.Fatalf("provider without discovered models should not be rejected, got %#v", issues)
		}
	}
	if !found {
		t.Fatalf("issues = %#v, want stale provider model issue", issues)
	}
}
