package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plantKeptTxn writes the transaction marker both install sites read: the kind
// that site writes, the destination the copy was set aside for, and the sequence
// its directory name carries. Written here by hand rather than through either
// package, so the command is exercised against the on-disk format an operator
// actually has, not against a helper that could drift with it.
func plantKeptTxn(t *testing.T, dir, kind, dest string, seq int64) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{"kind": kind, "dest": dest, "seq": seq})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "txn"), payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

// plantSTTKept writes a Kept backup of a dictation install: the engine copy an
// operator has to be able to find, because it is the only offline one.
func plantSTTKept(t *testing.T, root, dest string, seq int64, content string) string {
	t.Helper()
	name := fmt.Sprintf("%s.kept-%020d-seq", dest, seq)
	dir := filepath.Join(root, name)
	plantKeptTxn(t, dir, "dictation-promote", dest, seq)
	if err := os.MkdirAll(filepath.Join(dir, "install"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "install", "engine"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

// plantBundleKept writes a Kept backup of a bundle extract: a tree the client
// that uploaded it can send again, which is why the two sites cost differently.
func plantBundleKept(t *testing.T, dir, linkID string, seq int64, content string) string {
	t.Helper()
	name := fmt.Sprintf(".kept-%020d-seq", seq)
	path := filepath.Join(dir, name)
	plantKeptTxn(t, path, "bundle-extract", linkID, seq)
	if err := os.MkdirAll(filepath.Join(path, "backup"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "backup", "a.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func keptDeps(userConfigPath string) appDeps {
	return appDeps{userConfigPath: func() (string, error) { return userConfigPath, nil }}
}

// The listing is the only way a retained copy is found again: recovery never
// enumerates the Kept prefix and nothing reclaims one on its own. It has to name
// the site, because the same command reads two of them and the cost of keeping a
// copy is not the same at both.
func TestKeptBackupsListPrintsBothSites(t *testing.T) {
	userConfigPath := filepath.Join(t.TempDir(), "zero", "config.json")
	sttRoot := filepath.Join(filepath.Dir(userConfigPath), "stt")
	bundleDir := t.TempDir()

	sttName := plantSTTKept(t, sttRoot, "engine-a", 1, "engine-bytes")
	bundleName := plantBundleKept(t, bundleDir, "proj-1", 7, "tree")
	// Kept grammar, nothing attributing it: recovery's residue, which the
	// operator has to see beside the real backups to reclaim it by hand.
	unownedName := fmt.Sprintf(".kept-%020d-seq", 8)
	if err := os.MkdirAll(filepath.Join(bundleDir, unownedName), 0o700); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"kept-backups", "list", "--bundle-dir", bundleDir}, &stdout, &stderr, keptDeps(userConfigPath)); code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}

	lines := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 {
			lines[fields[1]] = line
		}
	}
	stt, ok := lines[sttName]
	if !ok {
		t.Fatalf("the dictation kept backup is missing from:\n%s", stdout.String())
	}
	for _, want := range []string{"stt", "dest=engine-a", "seq=1", "bytes="} {
		if !strings.Contains(stt, want) {
			t.Errorf("dictation line %q is missing %q", stt, want)
		}
	}
	if strings.Contains(stt, "bytes=0") {
		t.Errorf("dictation line %q reports no bytes for a copy that holds some", stt)
	}
	bundle, ok := lines[bundleName]
	if !ok {
		t.Fatalf("the bundle kept backup is missing from:\n%s", stdout.String())
	}
	for _, want := range []string{"bundle", "dest=proj-1", "seq=7", "bytes="} {
		if !strings.Contains(bundle, want) {
			t.Errorf("bundle line %q is missing %q", bundle, want)
		}
	}
	unowned, ok := lines[unownedName]
	if !ok {
		t.Fatalf("the unowned entry is missing from:\n%s", stdout.String())
	}
	if !strings.Contains(unowned, "unowned") {
		t.Errorf("unowned line %q does not say so", unowned)
	}
}

// Which site a removal lands on is decided by --bundle-dir alone. Getting that
// wrong deletes a copy at one site while the operator watches the other, so the
// name has to resolve at the site the flag names and nowhere else.
func TestKeptBackupsRemoveNamesTheSite(t *testing.T) {
	userConfigPath := filepath.Join(t.TempDir(), "zero", "config.json")
	sttRoot := filepath.Join(filepath.Dir(userConfigPath), "stt")
	bundleDir := t.TempDir()
	sttName := plantSTTKept(t, sttRoot, "engine-a", 1, "engine-bytes")
	bundleName := plantBundleKept(t, bundleDir, "proj-1", 7, "tree")

	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"kept-backups", "remove"}, &stdout, &stderr, keptDeps(userConfigPath)); code != exitUsage {
		t.Fatalf("remove with no name: exit = %d, want %d (stderr %s)", code, exitUsage, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runWithDeps([]string{"kept-backups", "remove", sttName}, &stdout, &stderr, keptDeps(userConfigPath)); code != 0 {
		t.Fatalf("remove at the dictation root: exit = %d, stderr = %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(sttRoot, sttName)); !os.IsNotExist(err) {
		t.Errorf("the dictation kept backup should be gone, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(bundleDir, bundleName)); err != nil {
		t.Errorf("a removal with no --bundle-dir must not touch the bundle site: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runWithDeps([]string{"kept-backups", "remove", bundleName, "--bundle-dir", bundleDir}, &stdout, &stderr, keptDeps(userConfigPath)); code != 0 {
		t.Fatalf("remove at the bundle dir: exit = %d, stderr = %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(bundleDir, bundleName)); !os.IsNotExist(err) {
		t.Errorf("the bundle kept backup should be gone, got %v", err)
	}
}

// The two sites cost differently and only the usage text says so, so a run that
// stops printing it leaves the operator deciding blind.
func TestKeptBackupsUsageNamesTheCostOfEachSite(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runWithDeps([]string{"kept-backups", "-h"}, &stdout, &stderr, keptDeps("")); code != exitSuccess {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	for _, want := range []string{"only offline copy", "upload"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("usage is missing %q:\n%s", want, stdout.String())
		}
	}
}

// The reclaim command is the only path a retained copy has off the disk, so a
// command that never appears in help or completions is a reclaim path that
// exists but cannot be found.
func TestKeptBackupsIsDiscoverable(t *testing.T) {
	var out bytes.Buffer
	if code := Run([]string{"--help"}, &out, &out); code != 0 {
		t.Fatalf("--help exit = %d", code)
	}
	if !strings.Contains(out.String(), "kept-backups") {
		t.Error("kept-backups is missing from the command list")
	}
	var comp bytes.Buffer
	if code := Run([]string{"completions", "bash"}, &comp, &comp); code != 0 {
		t.Fatalf("completions exit = %d", code)
	}
	if !strings.Contains(comp.String(), "kept-backups") {
		t.Error("kept-backups is missing from the completion tree")
	}
}
