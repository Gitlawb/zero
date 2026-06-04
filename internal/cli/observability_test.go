package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/sessions"
)

func TestRunDoctorFormatsRedactedProviderDiagnostics(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cwd := t.TempDir()

	exitCode := runWithDeps([]string{"doctor"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return cwd, nil
		},
		resolveConfig: func(workspaceRoot string, overrides config.Overrides) (config.ResolvedConfig, error) {
			if workspaceRoot != cwd {
				t.Fatalf("workspaceRoot = %q, want %q", workspaceRoot, cwd)
			}
			return config.ResolvedConfig{
				Provider: config.ProviderProfile{
					Name:         "openai",
					ProviderKind: config.ProviderKindOpenAI,
					BaseURL:      config.OpenAIBaseURL,
					APIKey:       "sk-proj-secret1234567890",
					Model:        "gpt-4.1",
				},
			}, nil
		},
		now: fixedCLITime("2026-06-04T16:00:00Z"),
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"Zero doctor report", "Overall: pass", "[pass] provider.config", "[warn] provider.connectivity"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected doctor output to contain %q, got %q", want, output)
		}
	}
	if strings.Contains(output, "sk-proj-secret") {
		t.Fatalf("doctor output leaked secret: %q", output)
	}
}

func TestRunDoctorJSONReturnsFailureForMissingProvider(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"doctor", "--json"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
		resolveConfig: func(string, config.Overrides) (config.ResolvedConfig, error) {
			return config.ResolvedConfig{}, nil
		},
		now: fixedCLITime("2026-06-04T16:30:00Z"),
	})

	if exitCode != exitProvider {
		t.Fatalf("expected provider exit %d, got %d", exitProvider, exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	var report struct {
		OK     bool `json:"ok"`
		Checks []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("doctor JSON did not decode: %v\n%s", err, stdout.String())
	}
	if report.OK {
		t.Fatalf("expected report ok=false, got %#v", report)
	}
}

func TestRunSearchFindsSessionEventsFromInjectedStore(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir(), Now: fixedCLITime("2026-06-04T17:00:00Z")})
	session, err := store.Create(sessions.CreateInput{SessionID: "cli_search", Title: "CLI Search", Cwd: "/repo", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]string{"content": "needle with token=sk-secret1234567890"}}); err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"search", "needle", "--limit", "3"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store {
			return store
		},
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, "Found 1 local session event") || !strings.Contains(output, "cli_search") {
		t.Fatalf("unexpected search output: %q", output)
	}
	if strings.Contains(output, "sk-secret") {
		t.Fatalf("search output leaked secret: %q", output)
	}
}

func TestRunSearchJSONAndValidation(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir(), Now: fixedCLITime("2026-06-04T17:30:00Z")})

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"search", "--json", "missing"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store {
			return store
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	var result struct {
		Query     string `json:"query"`
		TotalHits int    `json:"totalHits"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("search JSON did not decode: %v\n%s", err, stdout.String())
	}
	if result.Query != "missing" || result.TotalHits != 0 {
		t.Fatalf("unexpected search result: %#v", result)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"search"}, &stdout, &stderr, appDeps{})
	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if !strings.Contains(stderr.String(), "search query required") {
		t.Fatalf("expected search validation error, got %q", stderr.String())
	}
}

func TestRunSearchJSONRedactsQueryAndSessionMetadata(t *testing.T) {
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir(), Now: fixedCLITime("2026-06-04T18:00:00Z")})
	querySecret := "sk-proj-querysecret1234567890"
	metadataSecret := "sk-proj-metadatasecret1234567890"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithDeps([]string{"search", "--json", querySecret}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store {
			return store
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), querySecret) {
		t.Fatalf("search JSON leaked raw query secret: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[REDACTED]") {
		t.Fatalf("expected redacted query marker in JSON output: %q", stdout.String())
	}

	session, err := store.Create(sessions.CreateInput{
		SessionID: "json_metadata_secret",
		Title:     "Title " + metadataSecret,
		Cwd:       "/repo/" + metadataSecret,
		ModelID:   "model-" + metadataSecret,
		Provider:  "provider-token=" + metadataSecret,
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{Type: sessions.EventMessage, Payload: map[string]string{"content": "metadata needle"}}); err != nil {
		t.Fatalf("AppendEvent returned error: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = runWithDeps([]string{"search", "--json", "metadata", "needle"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store {
			return store
		},
	})
	if exitCode != exitSuccess {
		t.Fatalf("expected exit code %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), metadataSecret) {
		t.Fatalf("search JSON leaked session metadata secret: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[REDACTED]") {
		t.Fatalf("expected redacted metadata marker in JSON output: %q", stdout.String())
	}
}

func fixedCLITime(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
}
