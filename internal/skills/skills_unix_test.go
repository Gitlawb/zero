//go:build unix

package skills

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Regression: a SKILL.md that is a FIFO (or other non-regular in-root file) must
// be skipped, never read. os.ReadFile on a FIFO blocks until a writer appears, so
// without the IsRegular guard the permission-allow skill loader would hang.
func TestLoadSkipsNonRegularSkillFile(t *testing.T) {
	dir := t.TempDir()
	writeSkill(t, dir, "good", "---\nname: good\ndescription: ok\n---\nbody")

	fifoDir := filepath.Join(dir, "fifo")
	if err := os.MkdirAll(fifoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(fifoDir, "SKILL.md"), 0o644); err != nil {
		t.Skipf("mkfifo unavailable on this platform: %v", err)
	}

	type result struct {
		skills []Skill
		err    error
	}
	done := make(chan result, 1)
	go func() {
		skills, err := Load(dir)
		done <- result{skills, err}
	}()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Load returned error: %v", got.err)
		}
		if len(got.skills) != 1 || got.skills[0].Name != "good" {
			t.Fatalf("expected only the regular skill, got %+v", got.skills)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Load blocked on a FIFO SKILL.md; it must skip non-regular files")
	}
}
