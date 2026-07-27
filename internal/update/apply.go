package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/release"
)

// DefaultDownloadTimeout bounds the archive/checksum download phase of a
// standalone Apply, separately from Options.Timeout (which only covers the
// small release-metadata check), so a stalled connection can't hang forever.
const DefaultDownloadTimeout = 5 * time.Minute

// ApplyResult reports the outcome of Apply.
type ApplyResult struct {
	Result
	Applied       bool          `json:"applied"`
	InstallMethod InstallMethod `json:"installMethod,omitempty"`
	BinaryPath    string        `json:"binaryPath,omitempty"`
	Message       string        `json:"message,omitempty"`
	Warnings      []string      `json:"warnings,omitempty"`
}

// windowsOptionalBinaries/linuxOptionalBinaries mirror the helper binary
// names scripts/postinstall.mjs copies alongside the main binary when
// present, so `zero upgrade` refreshes them too instead of leaving them stale.
var (
	windowsOptionalBinaries = []string{"zero-windows-command-runner.exe", "zero-windows-sandbox-setup.exe"}
	linuxOptionalBinaries   = []string{"zero-linux-sandbox", "zero-seccomp"}
)

// Apply checks for an update and, if one is available, installs it: via
// `npm install -g` for npm-managed installs, or by downloading, verifying,
// and atomically replacing the binary for standalone installs.
func Apply(ctx context.Context, options Options) (ApplyResult, error) {
	checkResult, err := Check(ctx, options)
	if err != nil {
		return ApplyResult{}, err
	}

	executablePath, err := os.Executable()
	if err != nil {
		return ApplyResult{}, fmt.Errorf("resolve current executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executablePath); err == nil {
		executablePath = resolved
	}
	// Best-effort: remove a "<binary>.old" left behind by a previous Windows
	// replaceBinary call now that enough time (a whole separate invocation)
	// has passed for the old process to have released the file. Runs
	// regardless of whether an update is available, so it isn't stuck waiting
	// on a future upgrade that may never come.
	CleanupStaleBinary(executablePath)

	if !checkResult.UpdateAvailable {
		return ApplyResult{Result: checkResult, Message: "already up to date"}, nil
	}

	method := DetectInstallMethod(executablePath)
	switch method {
	case InstallMethodNpm:
		if err := applyNpmUpdate(ctx); err != nil {
			return ApplyResult{}, err
		}
		return ApplyResult{
			Result:        checkResult,
			Applied:       true,
			InstallMethod: method,
			BinaryPath:    executablePath,
			Message:       fmt.Sprintf("updated via npm to %s", checkResult.LatestVersion),
		}, nil
	default:
		warnings, err := applyStandaloneUpdate(ctx, checkResult, executablePath)
		if err != nil {
			return ApplyResult{}, err
		}
		return ApplyResult{
			Result:        checkResult,
			Applied:       true,
			InstallMethod: method,
			BinaryPath:    executablePath,
			Message:       fmt.Sprintf("updated to %s", checkResult.LatestVersion),
			Warnings:      warnings,
		}, nil
	}
}

// FormatApply renders an ApplyResult as human-readable text.
func FormatApply(result ApplyResult) string {
	if !result.Applied {
		return Format(result.Result)
	}
	lines := []string{
		fmt.Sprintf("[zero] %s (%s -> %s)", result.Message, result.CurrentVersion, result.LatestVersion),
		"Binary: " + result.BinaryPath,
	}
	for _, warning := range result.Warnings {
		lines = append(lines, "Warning: "+warning)
	}
	return strings.Join(lines, "\n")
}

func applyNpmUpdate(ctx context.Context) error {
	npmPath, err := exec.LookPath("npm")
	if err != nil {
		return fmt.Errorf("npm not found on PATH: reinstall with `npm install -g %s@latest`", npmPackageName)
	}
	command := exec.CommandContext(ctx, npmPath, "install", "-g", npmPackageName+"@latest")
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("npm install -g %s@latest: %w", npmPackageName, err)
	}
	return nil
}

func applyStandaloneUpdate(ctx context.Context, result Result, executablePath string) ([]string, error) {
	asset := result.ReleaseAsset
	if !asset.Verified {
		return nil, fmt.Errorf("release asset for %s-%s could not be verified", asset.Platform, asset.Arch)
	}

	tempDir, err := os.MkdirTemp("", "zero-update-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	downloadCtx, cancel := context.WithTimeout(ctx, DefaultDownloadTimeout)
	defer cancel()

	archivePath := filepath.Join(tempDir, asset.ArchiveName)
	if err := downloadFile(downloadCtx, asset.ArchiveURL, archivePath); err != nil {
		return nil, fmt.Errorf("download release archive: %w", err)
	}
	checksumPath := filepath.Join(tempDir, asset.ChecksumName)
	if err := downloadFile(downloadCtx, asset.ChecksumURL, checksumPath); err != nil {
		return nil, fmt.Errorf("download release checksum: %w", err)
	}
	if err := verifyArchiveChecksum(checksumPath, asset.ArchiveName); err != nil {
		return nil, err
	}

	extractDir := filepath.Join(tempDir, "extracted")
	if err := os.MkdirAll(extractDir, 0o755); err != nil {
		return nil, err
	}
	if err := extractArchive(archivePath, extractDir); err != nil {
		return nil, fmt.Errorf("extract release archive: %w", err)
	}

	binaryName := "zero"
	optionalBinaries := linuxOptionalBinaries
	switch runtime.GOOS {
	case "windows":
		binaryName = "zero.exe"
		optionalBinaries = windowsOptionalBinaries
	case "darwin":
		optionalBinaries = nil
	}

	newBinaryPath, err := findByBasename(extractDir, binaryName)
	if err != nil {
		return nil, err
	}
	if newBinaryPath == "" {
		return nil, fmt.Errorf("release archive did not contain %s", binaryName)
	}

	targetDir := filepath.Dir(executablePath)
	if err := installBinary(newBinaryPath, executablePath); err != nil {
		return nil, err
	}

	var warnings []string
	for _, name := range optionalBinaries {
		source, err := findByBasename(extractDir, name)
		if err != nil || source == "" {
			continue // optional: the sandbox degrades gracefully without it
		}
		destPath := filepath.Join(targetDir, name)
		if _, err := os.Stat(destPath); err != nil {
			continue // only refresh helpers this install already has
		}
		if err := installBinary(source, destPath); err != nil {
			warnings = append(warnings, fmt.Sprintf("failed to update helper %s: %v", name, err))
		}
	}

	return warnings, nil
}

// verifyArchiveChecksum verifies the checksum file at checksumPath and
// cross-checks it names expectedArchiveName. VerifySHA256Checksum hashes
// whichever file the checksum text names, not necessarily the archive we
// downloaded — this mirrors the cross-check VerifyReleaseChecksums already
// does, so a checksum file that names a different file can't be used to
// verify (and thereby vouch for) the wrong bytes before extraction.
func verifyArchiveChecksum(checksumPath string, expectedArchiveName string) error {
	verified, err := release.VerifySHA256Checksum(checksumPath)
	if err != nil {
		return fmt.Errorf("verify release checksum: %w", err)
	}
	if verified.ArchiveName != expectedArchiveName {
		return fmt.Errorf("checksum file references %s, expected %s", verified.ArchiveName, expectedArchiveName)
	}
	return nil
}

// installBinary stages sourcePath next to targetPath (same directory, so the
// final rename is atomic/same-filesystem) and then swaps it into place.
func installBinary(sourcePath string, targetPath string) error {
	stagedPath := targetPath + ".new"
	if err := copyFile(sourcePath, stagedPath); err != nil {
		return fmt.Errorf("stage %s: %w", filepath.Base(targetPath), err)
	}
	defer func() {
		_ = os.Remove(stagedPath)
	}()
	if err := replaceBinary(targetPath, stagedPath); err != nil {
		return fmt.Errorf("install %s: %w", filepath.Base(targetPath), err)
	}
	return nil
}

func copyFile(sourcePath string, destPath string) (retErr error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = source.Close()
	}()
	dest, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dest.Close(); closeErr != nil && retErr == nil {
			retErr = closeErr
		}
	}()
	_, retErr = io.Copy(dest, source)
	return retErr
}

func downloadFile(ctx context.Context, url string, destPath string) error {
	if url == "" {
		return fmt.Errorf("missing download URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "zero/update")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer func() {
		_ = response.Body.Close()
	}()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download failed (%s): %s", response.Status, url)
	}
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, response.Body)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
