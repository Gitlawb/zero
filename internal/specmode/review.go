package specmode

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func LoadSpecFile(workspaceRoot string, specFilePath string) (string, string, error) {
	path, err := ResolveSpecFilePath(workspaceRoot, specFilePath)
	if err != nil {
		return "", "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", "", fmt.Errorf("stat spec file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("spec file is not a regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read spec file: %w", err)
	}
	body := strings.TrimRight(string(data), "\n")
	if strings.TrimSpace(body) == "" {
		return "", "", fmt.Errorf("spec file is empty: %s", path)
	}
	return body, path, nil
}

func ResolveSpecFilePath(workspaceRoot string, specFilePath string) (string, error) {
	root := strings.TrimSpace(workspaceRoot)
	if root == "" {
		return "", fmt.Errorf("workspace root is required")
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	path := strings.TrimSpace(specFilePath)
	if path == "" {
		return "", fmt.Errorf("spec file path is required")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(absoluteRoot, filepath.FromSlash(path))
	}
	path = filepath.Clean(path)
	specDir := filepath.Join(absoluteRoot, filepath.FromSlash(SpecDirName))
	if err := ensureSpecPathContained(specDir, path); err != nil {
		return "", err
	}
	return path, nil
}

func ImplementationPrompt(specBody string, specFilePath string, draftSessionID string, userComment string) string {
	body := strings.TrimSpace(specBody)
	lines := []string{
		"Implement the following approved spec:",
		"",
	}
	if comment := strings.TrimSpace(userComment); comment != "" {
		lines = append(lines, "User note: "+comment, "")
	}
	lines = append(lines, body, "")
	if path := strings.TrimSpace(specFilePath); path != "" {
		lines = append(lines, "Spec file: "+path)
	}
	if sessionID := strings.TrimSpace(draftSessionID); sessionID != "" {
		lines = append(lines, "Planning session: "+sessionID)
	}
	lines = append(lines,
		"",
		"If you need details that are not in the spec, inspect the workspace again. The spec is the approved source of truth for scope.",
	)
	return strings.Join(lines, "\n")
}
