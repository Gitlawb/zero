package agenteval

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type ContextChecks struct {
	RequiredFiles  []string `json:"requiredFiles,omitempty"`
	ForbiddenFiles []string `json:"forbiddenFiles,omitempty"`
}

type ContextCheckResult struct {
	MissingRequiredFiles  []string
	PresentForbiddenFiles []string
}

func (result ContextCheckResult) OK() bool {
	return len(result.MissingRequiredFiles) == 0 && len(result.PresentForbiddenFiles) == 0
}

func (checks ContextChecks) CheckWorkspace(workspace string) (ContextCheckResult, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ContextCheckResult{}, errors.New("workspace path is required")
	}
	required, requiredProblems := normalizeContextCheckFiles("requiredFiles", checks.RequiredFiles)
	forbidden, forbiddenProblems := normalizeContextCheckFiles("forbiddenFiles", checks.ForbiddenFiles)
	if len(requiredProblems) > 0 || len(forbiddenProblems) > 0 {
		return ContextCheckResult{}, ValidationError{Problems: append(requiredProblems, forbiddenProblems...)}
	}

	workspacePath, err := filepath.Abs(workspace)
	if err != nil {
		return ContextCheckResult{}, fmt.Errorf("resolve workspace path: %w", err)
	}
	info, err := os.Stat(workspacePath)
	if err != nil {
		return ContextCheckResult{}, fmt.Errorf("workspace: %w", err)
	}
	if !info.IsDir() {
		return ContextCheckResult{}, fmt.Errorf("workspace must be a directory: %s", workspacePath)
	}

	result := ContextCheckResult{}
	for _, file := range required {
		exists, err := workspacePathExists(workspacePath, file)
		if err != nil {
			return ContextCheckResult{}, err
		}
		if !exists {
			result.MissingRequiredFiles = append(result.MissingRequiredFiles, file)
		}
	}
	for _, file := range forbidden {
		exists, err := workspacePathExists(workspacePath, file)
		if err != nil {
			return ContextCheckResult{}, err
		}
		if exists {
			result.PresentForbiddenFiles = append(result.PresentForbiddenFiles, file)
		}
	}
	return result, nil
}

func normalizeContextCheckFiles(field string, files []string) ([]string, []string) {
	normalized := make([]string, 0, len(files))
	problems := []string{}
	seen := map[string]bool{}
	for index, file := range files {
		clean, ok := normalizeEvalPath(file)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s[%d] must be a relative workspace path", field, index))
			continue
		}
		if clean == "" {
			problems = append(problems, fmt.Sprintf("%s[%d] must not be empty", field, index))
			continue
		}
		if seen[clean] {
			continue
		}
		seen[clean] = true
		normalized = append(normalized, clean)
	}
	sort.Strings(normalized)
	return normalized, problems
}

func workspacePathExists(workspace string, file string) (bool, error) {
	path := filepath.Join(workspace, filepath.FromSlash(file))
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat workspace path %q: %w", file, err)
}
