package specialist

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Gitlawb/zero/internal/background"
)

func TestRuntimeCloseDoesNotCreateUnusedManager(t *testing.T) {
	created := false
	runtime := NewRuntime(RuntimeOptions{
		ManagerFunc: func() (*background.Manager, error) {
			created = true
			return background.NewManager(t.TempDir())
		},
	})

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if created {
		t.Fatal("Close created an unused background manager")
	}
}

func TestRuntimeCloseKillsRunningTasksAndCleansPromptFiles(t *testing.T) {
	killed := []int{}
	manager, err := background.NewManagerWithOptions(background.ManagerOptions{
		RootDir: t.TempDir(),
		KillProcess: func(pid int) error {
			killed = append(killed, pid)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewManagerWithOptions returned error: %v", err)
	}
	if _, err := manager.Register(background.RegisterInput{TaskID: "child_task", Type: "specialist", PID: 42}); err != nil {
		t.Fatalf("Register returned error: %v", err)
	}
	promptDir := filepath.Join(t.TempDir(), "zero-specialist-test")
	if err := os.MkdirAll(promptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	promptFile := filepath.Join(promptDir, "prompt.md")
	if err := os.WriteFile(promptFile, []byte("prompt"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(RuntimeOptions{Manager: manager})
	runtime.TrackPromptFile("child_task", promptFile)

	if err := runtime.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if !reflect.DeepEqual(killed, []int{42}) {
		t.Fatalf("killed pids = %#v", killed)
	}
	task, ok := manager.Get("child_task")
	if !ok || task.Status != background.StatusKilled {
		t.Fatalf("task after close = %#v", task)
	}
	if _, err := os.Stat(promptFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prompt file cleanup error = %v", err)
	}
}
