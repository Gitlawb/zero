package zerocommands

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/sessions"
)

func TestConfigSnapshotRedactsProviderURLsAndResolvesAPIModels(t *testing.T) {
	resolved := config.ResolvedConfig{
		ActiveProvider: "work",
		MaxTurns:       9,
		Providers: []config.ProviderProfile{
			{
				Name:         "work",
				ProviderKind: config.ProviderKindOpenAICompatible,
				BaseURL:      "https://user:sk-secret@example.test/v1?token=sk-secret",
				APIKey:       "sk-secret",
				Model:        "gpt-4.1",
			},
			{
				Name:  "claude",
				Model: "sonnet-4.5",
			},
		},
	}

	snapshot := ConfigSnapshotFromResolved(resolved)

	if snapshot.Runtime != RuntimeGo || snapshot.ActiveProvider != "work" || snapshot.MaxTurns != 9 {
		t.Fatalf("unexpected config snapshot: %#v", snapshot)
	}
	if len(snapshot.Providers) != 2 {
		t.Fatalf("expected two providers, got %#v", snapshot.Providers)
	}
	active := snapshot.Providers[0]
	if !active.Active || active.Name != "work" || active.APIModel != "gpt-4.1" {
		t.Fatalf("unexpected active provider snapshot: %#v", active)
	}
	if strings.Contains(active.BaseURL, "user:") || strings.Contains(active.BaseURL, "sk-secret") {
		t.Fatalf("provider base URL was not redacted: %#v", active)
	}
	if !active.APIKeySet {
		t.Fatalf("expected APIKeySet=true for active provider: %#v", active)
	}
	if snapshot.Providers[1].APIModel != "claude-sonnet-4-5-20250929" || snapshot.Providers[1].ProviderKind != "anthropic" {
		t.Fatalf("expected model metadata resolution for Claude alias, got %#v", snapshot.Providers[1])
	}
}

func TestConfigSnapshotRedactsProviderWarnings(t *testing.T) {
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz"
	resolved := config.ResolvedConfig{
		Providers: []config.ProviderProfile{
			{
				Name:    "provider-" + secret,
				BaseURL: "https://user:" + secret + "@[invalid",
				APIKey:  secret,
			},
		},
	}

	snapshot := ConfigSnapshotFromResolved(resolved)

	if len(snapshot.Providers) != 1 {
		t.Fatalf("expected one provider, got %#v", snapshot.Providers)
	}
	provider := snapshot.Providers[0]
	if provider.Status != "warning" || provider.Message == "" {
		t.Fatalf("expected warning provider snapshot, got %#v", provider)
	}
	combined := provider.BaseURL + provider.Message
	if strings.Contains(combined, secret) || strings.Contains(combined, "user:") || strings.Contains(combined, "[invalid") {
		t.Fatalf("provider warning leaked raw secret or URL: %#v", provider)
	}
}

// TestConfigSnapshotRedactsOnMetadataResolverError covers the explicit CR request:
// ensure that even when provider metadata resolution hits an error/unavailable path,
// raw secrets never appear in the resulting snapshot (BaseURL or Message).
func TestConfigSnapshotRedactsOnMetadataResolverError(t *testing.T) {
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"
	// Bad URL pattern that can cause metadata resolution issues.
	resolved := config.ResolvedConfig{
		ActiveProvider: "broken",
		Providers: []config.ProviderProfile{
			{
				Name:    "broken",
				BaseURL: "https://user:" + secret + "@[invalid",
				APIKey:  secret,
				Model:   "gpt-4.1",
			},
		},
	}

	snapshot := ConfigSnapshotFromResolved(resolved)

	if len(snapshot.Providers) != 1 {
		t.Fatalf("expected one provider, got %#v", snapshot.Providers)
	}
	p := snapshot.Providers[0]
	combined := p.BaseURL + p.Message
	if strings.Contains(combined, secret) || strings.Contains(combined, "sk-") || strings.Contains(p.BaseURL, "user:") {
		t.Fatalf("error/unavailable metadata path leaked secret into snapshot: %#v", p)
	}
}

func TestModelSnapshotsFilterSortAndExposeCapabilities(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}

	models, err := ModelSnapshots(registry, ModelSnapshotOptions{Provider: modelregistry.ProviderAnthropic})

	if err != nil {
		t.Fatal(err)
	}
	if len(models) == 0 {
		t.Fatal("expected Anthropic model snapshots")
	}
	for index, model := range models {
		if model.Provider != string(modelregistry.ProviderAnthropic) {
			t.Fatalf("unexpected provider in filtered model %d: %#v", index, model)
		}
		if model.ID == "" || model.APIModel == "" || model.ContextWindow <= 0 {
			t.Fatalf("model snapshot missing required fields: %#v", model)
		}
		if index > 0 && models[index-1].ID > model.ID {
			t.Fatalf("model snapshots are not sorted: %#v", models)
		}
	}
}

func TestModelSnapshotsRejectUnknownProvider(t *testing.T) {
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}

	_, err = ModelSnapshots(registry, ModelSnapshotOptions{Provider: modelregistry.ProviderKind("chaos")})

	if err == nil {
		t.Fatal("expected unknown provider error")
	}
	commandErr, ok := err.(CommandError)
	if !ok {
		t.Fatalf("expected CommandError, got %T: %v", err, err)
	}
	if commandErr.Kind != ErrorKindUsage || !strings.Contains(commandErr.Message, "unknown model provider") {
		t.Fatalf("unexpected command error: %#v", commandErr)
	}
}

func TestSessionSnapshotsPreserveLineageFields(t *testing.T) {
	items := []sessions.Metadata{
		{
			SessionID:       "session_b",
			SessionKind:     sessions.SessionKindChild,
			Title:           "Child task",
			ParentSessionID: "session_a",
			ModelID:         "gpt-4.1",
			EventCount:      3,
			LastEventType:   sessions.EventPermission,
			AgentName:       "review",
			TaskID:          "task-1",
		},
		{
			SessionID:     "session_a",
			Title:         "Root task",
			EventCount:    2,
			LastEventType: sessions.EventMessage,
		},
	}

	snapshots := SessionSnapshots(items)

	if len(snapshots) != 2 {
		t.Fatalf("expected two snapshots, got %#v", snapshots)
	}
	child := snapshots[0]
	if child.SessionID != "session_b" || child.ParentSessionID != "session_a" || child.Kind != string(sessions.SessionKindChild) {
		t.Fatalf("lineage fields were not preserved: %#v", child)
	}
	if child.LastEventType != string(sessions.EventPermission) || child.AgentName != "review" || child.TaskID != "task-1" {
		t.Fatalf("session contract fields were not preserved: %#v", child)
	}
}

func TestSessionTreeSnapshotConvertsChildren(t *testing.T) {
	tree := sessions.TreeNode{
		Session: sessions.Metadata{SessionID: "root", Title: "Root", EventCount: 1},
		Children: []sessions.TreeNode{
			{Session: sessions.Metadata{SessionID: "child", ParentSessionID: "root", EventCount: 2}},
		},
	}

	snapshot := SessionTreeSnapshotFromNode(tree)

	if snapshot.Session.SessionID != "root" || len(snapshot.Children) != 1 {
		t.Fatalf("unexpected tree snapshot: %#v", snapshot)
	}
	if snapshot.Children[0].Session.SessionID != "child" || snapshot.Children[0].Session.ParentSessionID != "root" {
		t.Fatalf("child lineage not preserved in tree snapshot: %#v", snapshot)
	}
}
