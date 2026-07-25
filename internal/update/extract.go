package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// extractArchive extracts a .tar.gz or .zip release archive into destDir.
func extractArchive(archivePath string, destDir string) error {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractZip(archivePath, destDir)
	}
	return extractTarGz(archivePath, destDir)
}

func extractTarGz(archivePath string, destDir string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer func() {
		_ = gzipReader.Close()
	}()
	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeExtractPath(destDir, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if filepath.IsAbs(header.Linkname) {
				return fmt.Errorf("absolute symlink targets are not supported: %s -> %s", header.Name, header.Linkname)
			}
			// Verify that the symlink target, when resolved, does not escape destDir.
			resolvedTarget := filepath.Join(filepath.Dir(target), header.Linkname)
			destDirClean := filepath.Clean(destDir)
			if !strings.HasPrefix(resolvedTarget, destDirClean+string(os.PathSeparator)) && resolvedTarget != destDirClean {
				return fmt.Errorf("archive symlink target escapes destination: %s -> %s", header.Name, header.Linkname)
			}
			_ = os.Remove(target)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := writeExtractedFile(target, tarReader, fs.FileMode(header.Mode)); err != nil {
				return err
			}
		default:
			// Release archives only ever contain regular files and directories;
			// reject anything else (symlinks, devices) rather than silently skip it.
			return fmt.Errorf("unsupported archive entry type for %s", header.Name)
		}
	}
}

func extractZip(archivePath string, destDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = reader.Close()
	}()
	for _, entry := range reader.File {
		target, err := safeExtractPath(destDir, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		// Release archives only ever contain regular files and directories;
		// reject anything else (symlinks, devices) rather than silently write
		// the link-target string (or other special content) out as an
		// ordinary file. Unlike extractTarGz, zip symlinks stay rejected: the
		// .zip path is the Windows release archive format, where
		// filepath.IsAbs does not reliably reject a slash-rooted target like
		// "/some/other/path" (Windows absolute paths need a drive letter or
		// UNC prefix), and there is no current use case for symlinks in a
		// Windows archive. The npm shim symlinks this feature exists for are
		// packaged in the Unix/macOS .tar.gz archives, handled above.
		if !entry.Mode().IsRegular() {
			return fmt.Errorf("unsupported archive entry type for %s", entry.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := func() error {
			entryReader, err := entry.Open()
			if err != nil {
				return err
			}
			defer func() {
				_ = entryReader.Close()
			}()
			return writeExtractedFile(target, entryReader, entry.Mode())
		}(); err != nil {
			return err
		}
	}
	return nil
}

func writeExtractedFile(target string, source io.Reader, mode fs.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, source)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

// safeExtractPath resolves an archive entry name against destDir, rejecting
// absolute paths or entries that would escape destDir via "..".
func safeExtractPath(destDir string, name string) (string, error) {
	cleanName := filepath.Clean(strings.ReplaceAll(name, "\\", "/"))
	if cleanName == "." {
		return destDir, nil
	}
	if filepath.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, "../") {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	target := filepath.Join(destDir, cleanName)
	destDirClean := filepath.Clean(destDir)
	if target != destDirClean && !strings.HasPrefix(target, destDirClean+string(os.PathSeparator)) {
		return "", fmt.Errorf("archive entry escapes destination: %s", name)
	}
	return target, nil
}

// findByBasename recursively searches root for the first regular file whose
// basename matches name, mirroring scripts/postinstall.mjs's lookup so
// helper binaries nested under archive subdirectories (e.g. helpers/) are
// still found.
func findByBasename(root string, name string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if found != "" {
			return fs.SkipAll
		}
		if !entry.IsDir() && entry.Name() == name {
			found = path
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return found, nil
}
