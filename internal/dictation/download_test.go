package dictation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Tiny tar.bz2 fixtures (generated in-repo): the engine has top/bin/sherpa-onnx-
// offline + server; the model has top/tokens.txt.
const (
	engineTarB64 = "QlpoOTFBWSZTWW+fEfAAAW1/hMGRAIBAA/+AAgAgRH9v3+AAAQCoMAEYAYA00NGjEZANABoYYA00NGjEZANABoYIpE0TRoTGkA2poGgxqepl1H6w5anW3tY2OggUi1AW/d29pO0GLUoOtnQsrGFNowIEh0cImtQ9gyHqYH7unm+/wZSLXsBuBDDCfHiahCsGEgGL9/pUG+RQZo76xlaDIGKWDErPKuyhXYOlzJMG3iNa56kiluctzUqgZ2A1AGwgcB3jtB5PB7pYYlDHH8OvOznM6B6MHPRqa7sTE1vwDBc0LjmG4ah5F+YYaXHTZpGmcemWW1yMh1adhdrB6A3nAQP8XckU4UJBvnxHwA=="
	engineSHA    = "97b5cb6a417705860f8940e1bf9ffec8cc60e8666a0d228cd60f1de7607d503d"
	modelTarB64  = "QlpoOTFBWSZTWTtAoX4AANp/hMOQBIBAAf+AAAIQBHoJ3mAAAQAIMAC5sG1SNMhNqGmQwIyeKEagam1DQ0AAABFRT0T0mg0aeoDQHpH7Nzrs24wsbVW8hMQHClNI0LuLhKMGY4sEQi6N2qQWP1id8o1KtprGd4hll4uQkMGA1nFMXAwohEZOnIMIPQKQl2k0GbSGwkC0lJb2IWOQ57ETYbmxFGspaRp1QyT6q4sKj855st7GzOGj5eIwmWt6wRajEQl/F3JFOFCQO0Chfg=="
	modelSHA     = "16c6b1dcb40f6b21c0de70bc1a8edab95375e4de90dac7c076fd2cc7a34fc59a"
)

// fakeReleaseServer serves the GitHub release API + asset bytes for the engine
// and model, exercising resolveAsset + downloadVerifyExtract against a real HTTP
// round-trip (with the real coder/websocket-free download path).
func fakeReleaseServer(t *testing.T, engineDigest, modelDigest string) *httptest.Server {
	t.Helper()
	engineBytes, _ := base64.StdEncoding.DecodeString(engineTarB64)
	modelBytes, _ := base64.StdEncoding.DecodeString(modelTarB64)
	mux := http.NewServeMux()
	base := ""
	release := func(assetName, digest, dlPath string) string {
		body, _ := json.Marshal(map[string]any{
			"tag_name": "test",
			"assets": []map[string]any{{
				"name":                 assetName,
				"browser_download_url": base + dlPath,
				"digest":               "sha256:" + digest,
				"size":                 123,
			}},
		})
		return string(body)
	}
	var srv *httptest.Server
	// Any release tag under this prefix resolves: asr-models → the model, every
	// other tag → the engine (so the default-version pinned-check test works too).
	mux.HandleFunc("/repos/k2-fsa/sherpa-onnx/releases/tags/", func(w http.ResponseWriter, r *http.Request) {
		base = srv.URL
		if strings.HasSuffix(r.URL.Path, "/asr-models") {
			fmt.Fprint(w, release(modelAssetName, modelDigest, "/model.tar.bz2"))
			return
		}
		fmt.Fprint(w, release("sherpa-onnx-test-linux-x64-shared-no-tts.tar.bz2", engineDigest, "/engine.tar.bz2"))
	})
	mux.HandleFunc("/engine.tar.bz2", func(w http.ResponseWriter, r *http.Request) { w.Write(engineBytes) })
	mux.HandleFunc("/model.tar.bz2", func(w http.ResponseWriter, r *http.Request) { w.Write(modelBytes) })
	srv = httptest.NewServer(mux)
	base = srv.URL
	t.Cleanup(srv.Close)
	return srv
}

func TestResolveEnginePathsHonorsTargetPlatform(t *testing.T) {
	// The engine binary name follows the TARGET platform, not the host: a Windows
	// engine has ".exe", a Linux one doesn't. Resolution must find each regardless
	// of the OS the test runs on (this is what makes the cross-platform download
	// test pass on Windows CI).
	for _, tc := range []struct {
		name    string
		target  bool
		binName string
	}{
		{"linux target", false, "sherpa-onnx-offline"},
		{"windows target", true, "sherpa-onnx-offline.exe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			binDir := filepath.Join(dir, "bin")
			if err := os.MkdirAll(binDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(binDir, tc.binName), []byte("x"), 0o755); err != nil {
				t.Fatal(err)
			}
			bin, _ := resolveEnginePaths(dir, tc.target)
			if !fileExists(bin) {
				t.Errorf("resolveEnginePaths(target-windows=%v) = %q; not found", tc.target, bin)
			}
			if !strings.HasSuffix(bin, tc.binName) {
				t.Errorf("bin = %q, want suffix %q", bin, tc.binName)
			}
		})
	}
}

func TestEnsureLocalEngineDownloadsAndExtracts(t *testing.T) {
	srv := fakeReleaseServer(t, engineSHA, modelSHA)
	dest := t.TempDir()
	var stages []string
	comp, err := EnsureLocalEngine(context.Background(), DownloadOptions{
		DestRoot:      dest,
		EngineVersion: "test",
		APIBase:       srv.URL,
		platformKey:   "linux-amd64",
		skipPinned:    true,
		Progress:      func(s string) { stages = append(stages, s) },
	})
	if err != nil {
		t.Fatalf("EnsureLocalEngine: %v", err)
	}
	if !fileExists(comp.BinaryPath) || !strings.HasSuffix(comp.BinaryPath, "sherpa-onnx-offline") {
		t.Errorf("binary path wrong: %q", comp.BinaryPath)
	}
	if !fileExists(comp.ServerPath) {
		t.Errorf("server path missing: %q", comp.ServerPath)
	}
	if !fileExists(filepath.Join(comp.ModelPath, "tokens.txt")) {
		t.Errorf("model tokens.txt missing under %q", comp.ModelPath)
	}
	// The extracted binary keeps its exec bit — Unix only; Windows has no
	// executable permission bit, so os.FileMode never reports 0o100 there.
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(comp.BinaryPath); err == nil && info.Mode()&0o100 == 0 {
			t.Error("engine binary should be executable")
		}
	}
	if len(stages) == 0 {
		t.Error("expected progress stages")
	}

	// Idempotent: a second call reuses the extracted engine + model and downloads
	// NOTHING (no progress stages) — the fix for re-downloading an already-present
	// engine whose binary lives in the tarball's flattened subdir.
	var stages2 []string
	comp2, err := EnsureLocalEngine(context.Background(), DownloadOptions{
		DestRoot: dest, EngineVersion: "test", APIBase: srv.URL, platformKey: "linux-amd64", skipPinned: true,
		Progress: func(s string) { stages2 = append(stages2, s) },
	})
	if err != nil || comp2.BinaryPath != comp.BinaryPath {
		t.Errorf("second call should reuse: %v / %q vs %q", err, comp2.BinaryPath, comp.BinaryPath)
	}
	if len(stages2) != 0 {
		t.Errorf("a fully-cached second call must not download/extract anything, got stages: %v", stages2)
	}
}

func TestEnsureLocalEngineRejectsChecksumMismatch(t *testing.T) {
	// Serve the engine with a wrong digest → verification must refuse to extract.
	srv := fakeReleaseServer(t, strings.Repeat("0", 64), modelSHA)
	_, err := EnsureLocalEngine(context.Background(), DownloadOptions{
		DestRoot: t.TempDir(), EngineVersion: "test", APIBase: srv.URL, platformKey: "linux-amd64", skipPinned: true,
	})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected checksum-mismatch error, got %v", err)
	}
}

func TestEnsureLocalEnginePinnedCrossCheckRefusesChangedRelease(t *testing.T) {
	// Default version + a digest that doesn't match the pinned value → refuse,
	// even though the download itself would verify against the API digest.
	srv := fakeReleaseServer(t, engineSHA, modelSHA)
	_, err := EnsureLocalEngine(context.Background(), DownloadOptions{
		DestRoot:      t.TempDir(),
		EngineVersion: DefaultSherpaVersion, // triggers the pinned cross-check
		APIBase:       srv.URL,
		platformKey:   "linux-amd64",
		// skipPinned false: the pinned digest for linux-amd64 != engineSHA fixture.
	})
	if err == nil || !strings.Contains(err.Error(), "pinned known-good") {
		t.Fatalf("expected pinned cross-check refusal, got %v", err)
	}
}

func TestResolveAssetMissingDigestAllowed(t *testing.T) {
	// An older asset with no API digest resolves fine (verification against a
	// pinned value happens later); resolveAsset no longer refuses here.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"assets": []map[string]any{{"name": "x-linux-x64-shared-no-tts.tar.bz2", "browser_download_url": "http://x", "digest": ""}},
		})
	}))
	defer srv.Close()
	asset, err := resolveAsset(context.Background(), http.DefaultClient, srv.URL, "test", "x-", "linux-x64-shared-no-tts.tar.bz2")
	if err != nil {
		t.Fatalf("resolveAsset should tolerate a missing digest, got %v", err)
	}
	if asset.sha256 != "" {
		t.Errorf("expected empty digest, got %q", asset.sha256)
	}
}

func TestUnverifiableDownloadRefused(t *testing.T) {
	// No API digest AND no pinned digest → refuse to download/extract.
	srv := fakeReleaseServer(t, "", "") // both assets served with empty digest
	_, err := EnsureLocalEngine(context.Background(), DownloadOptions{
		DestRoot: t.TempDir(), EngineVersion: "test", APIBase: srv.URL, platformKey: "linux-amd64", skipPinned: true,
	})
	if err == nil || !strings.Contains(err.Error(), "unverifiable") {
		t.Fatalf("expected refusal of an unverifiable download, got %v", err)
	}
}

func TestProgressReaderReportsPercent(t *testing.T) {
	data := make([]byte, 1000)
	var last string
	pr := &progressReader{r: bytesReader(data), total: 1000, label: "Engine", report: func(s string) { last = s }, lastPct: -1}
	buf := make([]byte, 100)
	for {
		if _, err := pr.Read(buf); err != nil {
			break
		}
	}
	if !strings.Contains(last, "Engine") || !strings.Contains(last, "100%") {
		t.Errorf("expected a final 'Engine 100%%' report, got %q", last)
	}
}

func bytesReader(b []byte) *sliceReader { return &sliceReader{b: b} }

type sliceReader struct {
	b   []byte
	pos int
}

func (s *sliceReader) Read(p []byte) (int, error) {
	if s.pos >= len(s.b) {
		return 0, io.EOF
	}
	n := copy(p, s.b[s.pos:])
	s.pos += n
	return n, nil
}

func TestUnsupportedPlatformSetupError(t *testing.T) {
	_, err := EnsureLocalEngine(context.Background(), DownloadOptions{DestRoot: t.TempDir(), platformKey: "plan9-mips"})
	var setupErr *SetupError
	if !errors.As(err, &setupErr) {
		t.Fatalf("want *SetupError for unsupported platform, got %v", err)
	}
}

func stagedTree(t *testing.T, dir, content string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "engine"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPromoteStagedDirReplacesAPreviousInstall(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "engine-dir")
	stagedTree(t, dest, "old")
	stage := stagedTree(t, filepath.Join(root, "stage"), "new")

	if err := promoteStagedDir(stage, dest, "engine"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "engine"))
	if err != nil || string(got) != "new" {
		t.Fatalf("engine = %q, err %v, want %q", got, err, "new")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".previous-") {
			t.Errorf("the set-aside install should be cleaned up, found %s", e.Name())
		}
	}
}

func TestPromoteStagedDirWorksWithNoPreviousInstall(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "engine-dir")
	stage := stagedTree(t, filepath.Join(root, "stage"), "new")

	if err := promoteStagedDir(stage, dest, "engine"); err != nil {
		t.Fatalf("promote: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "engine"))
	if err != nil || string(got) != "new" {
		t.Fatalf("engine = %q, err %v, want %q", got, err, "new")
	}
}

// The old code deleted the previous install before it had anything to put in
// its place, so a failed promotion left the user with no engine at all.
func TestPromoteStagedDirKeepsThePreviousInstallWhenPromotionFails(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "engine-dir")
	stagedTree(t, dest, "old")
	stage := stagedTree(t, filepath.Join(root, "stage"), "new")

	real := renameStagedDir
	calls := 0
	renameStagedDir = func(from, to string) error {
		calls++
		if calls == 1 {
			return errors.New("injected promotion failure")
		}
		return real(from, to)
	}
	t.Cleanup(func() { renameStagedDir = real })

	if err := promoteStagedDir(stage, dest, "engine"); err == nil {
		t.Fatal("a failed promotion must be reported")
	}
	got, err := os.ReadFile(filepath.Join(dest, "engine"))
	if err != nil {
		t.Fatalf("the previous install was not restored: %v", err)
	}
	if string(got) != "old" {
		t.Fatalf("engine = %q, want the previous %q", got, "old")
	}
}

// If the restore fails too, the only remaining copy must survive the cleanup.
func TestPromoteStagedDirKeepsTheSetAsideCopyWhenRestoreAlsoFails(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "engine-dir")
	stagedTree(t, dest, "old")
	stage := stagedTree(t, filepath.Join(root, "stage"), "new")

	real := renameStagedDir
	renameStagedDir = func(string, string) error { return errors.New("injected rename failure") }
	t.Cleanup(func() { renameStagedDir = real })

	err := promoteStagedDir(stage, dest, "engine")
	if err == nil {
		t.Fatal("a failed promotion must be reported")
	}
	if !strings.Contains(err.Error(), "previous install left in") {
		t.Errorf("the error should name the retained copy, got: %v", err)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	found := false
	for _, e := range entries {
		if !strings.Contains(e.Name(), ".previous-") {
			continue
		}
		got, readErr := os.ReadFile(filepath.Join(root, e.Name(), "install", "engine"))
		if readErr == nil && string(got) == "old" {
			found = true
		}
	}
	if !found {
		t.Error("the previous install was deleted along with the holder dir")
	}
}

// A process stop between the two renames in promoteStagedDir leaves destDir
// absent and the only usable install in a .previous-* holder. Nothing else knows
// about that holder, so without a repair the engine is re-downloaded and a host
// that cannot reach the network stays without dictation despite having a copy.
func TestRestoreInterruptedPromotionPutsTheInstallBack(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "engine-1.2.3-linux-x64")
	holder := filepath.Join(root, filepath.Base(dest)+".previous-abc")
	install := filepath.Join(holder, "install")
	if err := os.MkdirAll(install, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install, "engine"), []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}

	restoreInterruptedPromotion(dest)

	got, err := os.ReadFile(filepath.Join(dest, "engine"))
	if err != nil {
		t.Fatalf("the interrupted promotion was not restored: %v", err)
	}
	if string(got) != "kept" {
		t.Fatalf("restored engine = %q, want %q", got, "kept")
	}
	if _, err := os.Stat(holder); !os.IsNotExist(err) {
		t.Errorf("the holder should be cleared after a restore, got %v", err)
	}
}

// A holder is a leftover, never a replacement for whatever is already at destDir,
// empty or not. os.Rename refuses an existing directory either way, so this
// pins the behavior rather than one implementation of it.
func TestRestoreInterruptedPromotionLeavesAnExistingDestAlone(t *testing.T) {
	for _, tc := range []struct{ name, live string }{
		{"empty dest", ""},
		{"populated dest", "live"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dest := filepath.Join(root, "engine-1.2.3-linux-x64")
			if err := os.MkdirAll(dest, 0o755); err != nil {
				t.Fatal(err)
			}
			if tc.live != "" {
				if err := os.WriteFile(filepath.Join(dest, "engine"), []byte(tc.live), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			holder := filepath.Join(root, filepath.Base(dest)+".previous-abc")
			install := filepath.Join(holder, "install")
			if err := os.MkdirAll(install, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(install, "engine"), []byte("stale"), 0o644); err != nil {
				t.Fatal(err)
			}

			restoreInterruptedPromotion(dest)

			got, err := os.ReadFile(filepath.Join(dest, "engine"))
			if tc.live == "" {
				if err == nil {
					t.Fatalf("an existing dest was replaced by a stale holder: engine = %q", got)
				}
			} else if err != nil || string(got) != tc.live {
				t.Fatalf("engine = %q, err %v, want the live %q", got, err, tc.live)
			}
			if _, err := os.Stat(install); err != nil {
				t.Errorf("the holder must be left intact when dest exists: %v", err)
			}
		})
	}
}

// An interrupted promotion of the MODEL leaves it in a holder exactly as it does
// for the engine, and the model side is what an offline user loses if nothing
// puts it back: with no network there is no download to fall back to.
func TestEnsureLocalEngineRestoresAnInterruptedModelPromotion(t *testing.T) {
	srv := fakeReleaseServer(t, engineSHA, modelSHA)
	dest := t.TempDir()
	opts := DownloadOptions{
		DestRoot: dest, EngineVersion: "test", APIBase: srv.URL, platformKey: "linux-amd64", skipPinned: true,
	}
	comp, err := EnsureLocalEngine(context.Background(), opts)
	if err != nil {
		t.Fatalf("seeding the install: %v", err)
	}

	// Stop the world where promoteStagedDir has moved the model aside but has
	// not yet published the staged copy.
	modelDir := filepath.Dir(comp.ModelPath)
	holder := modelDir + ".previous-abc"
	if err := os.MkdirAll(holder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(modelDir, filepath.Join(holder, "install")); err != nil {
		t.Fatal(err)
	}

	offline := offlineAPIBase(t)
	got, err := EnsureLocalEngine(context.Background(), DownloadOptions{
		DestRoot: dest, EngineVersion: "test", APIBase: offline, platformKey: "linux-amd64", skipPinned: true,
	})
	if err != nil {
		t.Fatalf("the model set aside by an interrupted promotion was not restored: %v", err)
	}
	if !fileExists(filepath.Join(got.ModelPath, "tokens.txt")) {
		t.Errorf("model tokens.txt missing under %q", got.ModelPath)
	}
	if _, err := os.Stat(holder); !os.IsNotExist(err) {
		t.Errorf("the holder should be cleared after a restore, got %v", err)
	}
}

// offlineAPIBase returns an API base nothing is listening on, so any attempt to
// resolve a release asset fails instead of silently downloading.
func offlineAPIBase(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NewServeMux())
	url := srv.URL
	srv.Close()
	return url
}

// plantHolder writes an install into a holder named the way promoteStagedDir
// names one, so recovery sees the same shape it does in production.
func plantHolder(t *testing.T, destDir string, stamp int64, content string) string {
	t.Helper()
	holder := fmt.Sprintf("%s%s%020d-%d", destDir, holderSuffix, stamp, stamp)
	install := filepath.Join(holder, "install")
	if err := os.MkdirAll(install, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(install, "engine"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return holder
}

// A cleanup that could not finish leaves an old holder behind; a later
// interrupted promotion adds a second one. Recovery has to put back the newer
// install, and Glob's lexical order is no evidence of which that is.
func TestRestoreInterruptedPromotionPrefersTheNewestHolder(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "engine-1.2.3-linux-x64")
	stale := plantHolder(t, dest, 100, "stale")
	current := plantHolder(t, dest, 200, "current")

	restoreInterruptedPromotion(dest)

	got, err := os.ReadFile(filepath.Join(dest, "engine"))
	if err != nil || string(got) != "current" {
		t.Fatalf("restored engine = %q (err %v), want the newest holder's %q", got, err, "current")
	}
	if _, err := os.Stat(current); !os.IsNotExist(err) {
		t.Errorf("the restored holder should be cleared, got %v", err)
	}
	// The loser is left for a human rather than deleted on a guess.
	if _, err := os.Stat(filepath.Join(stale, "install", "engine")); err != nil {
		t.Errorf("the older holder must be left intact: %v", err)
	}
}

// interruptPromotion drives the REAL promotion into the state a process killed
// between its two renames leaves: destDir absent, the only install in a holder
// promoteStagedDir named. Both renames fail, so nothing is put back in process
// and the holder is retained rather than cleaned up.
func interruptPromotion(t *testing.T, destDir, label string) {
	t.Helper()
	stage := destDir + ".incoming"
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	real := renameStagedDir
	renameStagedDir = func(string, string) error { return errors.New("injected rename failure") }
	err := promoteStagedDir(stage, destDir, label)
	renameStagedDir = real
	if err == nil {
		t.Fatalf("a promotion whose publish and restore both fail must report an error")
	}
	if _, statErr := os.Lstat(destDir); !os.IsNotExist(statErr) {
		t.Fatalf("the interrupted promotion should leave %s absent, got %v", destDir, statErr)
	}
}

// holdersFor lists the holders promoteStagedDir left beside destDir.
func holdersFor(t *testing.T, destDir string) []string {
	t.Helper()
	holders, err := filepath.Glob(destDir + holderSuffix + "*")
	if err != nil {
		t.Fatal(err)
	}
	return holders
}

// End to end over the real transaction, for BOTH consumers of it. The engine and
// the model are installed for real, one of them is interrupted mid-promotion by
// the real promoteStagedDir, and the next start has to put it back with no
// network to fall back on. Nothing plants a holder by hand, so the name recovery
// orders by is the one promoteStagedDir writes -- the half a hand-built fixture
// cannot check.
func TestEnsureLocalEngineRecoversARealInterruptedPromotionOffline(t *testing.T) {
	for _, tc := range []struct {
		name  string
		label string
		// dirFor picks the install this case interrupts, given a finished setup.
		dirFor func(t *testing.T, dest string, comp EngineComponents) string
	}{
		{
			name:  "engine",
			label: "Engine",
			dirFor: func(t *testing.T, dest string, comp EngineComponents) string {
				t.Helper()
				matches, err := filepath.Glob(filepath.Join(dest, "engine-*"))
				if err != nil || len(matches) != 1 {
					t.Fatalf("want exactly one engine dir under %s, got %v (err %v)", dest, matches, err)
				}
				return matches[0]
			},
		},
		{
			name:  "model",
			label: "Model",
			dirFor: func(t *testing.T, dest string, comp EngineComponents) string {
				t.Helper()
				return filepath.Dir(comp.ModelPath)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeReleaseServer(t, engineSHA, modelSHA)
			dest := t.TempDir()
			comp, err := EnsureLocalEngine(context.Background(), DownloadOptions{
				DestRoot: dest, EngineVersion: "test", APIBase: srv.URL, platformKey: "linux-amd64", skipPinned: true,
			})
			if err != nil {
				t.Fatalf("seeding the install: %v", err)
			}

			target := tc.dirFor(t, dest, comp)
			interruptPromotion(t, target, tc.label)
			holders := holdersFor(t, target)
			if len(holders) != 1 {
				t.Fatalf("want exactly one holder beside %s, got %v", target, holders)
			}
			// The name promoteStagedDir wrote must be one recovery can order by.
			// Restoring a lone holder works either way, so without this the two
			// halves could drift apart and only a second holder would show it.
			if _, ok := holderStamp(target, holders[0]); !ok {
				t.Fatalf("recovery cannot order the name promoteStagedDir wrote: %q", holders[0])
			}

			// No network: if the holder is not found there is nothing to fall
			// back on, which is the failure the offline user actually sees.
			got, err := EnsureLocalEngine(context.Background(), DownloadOptions{
				DestRoot: dest, EngineVersion: "test", APIBase: offlineAPIBase(t), platformKey: "linux-amd64", skipPinned: true,
			})
			if err != nil {
				t.Fatalf("the interrupted %s promotion was not recovered offline: %v", tc.name, err)
			}
			if !fileExists(got.BinaryPath) {
				t.Errorf("engine binary missing after recovery: %q", got.BinaryPath)
			}
			if !fileExists(filepath.Join(got.ModelPath, "tokens.txt")) {
				t.Errorf("model tokens.txt missing after recovery under %q", got.ModelPath)
			}
			if holders := holdersFor(t, target); len(holders) != 0 {
				t.Errorf("the holder should be cleared after recovery, got %v", holders)
			}
		})
	}
}

// A path is not a pattern. An install root containing a glob metacharacter -- a
// '[' is the one that silently matches nothing -- must not cost a user the
// install recovery exists to put back.
func TestRestoreInterruptedPromotionFindsHoldersUnderAnAwkwardPath(t *testing.T) {
	dirNames := []string{"plain", "wei[rd", "br]ack"}
	if runtime.GOOS != "windows" {
		// Windows refuses these two in a filename outright, so there is no such
		// path to defend there. The bracket cases above still cover the
		// metacharacter that matches nothing instead of failing.
		dirNames = append(dirNames, "sta*r", "que?ry")
	}
	for _, dirName := range dirNames {
		t.Run(dirName, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), dirName)
			dest := filepath.Join(root, "engine-1.2.3-linux-x64")
			install := filepath.Join(plantHolder(t, dest, 100, "kept"), "install")
			if _, err := os.Stat(install); err != nil {
				t.Fatal(err)
			}

			restoreInterruptedPromotion(dest)

			got, err := os.ReadFile(filepath.Join(dest, "engine"))
			if err != nil || string(got) != "kept" {
				t.Fatalf("the install was not restored under %q: got %q err %v", dirName, got, err)
			}
		})
	}
}

// A holder this package did not name carries no ordering anyone can read, so it
// is the least recent thing recovery can claim to know about and must lose to
// any stamped holder, however old that one's stamp is.
func TestRestoreInterruptedPromotionPrefersAStampedHolderOverAnUnstampedOne(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "engine-1.2.3-linux-x64")
	// The unstamped name sorts after the stamped one lexically, so a pass that
	// ignored the stamp entirely would still get this right; give it a name that
	// sorts FIRST, so only the stamped-wins rule can produce the wanted answer.
	unstamped := dest + holderSuffix + "aaa"
	if err := os.MkdirAll(filepath.Join(unstamped, "install"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unstamped, "install", "engine"), []byte("unstamped"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamped := plantHolder(t, dest, 100, "stamped")

	restoreInterruptedPromotion(dest)

	got, err := os.ReadFile(filepath.Join(dest, "engine"))
	if err != nil || string(got) != "stamped" {
		t.Fatalf("a stamped holder must win over an unstamped one: got %q err %v", got, err)
	}
	if _, err := os.Stat(stamped); !os.IsNotExist(err) {
		t.Errorf("the restored holder should be cleared, got %v", err)
	}
	// The one recovery did not use is left for a human, never deleted on a guess.
	if _, err := os.Stat(filepath.Join(unstamped, "install", "engine")); err != nil {
		t.Errorf("the unused holder must be kept: %v", err)
	}
}

// A holder can be there without an install in it: the promotion creates the
// holder first, so a stop before the rename leaves an empty one. Recovery must
// step over it and keep looking rather than treating it as the newest word on
// what to restore.
func TestRestoreInterruptedPromotionSkipsAHolderWithNoInstall(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "engine-1.2.3-linux-x64")
	usable := plantHolder(t, dest, 100, "kept")
	// Newer, so ordering reaches it first, but it holds nothing.
	empty := fmt.Sprintf("%s%s%020d-x", dest, holderSuffix, 200)
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	restoreInterruptedPromotion(dest)

	got, err := os.ReadFile(filepath.Join(dest, "engine"))
	if err != nil || string(got) != "kept" {
		t.Fatalf("the usable holder should be restored: got %q err %v", got, err)
	}
	if _, err := os.Stat(usable); !os.IsNotExist(err) {
		t.Errorf("the restored holder should be cleared, got %v", err)
	}
}
