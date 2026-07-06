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
