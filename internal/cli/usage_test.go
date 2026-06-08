package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/zerogit"
)

func seedUsageStore(t *testing.T) *sessions.Store {
	t.Helper()
	store := sessions.NewStore(sessions.StoreOptions{RootDir: t.TempDir(), Now: fixedCLITime("2026-06-01T09:00:00Z")})
	session, err := store.Create(sessions.CreateInput{SessionID: "usage_s1", Title: "Usage", Cwd: "/repo", ModelID: "gpt-4.1", Provider: "openai"})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	for _, payload := range []map[string]any{
		{"promptTokens": 1000, "completionTokens": 200, "totalTokens": 1200},
		{"promptTokens": 500, "completionTokens": 100, "totalTokens": 600},
	} {
		if _, err := store.AppendEvent(session.SessionID, sessions.AppendEventInput{Type: sessions.EventUsage, Payload: payload}); err != nil {
			t.Fatalf("AppendEvent returned error: %v", err)
		}
	}
	return store
}

func stubInspectChanges(stat string) func(context.Context, zerogit.InspectOptions) (zerogit.ChangeSummary, error) {
	return func(context.Context, zerogit.InspectOptions) (zerogit.ChangeSummary, error) {
		return zerogit.ChangeSummary{Root: "/repo", DiffStat: stat}, nil
	}
}

func TestRunUsageTextReport(t *testing.T) {
	store := seedUsageStore(t)
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"usage", "report"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store { return store },
		inspectChanges:  stubInspectChanges(" 1 file changed, 100 insertions(+), 30 deletions(-)"),
	})
	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"2026-06-01", "1,800", "estimate", "net LOC", "+100", "-30"} {
		if !strings.Contains(output, want) {
			t.Fatalf("usage report missing %q in:\n%s", want, output)
		}
	}
}

func TestRunUsageDefaultsToReport(t *testing.T) {
	store := seedUsageStore(t)
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"usage"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store { return store },
		inspectChanges:  stubInspectChanges(""),
	})
	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "2026-06-01") {
		t.Fatalf("expected default report output, got %q", stdout.String())
	}
}

func TestRunUsageJSONReport(t *testing.T) {
	store := seedUsageStore(t)
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"usage", "report", "--json"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store { return store },
		inspectChanges:  stubInspectChanges(" 1 file changed, 100 insertions(+), 30 deletions(-)"),
	})
	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	var report struct {
		NetLOC int `json:"netLOC"`
		Total  struct {
			Requests    int `json:"requests"`
			TotalTokens int `json:"totalTokens"`
		} `json:"total"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("usage JSON did not decode: %v\n%s", err, stdout.String())
	}
	if report.NetLOC != 70 || report.Total.Requests != 2 || report.Total.TotalTokens != 1800 {
		t.Fatalf("unexpected usage JSON: %+v", report)
	}
}

func TestRunUsageSinceFilter(t *testing.T) {
	store := seedUsageStore(t)
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"usage", "report", "--since", "2026-07-01"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store { return store },
		inspectChanges:  stubInspectChanges(""),
	})
	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	if strings.Contains(stdout.String(), "2026-06-01") {
		t.Fatalf("expected --since to filter out June events, got %q", stdout.String())
	}
}

func TestRunUsageSessionFilter(t *testing.T) {
	store := seedUsageStore(t)
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"usage", "report", "--json", "--session", "missing_session"}, &stdout, &stderr, appDeps{
		newSessionStore: func() *sessions.Store { return store },
		inspectChanges:  stubInspectChanges(""),
	})
	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	var report struct {
		Total struct {
			Requests int `json:"requests"`
		} `json:"total"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("usage JSON did not decode: %v\n%s", err, stdout.String())
	}
	if report.Total.Requests != 0 {
		t.Fatalf("expected unknown session to yield 0 requests, got %d", report.Total.Requests)
	}
}

func TestRunUsageHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"usage", "--help"}, &stdout, &stderr, appDeps{})
	if exitCode != exitSuccess {
		t.Fatalf("expected exit %d, got %d: %s", exitSuccess, exitCode, stderr.String())
	}
	for _, want := range []string{"zero usage report", "--json", "--days", "--since", "--session"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("usage help missing %q in:\n%s", want, stdout.String())
		}
	}
}

func TestRunUsageUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := runWithDeps([]string{"usage", "report", "--bogus"}, &stdout, &stderr, appDeps{})
	if exitCode != exitUsage {
		t.Fatalf("expected usage exit %d, got %d", exitUsage, exitCode)
	}
	if !strings.Contains(stderr.String(), "unknown usage flag") {
		t.Fatalf("expected unknown-flag error, got %q", stderr.String())
	}
}
