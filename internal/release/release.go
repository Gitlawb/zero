package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

type WrittenChecksum struct {
	ArchivePath  string
	ChecksumPath string
	ArchiveName  string
	Checksum     string
}

type VerifiedChecksum struct {
	WrittenChecksum
	ExpectedChecksum string
	ActualChecksum   string
}

type ParsedChecksum struct {
	Checksum string
	FileName string
}

type VerifyOptions struct {
	ReleaseDir string
}

type PackageOptions struct {
	RootDir     string
	ReleaseDir  string
	StagingRoot string
	Version     string
	GOOS        string
	GOARCH      string
}

type BuildOptions struct {
	RootDir string
	Output  string
	Version string
	GOOS    string
	GOARCH  string
}

type BuildResult struct {
	OutputPath string
	Version    string
	GOOS       string
	GOARCH     string
}

type SmokeOptions struct {
	RootDir    string
	BinaryPath string
	Version    string
	GOOS       string
}

type SmokeResult struct {
	BinaryPath string
	Version    string
}

type PackageResult struct {
	PackageName string
	ArchiveName string
	ArchivePath string
	Checksum    WrittenChecksum
	Version     string
	GOOS        string
	GOARCH      string
}

var checksumPattern = regexp.MustCompile(`^([a-fA-F0-9]{64})\s+(.+)$`)

func Build(ctx context.Context, options BuildOptions) (BuildResult, error) {
	rootDir, err := resolveRootDir(options.RootDir)
	if err != nil {
		return BuildResult{}, err
	}
	version := strings.TrimSpace(options.Version)
	if version == "" {
		version, err = PackageVersion(rootDir)
		if err != nil {
			return BuildResult{}, err
		}
	}
	goos := strings.TrimSpace(options.GOOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := strings.TrimSpace(options.GOARCH)
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	output := strings.TrimSpace(options.Output)
	if output == "" {
		output = DefaultBuildOutput(rootDir, goos)
	}
	if err := buildZero(ctx, rootDir, output, version, goos, goarch); err != nil {
		return BuildResult{}, err
	}
	return BuildResult{
		OutputPath: output,
		Version:    version,
		GOOS:       goos,
		GOARCH:     goarch,
	}, nil
}

func Smoke(ctx context.Context, options SmokeOptions) (SmokeResult, error) {
	rootDir, err := resolveRootDir(options.RootDir)
	if err != nil {
		return SmokeResult{}, err
	}
	version := strings.TrimSpace(options.Version)
	if version == "" {
		version, err = PackageVersion(rootDir)
		if err != nil {
			return SmokeResult{}, err
		}
	}
	goos := strings.TrimSpace(options.GOOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	binaryPath := strings.TrimSpace(options.BinaryPath)
	if binaryPath == "" {
		binaryPath = DefaultBuildOutput(rootDir, goos)
	} else if !filepath.IsAbs(binaryPath) {
		binaryPath = filepath.Join(rootDir, binaryPath)
	}
	if _, err := os.Stat(binaryPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SmokeResult{}, fmt.Errorf("build artifact not found: %s", filepath.Base(binaryPath))
		}
		return SmokeResult{}, err
	}
	if err := smokeVersion(ctx, binaryPath, version); err != nil {
		return SmokeResult{}, err
	}
	return SmokeResult{
		BinaryPath: binaryPath,
		Version:    version,
	}, nil
}

func Package(ctx context.Context, options PackageOptions) (PackageResult, error) {
	rootDir, err := resolveRootDir(options.RootDir)
	if err != nil {
		return PackageResult{}, err
	}
	version := strings.TrimSpace(options.Version)
	if version == "" {
		version, err = PackageVersion(rootDir)
		if err != nil {
			return PackageResult{}, err
		}
	}
	goos := strings.TrimSpace(options.GOOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := strings.TrimSpace(options.GOARCH)
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	if goos != runtime.GOOS || goarch != runtime.GOARCH {
		return PackageResult{}, fmt.Errorf("release packaging target must match host platform for smoke verification: host %s/%s, target %s/%s", runtime.GOOS, runtime.GOARCH, goos, goarch)
	}
	packageName, err := ReleasePackageName(version, goos, goarch)
	if err != nil {
		return PackageResult{}, err
	}
	archiveName, err := ReleaseArchiveName(version, goos, goarch)
	if err != nil {
		return PackageResult{}, err
	}
	releaseDir, stagingRoot, err := resolvePackageDirs(rootDir, options.ReleaseDir, options.StagingRoot)
	if err != nil {
		return PackageResult{}, err
	}
	stagingDir := filepath.Join(stagingRoot, packageName)
	archivePath := filepath.Join(releaseDir, archiveName)
	artifactPath := filepath.Join(rootDir, ZeroArtifactName(goos))
	stagedBinaryPath := filepath.Join(stagingDir, ZeroArtifactName(goos))
	helperArtifacts := map[string]string{}
	if goos == "linux" {
		helperPath := filepath.Join(rootDir, LinuxSandboxHelperArtifactName(goos))
		helperArtifacts[LinuxSandboxHelperArtifactName(goos)] = helperPath
		seccompPath := filepath.Join(rootDir, LinuxSeccompHelperArtifactName(goos))
		helperArtifacts[LinuxSeccompHelperArtifactName(goos)] = seccompPath
	}
	if goos == "windows" {
		runnerPath := filepath.Join(rootDir, WindowsSandboxCommandRunnerArtifactName(goos))
		helperArtifacts[WindowsSandboxCommandRunnerArtifactName(goos)] = runnerPath
		setupPath := filepath.Join(rootDir, WindowsSandboxSetupArtifactName(goos))
		helperArtifacts[WindowsSandboxSetupArtifactName(goos)] = setupPath
	}

	if err := os.RemoveAll(releaseDir); err != nil {
		return PackageResult{}, fmt.Errorf("clean release dir: %w", err)
	}
	if err := os.RemoveAll(stagingRoot); err != nil {
		return PackageResult{}, fmt.Errorf("clean package staging dir: %w", err)
	}
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return PackageResult{}, fmt.Errorf("create package staging dir: %w", err)
	}
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return PackageResult{}, fmt.Errorf("create release dir: %w", err)
	}

	if err := buildZero(ctx, rootDir, artifactPath, version, goos, goarch); err != nil {
		return PackageResult{}, err
	}
	for name, path := range helperArtifacts {
		if err := buildReleaseHelper(ctx, rootDir, path, goos, goarch, name); err != nil {
			return PackageResult{}, fmt.Errorf("build %s: %w", name, err)
		}
	}
	if err := smokeVersion(ctx, artifactPath, version); err != nil {
		return PackageResult{}, err
	}
	if err := copyPackageFiles(rootDir, stagingDir, artifactPath, stagedBinaryPath, goos, version, helperArtifacts); err != nil {
		return PackageResult{}, err
	}
	if err := createArchive(stagingDir, archivePath, goos); err != nil {
		return PackageResult{}, err
	}
	checksum, err := WriteSHA256Checksum(archivePath)
	if err != nil {
		return PackageResult{}, err
	}
	return PackageResult{
		PackageName: packageName,
		ArchiveName: archiveName,
		ArchivePath: archivePath,
		Checksum:    checksum,
		Version:     version,
		GOOS:        goos,
		GOARCH:      goarch,
	}, nil
}

func ZeroArtifactName(goos string) string {
	if goos == "windows" {
		return "zero.exe"
	}
	return "zero"
}

func LinuxSandboxHelperArtifactName(goos string) string {
	if goos == "windows" {
		return "zero-linux-sandbox.exe"
	}
	return "zero-linux-sandbox"
}

func LinuxSeccompHelperArtifactName(goos string) string {
	if goos == "windows" {
		return "zero-seccomp.exe"
	}
	return "zero-seccomp"
}

func WindowsSandboxCommandRunnerArtifactName(goos string) string {
	if goos == "windows" {
		return "zero-windows-command-runner.exe"
	}
	return "zero-windows-command-runner"
}

func WindowsSandboxSetupArtifactName(goos string) string {
	if goos == "windows" {
		return "zero-windows-sandbox-setup.exe"
	}
	return "zero-windows-sandbox-setup"
}

func DefaultBuildOutput(rootDir string, goos string) string {
	return filepath.Join(rootDir, ZeroArtifactName(goos))
}

func BuildLdflags(version string) string {
	return "-s -w -X github.com/Gitlawb/zero/internal/cli.version=" + version
}

func ReleasePlatform(goos string) (string, error) {
	switch strings.TrimSpace(goos) {
	case "linux":
		return "linux", nil
	case "darwin":
		return "macos", nil
	case "windows":
		return "windows", nil
	default:
		return "", fmt.Errorf("unsupported release platform: %s", goos)
	}
}

func ReleaseArch(goarch string) (string, error) {
	switch strings.TrimSpace(goarch) {
	case "amd64":
		return "x64", nil
	case "arm64":
		return "arm64", nil
	default:
		return "", fmt.Errorf("unsupported release architecture: %s", goarch)
	}
}

func ReleaseArchiveExtension(goos string) string {
	if goos == "windows" {
		return "zip"
	}
	return "tar.gz"
}

func ReleasePackageName(version string, goos string, goarch string) (string, error) {
	platform, err := ReleasePlatform(goos)
	if err != nil {
		return "", err
	}
	arch, err := ReleaseArch(goarch)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("zero-v%s-%s-%s", version, platform, arch), nil
}

func ReleaseArchiveName(version string, goos string, goarch string) (string, error) {
	packageName, err := ReleasePackageName(version, goos, goarch)
	if err != nil {
		return "", err
	}
	return packageName + "." + ReleaseArchiveExtension(goos), nil
}

func SHA256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ParseSHA256Checksum(text string) (ParsedChecksum, error) {
	lines := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return ParsedChecksum{}, errors.New("checksum file is empty")
	}
	if len(lines) > 1 {
		return ParsedChecksum{}, errors.New("checksum file must contain exactly one checksum line")
	}
	match := checksumPattern.FindStringSubmatch(lines[0])
	if match == nil {
		return ParsedChecksum{}, errors.New(`checksum file must contain "<sha256>  <archive-name>"`)
	}
	checksum := strings.ToLower(match[1])
	fileName := strings.TrimSpace(match[2])
	if err := assertSafeChecksumFileName(fileName); err != nil {
		return ParsedChecksum{}, err
	}
	return ParsedChecksum{Checksum: checksum, FileName: fileName}, nil
}

func FormatSHA256Checksum(checksum string, fileName string) (string, error) {
	if !regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(checksum) {
		return "", errors.New("SHA-256 checksum must be 64 hexadecimal characters")
	}
	if err := assertSafeChecksumFileName(fileName); err != nil {
		return "", err
	}
	return strings.ToLower(checksum) + "  " + fileName + "\n", nil
}

func WriteSHA256Checksum(archivePath string) (WrittenChecksum, error) {
	archiveName := filepath.Base(archivePath)
	checksum, err := SHA256File(archivePath)
	if err != nil {
		return WrittenChecksum{}, err
	}
	text, err := FormatSHA256Checksum(checksum, archiveName)
	if err != nil {
		return WrittenChecksum{}, err
	}
	checksumPath := archivePath + ".sha256"
	if err := os.WriteFile(checksumPath, []byte(text), 0o644); err != nil {
		return WrittenChecksum{}, err
	}
	return WrittenChecksum{
		ArchivePath:  archivePath,
		ChecksumPath: checksumPath,
		ArchiveName:  archiveName,
		Checksum:     checksum,
	}, nil
}

func VerifySHA256Checksum(checksumPath string) (VerifiedChecksum, error) {
	bytes, err := os.ReadFile(checksumPath)
	if err != nil {
		return VerifiedChecksum{}, err
	}
	parsed, err := ParseSHA256Checksum(string(bytes))
	if err != nil {
		return VerifiedChecksum{}, err
	}
	archivePath := filepath.Join(filepath.Dir(checksumPath), parsed.FileName)
	if _, err := os.Stat(archivePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return VerifiedChecksum{}, fmt.Errorf("archive referenced by checksum does not exist: %s", parsed.FileName)
		}
		return VerifiedChecksum{}, err
	}
	actualChecksum, err := SHA256File(archivePath)
	if err != nil {
		return VerifiedChecksum{}, err
	}
	if actualChecksum != parsed.Checksum {
		return VerifiedChecksum{}, fmt.Errorf("checksum mismatch for %s: expected %s, got %s", parsed.FileName, parsed.Checksum, actualChecksum)
	}
	return VerifiedChecksum{
		WrittenChecksum: WrittenChecksum{
			ArchivePath:  archivePath,
			ChecksumPath: checksumPath,
			ArchiveName:  parsed.FileName,
			Checksum:     parsed.Checksum,
		},
		ExpectedChecksum: parsed.Checksum,
		ActualChecksum:   actualChecksum,
	}, nil
}

func VerifyReleaseChecksums(options VerifyOptions) ([]VerifiedChecksum, error) {
	releaseDir := strings.TrimSpace(options.ReleaseDir)
	if releaseDir == "" {
		releaseDir = filepath.Join("dist", "release")
	}
	entries, err := os.ReadDir(releaseDir)
	if err != nil {
		return nil, err
	}
	files := []string{}
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	archiveNames := []string{}
	checksumNames := []string{}
	for _, name := range files {
		if strings.HasSuffix(name, ".sha256") {
			checksumNames = append(checksumNames, name)
		} else {
			archiveNames = append(archiveNames, name)
		}
	}
	if len(archiveNames) == 0 {
		return nil, fmt.Errorf("no release archives found in %s", releaseDir)
	}
	expectedChecksumNames := map[string]bool{}
	for _, archiveName := range archiveNames {
		expectedChecksumNames[archiveName+".sha256"] = true
	}
	for _, checksumName := range checksumNames {
		if !expectedChecksumNames[checksumName] {
			return nil, fmt.Errorf("unexpected checksum file without matching archive: %s", checksumName)
		}
	}
	checksumSet := map[string]bool{}
	for _, checksumName := range checksumNames {
		checksumSet[checksumName] = true
	}
	verified := []VerifiedChecksum{}
	for _, archiveName := range archiveNames {
		checksumName := archiveName + ".sha256"
		if !checksumSet[checksumName] {
			return nil, fmt.Errorf("missing checksum file: %s", checksumName)
		}
		result, err := VerifySHA256Checksum(filepath.Join(releaseDir, checksumName))
		if err != nil {
			return nil, err
		}
		if result.ArchiveName != archiveName {
			return nil, fmt.Errorf("checksum file %s references %s, expected %s", checksumName, result.ArchiveName, archiveName)
		}
		verified = append(verified, result)
	}
	return verified, nil
}

func resolveRootDir(rootDir string) (string, error) {
	rootDir = strings.TrimSpace(rootDir)
	if rootDir == "" {
		var err error
		rootDir, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	return filepath.Abs(rootDir)
}

func resolvePackageDirs(rootDir string, releaseDir string, stagingRoot string) (string, string, error) {
	distDir := filepath.Join(rootDir, "dist")
	resolvedReleaseDir, err := resolvePackageSubdir(rootDir, distDir, releaseDir, "release")
	if err != nil {
		return "", "", err
	}
	resolvedStagingRoot, err := resolvePackageSubdir(rootDir, distDir, stagingRoot, "package")
	if err != nil {
		return "", "", err
	}
	if pathsOverlap(resolvedReleaseDir, resolvedStagingRoot) {
		return "", "", fmt.Errorf("release dir and staging dir must not overlap: %s and %s", resolvedReleaseDir, resolvedStagingRoot)
	}
	return resolvedReleaseDir, resolvedStagingRoot, nil
}

func resolvePackageSubdir(rootDir string, distDir string, value string, defaultName string) (string, error) {
	path := strings.TrimSpace(value)
	if path == "" {
		path = filepath.Join(distDir, defaultName)
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(rootDir, path)
	}
	path = filepath.Clean(path)
	if !isStrictSubpath(distDir, path) {
		return "", fmt.Errorf("release tooling output path must be inside %s: %s", distDir, path)
	}
	return path, nil
}

func isStrictSubpath(parent string, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	relative, err := filepath.Rel(parent, child)
	if err != nil || relative == "." || relative == "" {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func pathsOverlap(left string, right string) bool {
	return left == right || isStrictSubpath(left, right) || isStrictSubpath(right, left)
}

func PackageVersion(rootDir string) (string, error) {
	bytes, err := os.ReadFile(filepath.Join(rootDir, "package.json"))
	if err != nil {
		return "", err
	}
	var payload struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(bytes, &payload); err != nil {
		return "", fmt.Errorf("parse package.json: %w", err)
	}
	if strings.TrimSpace(payload.Version) == "" {
		return "", errors.New("package.json must contain a non-empty string version")
	}
	return strings.TrimSpace(payload.Version), nil
}

func buildZero(ctx context.Context, rootDir string, output string, version string, goos string, goarch string) error {
	return buildGoPackage(ctx, rootDir, output, version, goos, goarch, "./cmd/zero")
}

func buildLinuxSandboxHelper(ctx context.Context, rootDir string, output string, goos string, goarch string) error {
	if goos != "linux" {
		return nil
	}
	return buildGoPackage(ctx, rootDir, output, "", goos, goarch, "./cmd/zero-linux-sandbox")
}

func buildLinuxSeccompHelper(ctx context.Context, rootDir string, output string, goos string, goarch string) error {
	if goos != "linux" {
		return nil
	}
	return buildGoPackage(ctx, rootDir, output, "", goos, goarch, "./cmd/zero-seccomp")
}

func buildWindowsSandboxCommandRunner(ctx context.Context, rootDir string, output string, goos string, goarch string) error {
	if goos != "windows" {
		return nil
	}
	return buildGoPackage(ctx, rootDir, output, "", goos, goarch, "./cmd/zero-windows-command-runner")
}

func buildWindowsSandboxSetup(ctx context.Context, rootDir string, output string, goos string, goarch string) error {
	if goos != "windows" {
		return nil
	}
	return buildGoPackage(ctx, rootDir, output, "", goos, goarch, "./cmd/zero-windows-sandbox-setup")
}

func buildReleaseHelper(ctx context.Context, rootDir string, output string, goos string, goarch string, name string) error {
	switch name {
	case LinuxSandboxHelperArtifactName(goos):
		return buildLinuxSandboxHelper(ctx, rootDir, output, goos, goarch)
	case LinuxSeccompHelperArtifactName(goos):
		return buildLinuxSeccompHelper(ctx, rootDir, output, goos, goarch)
	case WindowsSandboxCommandRunnerArtifactName(goos):
		return buildWindowsSandboxCommandRunner(ctx, rootDir, output, goos, goarch)
	case WindowsSandboxSetupArtifactName(goos):
		return buildWindowsSandboxSetup(ctx, rootDir, output, goos, goarch)
	default:
		return fmt.Errorf("unknown release helper %s", name)
	}
}

func buildGoPackage(ctx context.Context, rootDir string, output string, version string, goos string, goarch string, pkg string) error {
	args := []string{"build", "-trimpath"}
	if version != "" {
		args = append(args, "-ldflags", BuildLdflags(version))
	}
	args = append(args, "-o", output, pkg)
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = rootDir
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
	outputBytes, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(outputBytes))
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("build release binary %s: %s", filepath.Base(output), message)
	}
	return nil
}

func smokeVersion(ctx context.Context, binaryPath string, version string) error {
	command := exec.CommandContext(ctx, binaryPath, "--version")
	outputBytes, err := command.CombinedOutput()
	output := strings.TrimSpace(string(outputBytes))
	if err != nil {
		if output == "" {
			output = err.Error()
		}
		return fmt.Errorf("smoke release binary: %s", output)
	}
	expected := "zero " + version
	if output != expected {
		return fmt.Errorf("expected %s --version to print %s, got %s", filepath.Base(binaryPath), expected, output)
	}
	return nil
}

func copyPackageFiles(rootDir string, stagingDir string, artifactPath string, stagedBinaryPath string, goos string, version string, helperArtifacts map[string]string) error {
	if err := copyFile(artifactPath, stagedBinaryPath, 0o755); err != nil {
		return err
	}
	if goos != "windows" {
		if err := os.Chmod(stagedBinaryPath, 0o755); err != nil {
			return err
		}
	}
	for _, path := range []string{"README.md", "package.json"} {
		if err := copyFile(filepath.Join(rootDir, path), filepath.Join(stagingDir, path), 0o644); err != nil {
			return err
		}
	}
	if err := copyFile(filepath.Join(rootDir, "bin", "zero.js"), filepath.Join(stagingDir, "bin", "zero.js"), 0o755); err != nil {
		return err
	}
	for name, source := range helperArtifacts {
		if err := copyFile(source, filepath.Join(stagingDir, name), 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "VERSION"), []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	return nil
}

func copyFile(source string, destination string, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() {
		_ = input.Close()
	}()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer func() {
		_ = output.Close()
	}()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	return output.Chmod(mode)
}

func createArchive(stagingDir string, archivePath string, goos string) error {
	if err := os.MkdirAll(filepath.Dir(archivePath), 0o755); err != nil {
		return err
	}
	if goos == "windows" {
		return createZipArchive(stagingDir, archivePath)
	}
	return createTarGzArchive(stagingDir, archivePath)
}

func createTarGzArchive(stagingDir string, archivePath string) error {
	file, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	gzipWriter := gzip.NewWriter(file)
	tarWriter := tar.NewWriter(gzipWriter)
	retErr := filepath.WalkDir(stagingDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == stagingDir {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(stagingDir, path)
		if err != nil {
			return err
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relativePath)
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		entryFile, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tarWriter, entryFile)
		closeErr := entryFile.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	retErr = mergeCloseError(retErr, tarWriter.Close())
	retErr = mergeCloseError(retErr, gzipWriter.Close())
	retErr = mergeCloseError(retErr, file.Close())
	return retErr
}

func createZipArchive(stagingDir string, archivePath string) error {
	file, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	zipWriter := zip.NewWriter(file)
	retErr := filepath.WalkDir(stagingDir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == stagingDir || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		relativePath, err := filepath.Rel(stagingDir, path)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relativePath)
		header.Method = zip.Deflate
		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}
		entryFile, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, entryFile)
		closeErr := entryFile.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	retErr = mergeCloseError(retErr, zipWriter.Close())
	retErr = mergeCloseError(retErr, file.Close())
	return retErr
}

func mergeCloseError(retErr error, closeErr error) error {
	if retErr == nil {
		return closeErr
	}
	if closeErr == nil {
		return retErr
	}
	return errors.Join(retErr, closeErr)
}

func assertSafeChecksumFileName(fileName string) error {
	if fileName == "" || fileName != filepath.Base(fileName) || strings.Contains(fileName, "/") || strings.Contains(fileName, `\`) {
		return fmt.Errorf("checksum archive name must be a same-directory file name: %s", fileName)
	}
	return nil
}
