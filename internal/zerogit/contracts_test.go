package zerogit

import (
	"strings"
	"testing"
)

func TestSnapshotFromSummaryRedactsDiffAndBuildsEvents(t *testing.T) {
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz"
	summary := ChangeSummary{
		Root:     "/repo/" + secret,
		Branch:   "feature/" + secret,
		Commit:   "abc1234",
		Clean:    false,
		DiffStat: "README.md | 1 +",
		Diff:     "+ token " + secret,
		Files: []FileChange{
			{Path: "README.md", Status: "modified", Unstaged: true},
			{Path: "secret-" + secret + ".txt", Status: "added", Untracked: true},
		},
	}

	snapshot := SnapshotFromSummary(summary)

	if snapshot.Contract != ChangeContractVersion || snapshot.Runtime != RuntimeGo {
		t.Fatalf("unexpected snapshot metadata: %#v", snapshot)
	}
	if len(snapshot.Files) != 2 || snapshot.Files[0].Path != "README.md" {
		t.Fatalf("file changes were not preserved: %#v", snapshot.Files)
	}
	combined := snapshot.Root + snapshot.Branch + snapshot.Diff + snapshot.Files[1].Path
	if strings.Contains(combined, secret) || !strings.Contains(combined, "[REDACTED]") {
		t.Fatalf("change snapshot leaked secret: %#v", snapshot)
	}
	if len(snapshot.Events) != 3 {
		t.Fatalf("expected summary and two file events, got %#v", snapshot.Events)
	}
	if snapshot.Events[0].Type != EventChangeSummary || snapshot.Events[1].Type != EventFileChange {
		t.Fatalf("unexpected event order: %#v", snapshot.Events)
	}
	if snapshot.Events[1].Path != "README.md" || snapshot.Events[1].Status != "modified" {
		t.Fatalf("file event did not capture change: %#v", snapshot.Events[1])
	}
}

func TestSnapshotFromSummaryCarriesBase(t *testing.T) {
	summary := ChangeSummary{
		Root:   "/repo",
		Branch: "feature",
		Base:   "main",
		Files:  []FileChange{{Path: "a.txt", Status: "added"}},
	}
	snapshot := SnapshotFromSummary(summary)
	if snapshot.Base != "main" {
		t.Fatalf("snapshot.Base = %q, want main", snapshot.Base)
	}
	if snapshot.Branch != "feature" {
		t.Fatalf("snapshot.Branch = %q, want feature", snapshot.Branch)
	}
}

func TestSnapshotFromSummaryOmitsEmptyBase(t *testing.T) {
	snapshot := SnapshotFromSummary(ChangeSummary{Root: "/repo"})
	if snapshot.Base != "" {
		t.Fatalf("snapshot.Base = %q, want empty", snapshot.Base)
	}
}

func TestEventsFromSummaryHandlesCleanRepository(t *testing.T) {
	snapshot := SnapshotFromSummary(ChangeSummary{Root: "/repo", Branch: "main", Clean: true})
	events := snapshot.Events

	if snapshot.Files == nil {
		t.Fatalf("expected empty files slice, got nil: %#v", snapshot)
	}
	if len(events) != 1 {
		t.Fatalf("expected one clean summary event, got %#v", events)
	}
	if events[0].Type != EventChangeSummary || !events[0].Clean {
		t.Fatalf("unexpected clean summary event: %#v", events[0])
	}
}
