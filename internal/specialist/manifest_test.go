package specialist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseMarkdownValidatesAndResolvesTools(t *testing.T) {
	manifest, err := ParseMarkdown(`---
name: code-review
description: Reviews a change
tools: [read-only, apply_patch]
unknown: kept
---
Review the diff.`)
	if err != nil {
		t.Fatalf("ParseMarkdown returned error: %v", err)
	}
	if manifest.Metadata.Name != "code-review" || manifest.SystemPrompt != "Review the diff." {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	for _, want := range []string{"apply_patch", "glob", "grep", "list_directory", "read_file"} {
		if !contains(manifest.ResolvedTools, want) {
			t.Fatalf("resolved tools missing %q: %#v", want, manifest.ResolvedTools)
		}
	}
	if len(manifest.Warnings) != 1 || !strings.Contains(manifest.Warnings[0], "unknown") {
		t.Fatalf("expected unknown key warning, got %#v", manifest.Warnings)
	}
}

func TestParseMarkdownRejectsScalarTools(t *testing.T) {
	_, err := ParseMarkdown(`---
name: explorer
description: Explores code
tools: read-only
---
Explore.`)
	if err == nil || !strings.Contains(err.Error(), "tools must be an array") {
		t.Fatalf("expected tools array error, got %v", err)
	}
}

func TestParseMarkdownUsesDescriptionFallback(t *testing.T) {
	manifest, err := ParseMarkdown(`---
name: greeter
description: Greets the user
---`)
	if err != nil {
		t.Fatalf("ParseMarkdown returned error: %v", err)
	}
	if manifest.SystemPrompt != "Greets the user" {
		t.Fatalf("SystemPrompt = %q, want description fallback", manifest.SystemPrompt)
	}
	if len(manifest.Warnings) != 1 || !strings.Contains(manifest.Warnings[0], "description") {
		t.Fatalf("expected description fallback warning, got %#v", manifest.Warnings)
	}
}

func TestLoadMergesBuiltinsUserAndProjectByPrecedence(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(root, "user")
	projectDir := filepath.Join(root, "project")
	writeManifest(t, filepath.Join(userDir, "explorer.md"), `---
name: explorer
description: User explorer override
tools: [read-only]
---
User prompt.`)
	writeManifest(t, filepath.Join(projectDir, "worker.md"), `---
name: worker
description: Project worker override
tools: [execute]
---
Project prompt.`)

	result, err := Load(LoadOptions{Paths: Paths{UserDir: userDir, ProjectDir: projectDir}})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	explorer, ok := Find(result, "explorer")
	if !ok {
		t.Fatal("explorer not found")
	}
	if explorer.Location != LocationUser || explorer.SystemPrompt != "User prompt." {
		t.Fatalf("unexpected explorer override: %#v", explorer)
	}
	worker, ok := Find(result, "worker")
	if !ok {
		t.Fatal("worker not found")
	}
	if worker.Location != LocationProject || !contains(worker.ResolvedTools, "bash") {
		t.Fatalf("unexpected worker override: %#v", worker)
	}
}

func TestFormatListUsesSpecialistTerminology(t *testing.T) {
	result := LoadResult{Specialists: []Manifest{{
		Metadata: Metadata{Name: "worker", Description: "Does work"},
		Location: LocationBuiltin,
		FilePath: "(builtin)",
	}}}
	output := FormatList(result)
	if !strings.Contains(output, "Zero Specialists") || !strings.Contains(output, "worker [builtin]") {
		t.Fatalf("unexpected list output: %s", output)
	}
}

func writeManifest(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create manifest dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
