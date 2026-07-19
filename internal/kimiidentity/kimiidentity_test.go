package kimiidentity

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestHeadersIncludesDeviceIdentity(t *testing.T) {
	headers := Headers()
	for _, key := range []string{
		"X-Msh-Platform",
		"X-Msh-Version",
		"X-Msh-Device-Name",
		"X-Msh-Device-Model",
		"X-Msh-Os-Version",
		"X-Msh-Device-Id",
	} {
		if headers[key] == "" {
			t.Fatalf("Headers()[%q] empty", key)
		}
	}
	if headers["X-Msh-Platform"] != "zero-cli" {
		t.Fatalf("X-Msh-Platform = %q, want zero-cli", headers["X-Msh-Platform"])
	}
	if !isUUID(headers["X-Msh-Device-Id"]) {
		t.Fatalf("X-Msh-Device-Id = %q, want UUID", headers["X-Msh-Device-Id"])
	}
}

func TestLoadOrCreateDeviceIDExclusiveCreate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "zero", "kimi-device-id")
	// Point the loader at the temp config dir for this process.
	t.Setenv("XDG_CONFIG_HOME", dir)
	// On macOS/Windows UserConfigDir ignores XDG; force via GOOS-specific
	// fallback by rewriting through a private path helper is not available,
	// so exercise the exclusive-create path directly.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	ids := make([]string, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range workers {
		go func(i int) {
			defer wg.Done()
			// Concurrent exclusive creates: only one OpenFile succeeds; others
			// adopt the winner. Call the create path the same way loadOrCreate
			// does after a missing file.
			id := generateDeviceID()
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err == nil {
				_, _ = f.WriteString(id + "\n")
				_ = f.Close()
				ids[i] = id
				return
			}
			if !os.IsExist(err) {
				t.Errorf("unexpected open error: %v", err)
				return
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Errorf("read back: %v", readErr)
				return
			}
			existing := string(raw)
			// trim newline manually without importing more than needed
			if len(existing) > 0 && existing[len(existing)-1] == '\n' {
				existing = existing[:len(existing)-1]
			}
			ids[i] = existing
		}(i)
	}
	wg.Wait()

	winner := ""
	for _, id := range ids {
		if id == "" {
			t.Fatal("worker returned empty id")
		}
		if winner == "" {
			winner = id
			continue
		}
		if id != winner {
			t.Fatalf("workers diverged: got %q and %q", winner, id)
		}
	}
	if !isUUID(winner) {
		t.Fatalf("winner id %q is not a UUID", winner)
	}
}

func TestAsciiHeaderValueStripsNonPrintable(t *testing.T) {
	if got := asciiHeaderValue("linux#6.1"); got != "linux#6.1" {
		// printable ASCII including # is kept; the kimi-cli bug was a different
		// control character path — ensure we still strip true controls.
		t.Fatalf("got %q", got)
	}
	if got := asciiHeaderValue("a\nb\x00c"); got != "abc" {
		t.Fatalf("got %q, want abc", got)
	}
	if got := asciiHeaderValue("\x01\x02"); got != "unknown" {
		t.Fatalf("got %q, want unknown", got)
	}
}
