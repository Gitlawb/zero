package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestResolvePathsUsesXDGLocations(t *testing.T) {
	dir := t.TempDir()
	paths, err := ResolvePaths(ResolvePathOptions{
		Cwd: dir,
		Env: map[string]string{
			"XDG_CONFIG_HOME": filepath.Join(dir, "config"),
			"XDG_DATA_HOME":   filepath.Join(dir, "data"),
		},
	})
	if err != nil {
		t.Fatalf("ResolvePaths returned error: %v", err)
	}
	if paths.UserConfigPath != filepath.Join(dir, "config", "zero", "hooks.json") {
		t.Fatalf("user path = %q", paths.UserConfigPath)
	}
	if paths.ProjectConfigPath != filepath.Join(dir, ".zero", "hooks.json") {
		t.Fatalf("project path = %q", paths.ProjectConfigPath)
	}
	if paths.AuditPath != filepath.Join(dir, "data", "zero", "hooks", "audit.jsonl") {
		t.Fatalf("audit path = %q", paths.AuditPath)
	}
}

func TestLoadConfigLayersProjectOverridesAndDiagnostics(t *testing.T) {
	dir := t.TempDir()
	userConfigPath := filepath.Join(dir, "user-hooks.json")
	projectConfigPath := filepath.Join(dir, "project-hooks.json")
	writeHookJSON(t, userConfigPath, map[string]any{
		"enabled": true,
		"hooks": []any{
			map[string]any{
				"id":      "zero.format",
				"name":    "Format after edits",
				"event":   "afterTool",
				"matcher": "edit_file",
				"command": "bun",
				"args":    []string{"run", "format"},
			},
			map[string]any{
				"id":      "zero.audit",
				"event":   "sessionEnd",
				"command": "node",
				"args":    []string{"audit.mjs"},
			},
		},
	})
	writeHookJSON(t, projectConfigPath, map[string]any{
		"hooks": []any{map[string]any{
			"id":      "zero.format",
			"event":   "afterTool",
			"matcher": "write_file",
			"command": "bun",
			"args":    []string{"run", "lint"},
			"enabled": false,
		}},
	})

	result, err := LoadConfig(LoadOptions{UserConfigPath: userConfigPath, ProjectConfigPath: projectConfigPath})
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if !result.Config.Enabled {
		t.Fatalf("config should be enabled")
	}
	if got := []string{result.Config.Hooks[0].ID, result.Config.Hooks[1].ID}; !reflect.DeepEqual(got, []string{"zero.audit", "zero.format"}) {
		t.Fatalf("hook order = %#v", got)
	}
	format := result.Config.Hooks[1]
	if format.Enabled || format.Matcher != "write_file" || !reflect.DeepEqual(format.Args, []string{"run", "lint"}) {
		t.Fatalf("project override not applied: %#v", format)
	}
	if !hasHookDiagnostic(result.Diagnostics, DiagnosticDuplicate, "zero.format", "") {
		t.Fatalf("missing duplicate diagnostic: %#v", result.Diagnostics)
	}
}

func TestLoadConfigRejectsMatchersOnSessionHooks(t *testing.T) {
	dir := t.TempDir()
	projectConfigPath := filepath.Join(dir, "hooks.json")
	writeHookJSON(t, projectConfigPath, map[string]any{
		"hooks": []any{map[string]any{
			"id":      "zero.session",
			"event":   "sessionStart",
			"matcher": "bash",
			"command": "node",
		}},
	})

	result, err := LoadConfig(LoadOptions{
		UserConfigPath:    filepath.Join(dir, "missing-user-hooks.json"),
		ProjectConfigPath: projectConfigPath,
	})
	if err != nil {
		t.Fatalf("LoadConfig returned error: %v", err)
	}
	if len(result.Config.Hooks) != 0 {
		t.Fatalf("expected invalid hooks to be skipped: %#v", result.Config.Hooks)
	}
	if !hasHookDiagnostic(result.Diagnostics, DiagnosticSchema, "", "hooks.0.matcher") {
		t.Fatalf("missing matcher diagnostic: %#v", result.Diagnostics)
	}
}

func TestConfigStorePersistsUpdates(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "hooks.json")
	store := NewConfigStore(StoreOptions{ConfigPath: configPath})

	_, err := store.Upsert(Definition{
		ID:      "zero.preflight",
		Name:    "Preflight",
		Event:   EventBeforeTool,
		Matcher: "bash",
		Command: "node",
		Args:    []string{"hooks/preflight.mjs"},
	})
	if err != nil {
		t.Fatalf("Upsert returned error: %v", err)
	}
	config, err := store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if !config.Hooks[0].Enabled {
		t.Fatalf("new hooks should default to enabled: %#v", config.Hooks[0])
	}
	changed, err := store.SetEnabled("zero.preflight", false)
	if err != nil || !changed {
		t.Fatalf("SetEnabled changed=%v err=%v", changed, err)
	}

	config, err = store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if config.Hooks[0].Enabled || config.Hooks[0].Matcher != "bash" {
		t.Fatalf("unexpected stored hook: %#v", config.Hooks[0])
	}
	removed, err := store.Remove("zero.preflight")
	if err != nil || !removed {
		t.Fatalf("Remove removed=%v err=%v", removed, err)
	}
	config, err = store.List()
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(config.Hooks) != 0 {
		t.Fatalf("expected no hooks after remove, got %#v", config.Hooks)
	}
}

func TestSelectMatchesEnabledHooksByEventAndWildcard(t *testing.T) {
	config := Config{Enabled: true, Hooks: []Definition{
		{ID: "zero.reads", Event: EventBeforeTool, Matcher: "read_*", Command: "node", Enabled: true},
		{ID: "zero.shell", Event: EventBeforeTool, Matcher: "bash", Command: "node", Enabled: false},
		{ID: "zero.done", Event: EventSessionEnd, Command: "node", Enabled: true},
		{ID: "zero.shell-edit", Event: EventBeforeTool, Matcher: "shell_*_edit", Command: "node", Enabled: true},
	}}

	if got := hookIDs(Select(config, SelectInput{Event: EventBeforeTool, ToolName: "read_file"})); !reflect.DeepEqual(got, []string{"zero.reads"}) {
		t.Fatalf("read selection = %#v", got)
	}
	if got := hookIDs(Select(config, SelectInput{Event: EventBeforeTool, ToolName: "shell_safe_edit"})); !reflect.DeepEqual(got, []string{"zero.shell-edit"}) {
		t.Fatalf("shell edit selection = %#v", got)
	}
	if got := Select(config, SelectInput{Event: EventBeforeTool, ToolName: "shell_safe_view"}); len(got) != 0 {
		t.Fatalf("unexpected selection: %#v", got)
	}
}

func TestAuditStoreAppendsAndSkipsMalformedLines(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(auditPath, []byte(strings.Join([]string{
		`{"sequence":1,"createdAt":"2026-06-04T00:00:00Z","type":"hook_execution_started","hookId":"zero.seed","event":"sessionStart"}`,
		"{not-json",
		`{"sequence":2,"createdAt":"2026-06-04T00:00:01Z","type":"hook_execution_completed","hookId":"zero.seed","event":"sessionStart","status":"completed"}`,
		"",
	}, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewAuditStore(AuditStoreOptions{
		AuditPath: auditPath,
		Now:       func() time.Time { return time.Date(2026, 6, 4, 0, 0, 2, 0, time.UTC) },
	})

	events, err := store.ReadEvents()
	if err != nil {
		t.Fatalf("ReadEvents returned error: %v", err)
	}
	if got := []int{events[0].Sequence, events[1].Sequence}; !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("initial sequences = %#v", got)
	}

	appended, err := store.AppendStarted(AppendStartedInput{
		HookID:   "zero.preflight",
		Event:    EventBeforeTool,
		Matcher:  "bash",
		Commands: []AuditCommand{{Command: "node", Args: []string{"hooks/preflight.mjs"}}},
	})
	if err != nil {
		t.Fatalf("AppendStarted returned error: %v", err)
	}
	if appended.Sequence != 3 || appended.CreatedAt != "2026-06-04T00:00:02Z" {
		t.Fatalf("unexpected appended event: %#v", appended)
	}
}

func writeHookJSON(t *testing.T, path string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func hookIDs(definitions []Definition) []string {
	ids := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		ids = append(ids, definition.ID)
	}
	return ids
}

func hasHookDiagnostic(diagnostics []Diagnostic, kind DiagnosticKind, hookID string, fieldPath string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Kind != kind {
			continue
		}
		if hookID != "" && diagnostic.HookID != hookID {
			continue
		}
		if fieldPath != "" && diagnostic.FieldPath != fieldPath {
			continue
		}
		return true
	}
	return false
}
