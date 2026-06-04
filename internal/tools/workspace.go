package tools

import (
	"fmt"
	"path/filepath"
	"strings"
)

var ignoredDirectories = map[string]bool{
	".git":         true,
	"node_modules": true,
	"dist":         true,
	"build":        true,
	".next":        true,
	".turbo":       true,
	"coverage":     true,
	".cache":       true,
	"tmp":          true,
	"temp":         true,
}

func normalizeWorkspaceRoot(workspaceRoot string) string {
	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return workspaceRoot
	}
	return root
}

func resolveWorkspacePath(workspaceRoot string, requestedPath string) (string, string, error) {
	if requestedPath == "" {
		requestedPath = "."
	}

	root, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return "", "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", err
	}

	target := requestedPath
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}

	target, err = filepath.Abs(target)
	if err != nil {
		return "", "", err
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", err
	}

	relative, err := filepath.Rel(root, target)
	if err != nil {
		return "", "", err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", fmt.Errorf("%s must stay inside the workspace", requestedPath)
	}
	if relative == "." {
		return target, ".", nil
	}
	return target, filepath.ToSlash(relative), nil
}

func shouldSkipDirectory(name string) bool {
	return ignoredDirectories[name]
}
