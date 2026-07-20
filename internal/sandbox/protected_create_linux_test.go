//go:build linux

package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProtectedCreateMonitorStopsCommandAndRemovesTarget(t *testing.T) {
	target := filepath.Join(t.TempDir(), ".zero")
	var stderr bytes.Buffer
	started := time.Now()
	code := runLinuxSandboxWithProtectedCreateMonitor("/bin/sh", linuxSandboxBwrapPlan{
		Args:                   []string{"-c", "mkdir " + shellQuote(target) + "; while :; do :; done"},
		ProtectedCreateTargets: []string{target},
	}, "", &stderr)

	if code != 1 {
		t.Fatalf("exit code = %d, want policy failure 1; stderr=%s", code, stderr.String())
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("protected creation did not stop command promptly; elapsed=%s", elapsed)
	}
	if !strings.Contains(stderr.String(), "blocked creation of protected workspace metadata path") {
		t.Fatalf("missing denial reason: %s", stderr.String())
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("protected target remained after denial: %v", err)
	}
}
