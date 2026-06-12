package agenteval

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMaterializeTaskCopiesFixtureAndInitializesGit(t *testing.T) {
	suitePath := filepath.Join("testdata", "sample_suite.json")
	suite, err := LoadSuite(suitePath)
	if err != nil {
		t.Fatal(err)
	}
	task := suite.Tasks[0]
	workRoot := t.TempDir()

	workspace, err := Materializer{}.MaterializeTask(context.Background(), suitePath, task, MaterializeInput{WorkRoot: workRoot})
	if err != nil {
		t.Fatalf("MaterializeTask: %v", err)
	}

	if workspace.TaskID != task.ID {
		t.Fatalf("TaskID = %q, want %q", workspace.TaskID, task.ID)
	}
	if workspace.Path == "" || !strings.HasPrefix(filepath.Base(workspace.Path), task.ID) {
		t.Fatalf("workspace path %q does not use task id prefix %q", workspace.Path, task.ID)
	}
	if workspace.FixturePath == "" || !filepath.IsAbs(workspace.FixturePath) {
		t.Fatalf("FixturePath = %q, want absolute path", workspace.FixturePath)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, "go.mod")); err != nil {
		t.Fatalf("fixture was not copied: %v", err)
	}
	if output, err := exec.Command("git", "-C", workspace.Path, "status", "--porcelain").CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "" {
		t.Fatalf("workspace baseline is dirty: err=%v output=%s", err, output)
	}
}

func TestMaterializeTaskSkipsGitDirectory(t *testing.T) {
	suitePath, task := writeMaterializerFixture(t, "fixtures/source", map[string]string{
		"keep.txt":          "keep",
		".git/config":       "do not copy",
		".git/objects/blob": "do not copy",
	})

	workspace, err := Materializer{}.MaterializeTask(context.Background(), suitePath, task, MaterializeInput{WorkRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("MaterializeTask: %v", err)
	}

	if _, err := os.Stat(filepath.Join(workspace.Path, "keep.txt")); err != nil {
		t.Fatalf("fixture file was not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace.Path, ".git", "objects", "blob")); !os.IsNotExist(err) {
		t.Fatalf("source .git directory was copied, stat err=%v", err)
	}
}

func TestMaterializerRejectsInvalidInputs(t *testing.T) {
	suitePath, task := writeMaterializerFixture(t, "fixtures/source", map[string]string{
		"keep.txt": "keep",
	})
	workRoot := t.TempDir()

	tests := []struct {
		name    string
		task    Task
		input   MaterializeInput
		wantErr string
	}{
		{
			name:    "empty work root",
			task:    task,
			input:   MaterializeInput{},
			wantErr: "work root",
		},
		{
			name:    "missing fixture",
			task:    Task{ID: "missing-fixture", WorkspaceFixture: "fixtures/missing"},
			input:   MaterializeInput{WorkRoot: workRoot},
			wantErr: "fixture",
		},
		{
			name:    "absolute fixture",
			task:    Task{ID: "absolute-fixture", WorkspaceFixture: absoluteFixturePath()},
			input:   MaterializeInput{WorkRoot: workRoot},
			wantErr: "relative",
		},
		{
			name:    "escaping fixture",
			task:    Task{ID: "escaping-fixture", WorkspaceFixture: "../outside"},
			input:   MaterializeInput{WorkRoot: workRoot},
			wantErr: "within",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Materializer{}.MaterializeTask(context.Background(), suitePath, tt.task, tt.input)
			if err == nil {
				t.Fatal("MaterializeTask returned nil error")
			}
			if !strings.Contains(strings.ToLower(err.Error()), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestMaterializerSanitizesWorkspacePrefix(t *testing.T) {
	suitePath, task := writeMaterializerFixture(t, "fixtures/source", map[string]string{
		"keep.txt": "keep",
	})
	task.ID = "Bad Task:../ID"

	workspace, err := Materializer{}.MaterializeTask(context.Background(), suitePath, task, MaterializeInput{WorkRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("MaterializeTask: %v", err)
	}

	base := filepath.Base(workspace.Path)
	if strings.ContainsAny(base, ` :\/`) || !strings.HasPrefix(base, "Bad-Task") {
		t.Fatalf("workspace base = %q, want sanitized prefix", base)
	}
}

func writeMaterializerFixture(t *testing.T, fixture string, files map[string]string) (string, Task) {
	t.Helper()
	root := t.TempDir()
	suitePath := filepath.Join(root, "suite.json")
	fixturePath := filepath.Join(root, filepath.FromSlash(fixture))
	for name, content := range files {
		path := filepath.Join(fixturePath, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return suitePath, Task{ID: "copy-fixture", WorkspaceFixture: fixture}
}

func absoluteFixturePath() string {
	if runtime.GOOS == "windows" {
		return `C:\absolute\fixture`
	}
	return "/absolute/fixture"
}
