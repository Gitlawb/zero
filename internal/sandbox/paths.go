package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type pathBlock struct {
	Code   BlockCode
	Path   string
	Reason string
}

func validateWorkspacePath(workspaceRoot string, requestedPath string) *pathBlock {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return &pathBlock{Code: BlockOutsideWorkspace, Path: requestedPath, Reason: err.Error()}
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return &pathBlock{Code: BlockOutsideWorkspace, Path: requestedPath, Reason: err.Error()}
	}

	target := requestedPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target, err = filepath.Abs(target)
	if err != nil {
		return &pathBlock{Code: BlockOutsideWorkspace, Path: requestedPath, Reason: err.Error()}
	}

	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return &pathBlock{
			Code:   BlockOutsideWorkspace,
			Path:   requestedPath,
			Reason: fmt.Sprintf("%s is outside the workspace", requestedPath),
		}
	}
	if relative == "." {
		return nil
	}

	current := root
	for _, segment := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if segment == "." || segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return &pathBlock{Code: BlockOutsideWorkspace, Path: requestedPath, Reason: err.Error()}
		}
		if info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		resolved, err := filepath.EvalSymlinks(current)
		if err != nil {
			return &pathBlock{Code: BlockSymlinkTraversal, Path: requestedPath, Reason: err.Error()}
		}
		resolvedRelative, err := filepath.Rel(root, resolved)
		if err != nil || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) || filepath.IsAbs(resolvedRelative) {
			return &pathBlock{
				Code:   BlockSymlinkTraversal,
				Path:   requestedPath,
				Reason: fmt.Sprintf("%s must not traverse symlink %s", requestedPath, filepath.ToSlash(filepath.Clean(segment))),
			}
		}
	}
	return nil
}
