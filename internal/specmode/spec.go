package specmode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	SpecDirName = ".zero/specs"
)

type SaveOptions struct {
	WorkspaceRoot string
	Title         string
	Plan          string
	Now           func() time.Time
}

type SavedSpec struct {
	ID           string
	Title        string
	Path         string
	RelativePath string
}

func SaveDraft(options SaveOptions) (SavedSpec, error) {
	root := strings.TrimSpace(options.WorkspaceRoot)
	if root == "" {
		return SavedSpec{}, fmt.Errorf("workspace root is required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return SavedSpec{}, fmt.Errorf("resolve workspace root: %w", err)
	}
	title := strings.TrimSpace(options.Title)
	if title == "" {
		return SavedSpec{}, fmt.Errorf("title is required")
	}
	plan := strings.TrimSpace(options.Plan)
	if plan == "" {
		return SavedSpec{}, fmt.Errorf("plan is required")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}

	date := now().UTC().Format("2006-01-02")
	slug := slugify(title)
	specDir := filepath.Join(absoluteRoot, filepath.FromSlash(SpecDirName))
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		return SavedSpec{}, fmt.Errorf("create spec directory: %w", err)
	}

	for suffix := 0; suffix < 1000; suffix++ {
		id := date + "-" + slug
		if suffix > 0 {
			id = fmt.Sprintf("%s-%s-%d", date, slug, suffix+1)
		}
		relativePath := filepath.ToSlash(filepath.Join(SpecDirName, id+".md"))
		path := filepath.Join(absoluteRoot, filepath.FromSlash(relativePath))
		if err := ensureSpecPathContained(specDir, path); err != nil {
			return SavedSpec{}, err
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return SavedSpec{}, fmt.Errorf("create spec file: %w", err)
		}
		if _, err := file.WriteString(plan + "\n"); err != nil {
			_ = file.Close()
			_ = os.Remove(path)
			return SavedSpec{}, fmt.Errorf("write spec file: %w", err)
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(path)
			return SavedSpec{}, fmt.Errorf("close spec file: %w", err)
		}
		return SavedSpec{
			ID:           id,
			Title:        title,
			Path:         path,
			RelativePath: relativePath,
		}, nil
	}
	return SavedSpec{}, fmt.Errorf("create spec file: too many name collisions for %q", title)
}

func ensureSpecPathContained(specDir string, path string) error {
	relative, err := filepath.Rel(filepath.Clean(specDir), filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("resolve spec file path: %w", err)
	}
	if relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("create spec file: resolved path escapes %s", specDir)
	}
	return nil
}
