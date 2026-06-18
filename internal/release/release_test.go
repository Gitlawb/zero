package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSHA256FileHashesArchiveBytes(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "zero-v0.1.0-linux-x64.tar.gz")
	archiveBytes := []byte("zero archive bytes")
	if err := os.WriteFile(archivePath, archiveBytes, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	sum := sha256.Sum256(archiveBytes)
	expected := hex.EncodeToString(sum[:])
	got, err := SHA256File(archivePath)
	if err != nil {
		t.Fatalf("SHA256File returned error: %v", err)
	}
	if got != expected {
		t.Fatalf("SHA256File = %q, want %q", got, expected)
	}
}

func TestWriteAndVerifyReleaseChecksums(t *testing.T) {
	dir := t.TempDir()
	archiveName := "zero-v0.1.0-linux-x64.tar.gz"
	archivePath := filepath.Join(dir, archiveName)
	if err := os.WriteFile(archivePath, []byte("zero archive bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	written, err := WriteSHA256Checksum(archivePath)
	if err != nil {
		t.Fatalf("WriteSHA256Checksum returned error: %v", err)
	}
	checksumBytes, err := os.ReadFile(written.ChecksumPath)
	if err != nil {
		t.Fatalf("ReadFile checksum: %v", err)
	}
	if !strings.HasSuffix(string(checksumBytes), "  "+archiveName+"\n") {
		t.Fatalf("checksum text = %q, want shasum-compatible archive name", string(checksumBytes))
	}

	verifiedFile, err := VerifySHA256Checksum(written.ChecksumPath)
	if err != nil {
		t.Fatalf("VerifySHA256Checksum returned error: %v", err)
	}
	if verifiedFile.ArchiveName != archiveName || verifiedFile.ExpectedChecksum != verifiedFile.ActualChecksum {
		t.Fatalf("verified checksum = %#v", verifiedFile)
	}

	verifiedRelease, err := VerifyReleaseChecksums(VerifyOptions{ReleaseDir: dir})
	if err != nil {
		t.Fatalf("VerifyReleaseChecksums returned error: %v", err)
	}
	if len(verifiedRelease) != 1 || verifiedRelease[0].ArchiveName != archiveName {
		t.Fatalf("verified release = %#v, want %s", verifiedRelease, archiveName)
	}
}

func TestChecksumParsingRejectsMalformedAndUnsafeNames(t *testing.T) {
	if _, err := ParseSHA256Checksum("not a checksum"); err == nil || !strings.Contains(err.Error(), "checksum file must contain") {
		t.Fatalf("ParseSHA256Checksum malformed error = %v", err)
	}
	if _, err := FormatSHA256Checksum("abc", "zero.tar.gz"); err == nil || !strings.Contains(err.Error(), "64 hexadecimal") {
		t.Fatalf("FormatSHA256Checksum invalid checksum error = %v", err)
	}
	if _, err := ParseSHA256Checksum(strings.Repeat("a", 64) + "  ../zero.tar.gz\n"); err == nil || !strings.Contains(err.Error(), "same-directory") {
		t.Fatalf("ParseSHA256Checksum unsafe path error = %v", err)
	}
	if _, err := ParseSHA256Checksum(strings.Repeat("a", 64) + "  zero.tar.gz\n" + strings.Repeat("b", 64) + "  other.tar.gz\n"); err == nil || !strings.Contains(err.Error(), "exactly one checksum line") {
		t.Fatalf("ParseSHA256Checksum multi-line error = %v", err)
	}
}

func TestVerifyChecksumDetectsArchiveChanges(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "zero-v0.1.0-linux-x64.tar.gz")
	if err := os.WriteFile(archivePath, []byte("original bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	written, err := WriteSHA256Checksum(archivePath)
	if err != nil {
		t.Fatalf("WriteSHA256Checksum returned error: %v", err)
	}
	if err := os.WriteFile(archivePath, []byte("changed bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile changed archive: %v", err)
	}

	_, err = VerifySHA256Checksum(written.ChecksumPath)
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("VerifySHA256Checksum error = %v, want mismatch", err)
	}
}

func TestVerifyReleaseChecksumsRequiresMatchingFiles(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "zero-v0.1.0-linux-x64.tar.gz")
	if err := os.WriteFile(archivePath, []byte("archive bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := VerifyReleaseChecksums(VerifyOptions{ReleaseDir: dir})
	if err == nil || !strings.Contains(err.Error(), "missing checksum file") {
		t.Fatalf("VerifyReleaseChecksums error = %v, want missing checksum", err)
	}

	if _, err := WriteSHA256Checksum(archivePath); err != nil {
		t.Fatalf("WriteSHA256Checksum returned error: %v", err)
	}
	strayChecksum := filepath.Join(dir, "zero-v0.1.0-macos-arm64.tar.gz.sha256")
	if err := os.WriteFile(strayChecksum, []byte(strings.Repeat("a", 64)+"  zero-v0.1.0-macos-arm64.tar.gz\n"), 0o644); err != nil {
		t.Fatalf("WriteFile stray checksum: %v", err)
	}

	_, err = VerifyReleaseChecksums(VerifyOptions{ReleaseDir: dir})
	if err == nil || !strings.Contains(err.Error(), "unexpected checksum file") {
		t.Fatalf("VerifyReleaseChecksums error = %v, want unexpected checksum", err)
	}
}

func TestVerifyReleaseChecksumsRejectsEmptyReleaseDir(t *testing.T) {
	_, err := VerifyReleaseChecksums(VerifyOptions{ReleaseDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "no release archives found") {
		t.Fatalf("VerifyReleaseChecksums error = %v, want no archives", err)
	}
}

func TestReleaseArchiveNamesMatchInstallerContracts(t *testing.T) {
	tests := []struct {
		name        string
		version     string
		goos        string
		goarch      string
		packageName string
		archiveName string
	}{
		{
			name:        "linux amd64",
			version:     "0.1.0",
			goos:        "linux",
			goarch:      "amd64",
			packageName: "zero-v0.1.0-linux-x64",
			archiveName: "zero-v0.1.0-linux-x64.tar.gz",
		},
		{
			name:        "macos arm64",
			version:     "0.1.0",
			goos:        "darwin",
			goarch:      "arm64",
			packageName: "zero-v0.1.0-macos-arm64",
			archiveName: "zero-v0.1.0-macos-arm64.tar.gz",
		},
		{
			name:        "windows amd64",
			version:     "0.1.0",
			goos:        "windows",
			goarch:      "amd64",
			packageName: "zero-v0.1.0-windows-x64",
			archiveName: "zero-v0.1.0-windows-x64.zip",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packageName, err := ReleasePackageName(tt.version, tt.goos, tt.goarch)
			if err != nil {
				t.Fatalf("ReleasePackageName returned error: %v", err)
			}
			archiveName, err := ReleaseArchiveName(tt.version, tt.goos, tt.goarch)
			if err != nil {
				t.Fatalf("ReleaseArchiveName returned error: %v", err)
			}
			if packageName != tt.packageName || archiveName != tt.archiveName {
				t.Fatalf("package/archive = %q/%q, want %q/%q", packageName, archiveName, tt.packageName, tt.archiveName)
			}
		})
	}
}

func TestBuildHelpersMatchScriptContracts(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "package.json"), `{"version":"0.1.0"}`)

	version, err := PackageVersion(root)
	if err != nil {
		t.Fatalf("PackageVersion returned error: %v", err)
	}
	if version != "0.1.0" {
		t.Fatalf("PackageVersion = %q, want 0.1.0", version)
	}
	if got := DefaultBuildOutput(root, "windows"); got != filepath.Join(root, "zero.exe") {
		t.Fatalf("DefaultBuildOutput(windows) = %q", got)
	}
	if got := DefaultBuildOutput(root, "linux"); got != filepath.Join(root, "zero") {
		t.Fatalf("DefaultBuildOutput(linux) = %q", got)
	}
	if got := WindowsSandboxCommandRunnerArtifactName("windows"); got != "zero-windows-command-runner.exe" {
		t.Fatalf("WindowsSandboxCommandRunnerArtifactName(windows) = %q", got)
	}
	if got := WindowsSandboxSetupArtifactName("windows"); got != "zero-windows-sandbox-setup.exe" {
		t.Fatalf("WindowsSandboxSetupArtifactName(windows) = %q", got)
	}
	if got := BuildLdflags(version); !strings.Contains(got, "-X github.com/Gitlawb/zero/internal/cli.version=0.1.0") {
		t.Fatalf("BuildLdflags = %q", got)
	}
}

func TestSmokeRejectsMissingDefaultArtifact(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "package.json"), `{"version":"0.1.0"}`)

	_, err := Smoke(context.Background(), SmokeOptions{RootDir: root, GOOS: "linux"})
	if err == nil || !strings.Contains(err.Error(), "build artifact not found: zero") {
		t.Fatalf("Smoke error = %v, want missing artifact", err)
	}
}

func TestPackageRejectsCrossTargetBecauseItSmokesTheBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"version":"0.1.0"}`), 0o644); err != nil {
		t.Fatalf("WriteFile package.json: %v", err)
	}
	goos := "linux"
	if runtime.GOOS == "linux" {
		goos = "darwin"
	}
	_, err := Package(context.Background(), PackageOptions{
		RootDir: root,
		GOOS:    goos,
		GOARCH:  runtime.GOARCH,
	})
	if err == nil || !strings.Contains(err.Error(), "target must match host platform") {
		t.Fatalf("Package error = %v, want host-target mismatch", err)
	}
}

func TestResolvePackageDirsRejectsDangerousDeleteTargets(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	tests := []struct {
		name       string
		releaseDir string
		stagingDir string
		want       string
	}{
		{
			name:       "repo root release dir",
			releaseDir: ".",
			stagingDir: "dist/package",
			want:       "inside",
		},
		{
			name:       "dist root release dir",
			releaseDir: "dist",
			stagingDir: "dist/package",
			want:       "inside",
		},
		{
			name:       "outside absolute release dir",
			releaseDir: home,
			stagingDir: "dist/package",
			want:       "inside",
		},
		{
			name:       "same release and staging dir",
			releaseDir: "dist/release",
			stagingDir: "dist/release",
			want:       "overlap",
		},
		{
			name:       "staging contains release dir",
			releaseDir: "dist/package/release",
			stagingDir: "dist/package",
			want:       "overlap",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := resolvePackageDirs(root, tt.releaseDir, tt.stagingDir)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("resolvePackageDirs error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPackageRejectsDangerousDirsBeforeDeleting(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "package.json"), `{"version":"0.1.0"}`)
	markerPath := filepath.Join(root, "DO_NOT_DELETE")
	mustWriteFile(t, markerPath, "marker")

	_, err := Package(context.Background(), PackageOptions{
		RootDir:    root,
		ReleaseDir: ".",
	})
	if err == nil || !strings.Contains(err.Error(), "inside") {
		t.Fatalf("Package error = %v, want unsafe path rejection", err)
	}
	if _, statErr := os.Stat(markerPath); statErr != nil {
		t.Fatalf("Package removed marker before rejecting unsafe dir: %v", statErr)
	}
}

func TestResolvePackageDirsAcceptsDistSubdirs(t *testing.T) {
	root := t.TempDir()
	releaseDir, stagingDir, err := resolvePackageDirs(root, "dist/custom-release", "dist/custom-package")
	if err != nil {
		t.Fatalf("resolvePackageDirs returned error: %v", err)
	}
	if releaseDir != filepath.Join(root, "dist", "custom-release") || stagingDir != filepath.Join(root, "dist", "custom-package") {
		t.Fatalf("release/staging dirs = %q/%q", releaseDir, stagingDir)
	}
}

func TestCreateArchivesWithRootPackageFiles(t *testing.T) {
	t.Run("tar gz", func(t *testing.T) {
		stagingDir := packageStagingFixture(t, "zero")
		archivePath := filepath.Join(t.TempDir(), "zero-v0.1.0-linux-x64.tar.gz")
		if err := createArchive(stagingDir, archivePath, "linux"); err != nil {
			t.Fatalf("createArchive returned error: %v", err)
		}
		names := tarArchiveNames(t, archivePath)
		for _, want := range []string{"zero", "README.md", "bin/zero.js", "VERSION"} {
			if !names[want] {
				t.Fatalf("tar archive missing %s: %#v", want, names)
			}
		}
	})

	t.Run("zip", func(t *testing.T) {
		stagingDir := packageStagingFixture(t, "zero.exe")
		archivePath := filepath.Join(t.TempDir(), "zero-v0.1.0-windows-x64.zip")
		if err := createArchive(stagingDir, archivePath, "windows"); err != nil {
			t.Fatalf("createArchive returned error: %v", err)
		}
		names := zipArchiveNames(t, archivePath)
		for _, want := range []string{"zero.exe", "README.md", "bin/zero.js", "VERSION"} {
			if !names[want] {
				t.Fatalf("zip archive missing %s: %#v", want, names)
			}
		}
	})
}

func TestCopyPackageFilesStagesLinuxSandboxHelper(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	artifact := filepath.Join(root, "zero")
	helper := filepath.Join(root, "zero-linux-sandbox")
	seccomp := filepath.Join(root, "zero-seccomp")
	for path, content := range map[string]string{
		artifact:                              "zero",
		helper:                                "helper",
		seccomp:                               "seccomp",
		filepath.Join(root, "README.md"):      "readme",
		filepath.Join(root, "package.json"):   `{"version":"0.1.0"}`,
		filepath.Join(root, "bin", "zero.js"): "wrapper",
	} {
		mustWriteFile(t, path, content)
	}
	if err := copyPackageFiles(root, staging, artifact, filepath.Join(staging, "zero"), "linux", "0.1.0", map[string]string{
		"zero-linux-sandbox": helper,
		"zero-seccomp":       seccomp,
	}); err != nil {
		t.Fatalf("copyPackageFiles: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "zero-linux-sandbox")); err != nil {
		t.Fatalf("staged helper missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "zero-seccomp")); err != nil {
		t.Fatalf("staged seccomp compatibility helper missing: %v", err)
	}
}

func TestCopyPackageFilesStagesWindowsSandboxHelpers(t *testing.T) {
	root := t.TempDir()
	staging := t.TempDir()
	artifact := filepath.Join(root, "zero.exe")
	runner := filepath.Join(root, "zero-windows-command-runner.exe")
	setup := filepath.Join(root, "zero-windows-sandbox-setup.exe")
	for path, content := range map[string]string{
		artifact:                              "zero",
		runner:                                "runner",
		setup:                                 "setup",
		filepath.Join(root, "README.md"):      "readme",
		filepath.Join(root, "package.json"):   `{"version":"0.1.0"}`,
		filepath.Join(root, "bin", "zero.js"): "wrapper",
	} {
		mustWriteFile(t, path, content)
	}
	if err := copyPackageFiles(root, staging, artifact, filepath.Join(staging, "zero.exe"), "windows", "0.1.0", map[string]string{
		"zero-windows-command-runner.exe": runner,
		"zero-windows-sandbox-setup.exe":  setup,
	}); err != nil {
		t.Fatalf("copyPackageFiles: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "zero-windows-command-runner.exe")); err != nil {
		t.Fatalf("staged runner missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(staging, "zero-windows-sandbox-setup.exe")); err != nil {
		t.Fatalf("staged setup helper missing: %v", err)
	}
}

func packageStagingFixture(t *testing.T, binaryName string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		binaryName:    "binary",
		"README.md":   "readme",
		"bin/zero.js": "wrapper",
		"VERSION":     "0.1.0\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%s): %v", name, err)
		}
	}
	return dir
}

func mustWriteFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func tarArchiveNames(t *testing.T, archivePath string) map[string]bool {
	t.Helper()
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatalf("Open tar archive: %v", err)
	}
	defer func() {
		_ = file.Close()
	}()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("NewReader gzip: %v", err)
	}
	defer func() {
		_ = gzipReader.Close()
	}()
	reader := tar.NewReader(gzipReader)
	names := map[string]bool{}
	for {
		header, err := reader.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("tar Next: %v", err)
		}
		names[header.Name] = true
	}
	return names
}

func zipArchiveNames(t *testing.T, archivePath string) map[string]bool {
	t.Helper()
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("OpenReader zip: %v", err)
	}
	defer func() {
		_ = reader.Close()
	}()
	names := map[string]bool{}
	for _, file := range reader.File {
		names[file.Name] = true
	}
	return names
}
