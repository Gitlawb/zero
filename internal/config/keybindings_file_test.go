package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDefaultKeybindingsFileSeedsCatalog(t *testing.T) {
	seed := DefaultKeybindingsFile([]string{"/model", "/help", "/exit"})
	if seed.LeaderKey != DefaultLeaderKey {
		t.Fatalf("LeaderKey = %q, want %q", seed.LeaderKey, DefaultLeaderKey)
	}
	if seed.Leader["/model"] != "m" {
		t.Fatalf("/model = %q, want m", seed.Leader["/model"])
	}
	if seed.Leader["/help"] != "" {
		t.Fatalf("/help = %q, want empty", seed.Leader["/help"])
	}
	if seed.Leader["/exit"] != "" {
		t.Fatalf("/exit = %q, want empty", seed.Leader["/exit"])
	}
	// Default assignments always present even if catalog omitted them.
	if seed.Leader["/provider"] != "p" {
		t.Fatalf("/provider missing from seed: %#v", seed.Leader)
	}
}

func TestEnsureKeybindingsFileCreatesOnce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keybindings.json")
	seed := DefaultKeybindingsFile([]string{"/model", "/help"})
	if err := EnsureKeybindingsFile(path, seed); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Mutate file, ensure again must not overwrite.
	if err := os.WriteFile(path, []byte(`{"leaderKey":"alt+x","leader":{}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureKeybindingsFile(path, seed); err != nil {
		t.Fatalf("Ensure again: %v", err)
	}
	raw2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw2) == string(raw) {
		t.Fatal("expected user file to remain after second ensure")
	}
	if !strings.Contains(string(raw2), `"leaderKey":"alt+x"`) && !strings.Contains(string(raw2), `"leaderKey": "alt+x"`) {
		t.Fatalf("user content clobbered: %s", raw2)
	}
}

func TestEnsureKeybindingsBesideConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	SetKeybindingsPrimarySlashes([]string{"/model", "/help"})
	t.Cleanup(func() { SetKeybindingsPrimarySlashes(nil) })
	if err := EnsureKeybindingsBesideConfig(configPath); err != nil {
		t.Fatal(err)
	}
	kbPath := KeybindingsPathBeside(configPath)
	file, err := LoadKeybindingsFile(kbPath)
	if err != nil {
		t.Fatal(err)
	}
	if file.Leader["/model"] != "m" || file.Leader["/help"] != "" {
		t.Fatalf("unexpected seed: %#v", file.Leader)
	}
}

func TestWriteConfigFileSeedsKeybindings(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	SetKeybindingsPrimarySlashes([]string{"/model", "/theme"})
	t.Cleanup(func() { SetKeybindingsPrimarySlashes(nil) })
	if err := writeConfigFile(configPath, FileConfig{}); err != nil {
		t.Fatal(err)
	}
	kbPath := KeybindingsPathBeside(configPath)
	if _, err := os.Stat(kbPath); err != nil {
		t.Fatalf("expected sibling keybindings.json: %v", err)
	}
}

func TestResolveKeybindingsOverlay(t *testing.T) {
	defaults := DefaultKeybindingsFile([]string{"/model", "/clear", "/theme"})
	user := KeybindingsFile{
		LeaderKey: "alt+space",
		Leader: map[string]string{
			"/theme": "m",
			"/clear": "",
		},
	}
	project := KeybindingsFile{
		Leader: map[string]string{
			"/theme": "t",
		},
	}
	got := ResolveKeybindings(defaults, user, project)
	if got.LeaderKey != "alt+space" {
		t.Fatalf("LeaderKey = %q", got.LeaderKey)
	}
	if got.Leader["/theme"] != "t" {
		t.Fatalf("/theme = %q, want project t", got.Leader["/theme"])
	}
	if got.Leader["/clear"] != "" {
		t.Fatalf("/clear should be unbound, got %q", got.Leader["/clear"])
	}
	if got.Leader["/model"] != "m" {
		t.Fatalf("/model default lost: %q", got.Leader["/model"])
	}
}

func TestLoadKeybindingsMissingIsEmpty(t *testing.T) {
	file, err := LoadKeybindingsFile(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if file.LeaderKey != "" || len(file.Leader) != 0 {
		t.Fatalf("unexpected file: %#v", file)
	}
}

func TestLoadKeybindingsCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadKeybindingsFile(path)
	if err == nil {
		t.Fatal("want parse error")
	}
}

func TestKeybindingsJSONRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	seed := DefaultKeybindingsFile([]string{"/model", "/help"})
	if err := writeKeybindingsFile(path, seed); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded KeybindingsFile
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LeaderKey != seed.LeaderKey || decoded.Leader["/model"] != "m" {
		t.Fatalf("round-trip mismatch: %#v", decoded)
	}
}

// Concurrent Ensure on a missing path must leave exactly one valid file and
// never report an error when another process wins the create race.
func TestEnsureKeybindingsFileConcurrentCreate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybindings.json")
	seed := DefaultKeybindingsFile([]string{"/model", "/help", "/theme"})

	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- EnsureKeybindingsFile(path, seed)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Ensure: %v", err)
		}
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded KeybindingsFile
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("concurrent create left invalid JSON: %v\n%s", err, raw)
	}
	if decoded.LeaderKey != DefaultLeaderKey || decoded.Leader["/model"] != "m" {
		t.Fatalf("unexpected seed after concurrent create: %#v", decoded)
	}

	// User edits must survive concurrent re-ensure (O_EXCL no-op, not rename).
	custom := []byte(`{"leaderKey":"alt+x","leader":{"/model":"z"}}` + "\n")
	if err := os.WriteFile(path, custom, 0o600); err != nil {
		t.Fatal(err)
	}
	errs2 := make(chan error, n)
	var wg2 sync.WaitGroup
	for i := 0; i < n; i++ {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			// Different seed would overwrite under a racy rename; must not here.
			errs2 <- EnsureKeybindingsFile(path, DefaultKeybindingsFile([]string{"/model"}))
		}()
	}
	wg2.Wait()
	close(errs2)
	for err := range errs2 {
		if err != nil {
			t.Fatalf("concurrent Ensure on existing: %v", err)
		}
	}
	raw2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw2) != string(custom) {
		t.Fatalf("concurrent Ensure overwrote user file:\n got %s\nwant %s", raw2, custom)
	}
}
