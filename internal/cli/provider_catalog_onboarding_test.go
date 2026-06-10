package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestProviderCatalogRuntimeProvidersShowOnboardingSetupHints(t *testing.T) {
	output := runProviderCatalogOnboarding(t)

	tests := []struct {
		id           string
		name         string
		transport    string
		defaultModel string
	}{
		{id: "openai", name: "OpenAI", transport: "openai", defaultModel: "gpt-4.1"},
		{id: "groq", name: "Groq", transport: "openai-compatible", defaultModel: "llama-3.3-70b-versatile"},
		{id: "ollama", name: "Ollama", transport: "openai-compatible", defaultModel: "llama3.1"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			block := providerCatalogOnboardingBlock(t, output, tt.id)
			for _, want := range []string{
				"id=" + tt.id,
				"name=" + tt.name,
				"transport=" + tt.transport,
				"defaultModel=" + tt.defaultModel,
				"setup: zero providers setup " + tt.id + " --set-active",
			} {
				if !strings.Contains(block, want) {
					t.Fatalf("expected %s catalog block to contain %q, got:\n%s", tt.id, want, block)
				}
			}
			if strings.Contains(block, "unsupported:") {
				t.Fatalf("runtime-supported provider %s should not show unsupported reason, got:\n%s", tt.id, block)
			}
		})
	}
}

func TestProviderCatalogLocalProviderOnboardingDoesNotRequireAuthEnv(t *testing.T) {
	output := runProviderCatalogOnboarding(t)
	block := providerCatalogOnboardingBlock(t, output, "ollama")

	if strings.Contains(block, "authEnvVars=") {
		t.Fatalf("local provider should not show an auth env var summary, got:\n%s", block)
	}
	if strings.Contains(block, "requiresAuth=true") {
		t.Fatalf("local provider should not require auth, got:\n%s", block)
	}
}

func TestProviderCatalogUnsupportedProvidersShowOnboardingReason(t *testing.T) {
	output := runProviderCatalogOnboarding(t)

	for _, id := range []string{"bedrock", "vertex"} {
		t.Run(id, func(t *testing.T) {
			block := providerCatalogOnboardingBlock(t, output, id)
			if !strings.Contains(block, "unsupported: native adapter not implemented yet") {
				t.Fatalf("expected %s catalog block to show unsupported reason, got:\n%s", id, block)
			}
			if strings.Contains(block, "setup: zero providers setup") {
				t.Fatalf("unsupported provider %s should not show setup hint, got:\n%s", id, block)
			}
		})
	}
}

func runProviderCatalogOnboarding(t *testing.T) string {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"providers", "catalog"}, &stdout, &stderr, providerCatalogDeps(t))
	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	return stdout.String()
}

func providerCatalogOnboardingBlock(t *testing.T, output string, id string) string {
	t.Helper()

	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	start := -1
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "id="+id) {
			start = index
			break
		}
	}
	if start == -1 {
		t.Fatalf("catalog block for provider %q not found in:\n%s", id, output)
	}
	end := len(lines)
	for index := start + 1; index < len(lines); index++ {
		if strings.HasPrefix(strings.TrimSpace(lines[index]), "id=") {
			end = index
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}
