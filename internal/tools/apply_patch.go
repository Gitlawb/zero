package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type applyPatchTool struct {
	baseTool
	workspaceRoot string
}

func NewApplyPatchTool(workspaceRoot string) Tool {
	return applyPatchTool{
		baseTool: baseTool{
			name:        "apply_patch",
			description: "Apply a unified diff patch inside the workspace.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"patch": {Type: "string", Description: "Unified diff patch to apply."},
					"cwd":   {Type: "string", Description: "Directory where the patch should be applied. Defaults to workspace root.", Default: "."},
				},
				Required:             []string{"patch"},
				AdditionalProperties: false,
			},
			safety: promptSafety(SideEffectWrite, "Applies patch hunks that can create, edit, or delete files."),
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
	}
}

func (tool applyPatchTool) Run(ctx context.Context, args map[string]any) Result {
	patch, err := stringArg(args, "patch", "", true)
	if err != nil {
		return errorResult("Error: Invalid arguments for apply_patch: " + err.Error())
	}
	cwd, err := stringArg(args, "cwd", ".", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for apply_patch: " + err.Error())
	}

	applyRoot, relativeRoot, err := resolveWorkspacePath(tool.workspaceRoot, cwd)
	if err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	if err := validatePatchPaths(applyRoot, patch); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}

	tempFile, err := os.CreateTemp("", "zero-patch-*.patch")
	if err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}
	patchPath := tempFile.Name()
	defer func() {
		_ = os.Remove(patchPath)
	}()
	if _, err := tempFile.WriteString(patch); err != nil {
		_ = tempFile.Close()
		return errorResult("Error applying patch: " + err.Error())
	}
	if err := tempFile.Close(); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}

	if err := recheckPatchWriteTargets(applyRoot, patch); err != nil {
		return errorResult("Error applying patch: " + err.Error())
	}

	command := exec.CommandContext(ctx, "git", "apply", "--whitespace=nowarn", patchPath)
	command.Dir = applyRoot
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return errorResult("Error applying patch: " + message)
	}

	if relativeRoot == "." {
		return okResult("Patch applied successfully.")
	}
	return okResult("Patch applied successfully in " + relativeRoot + ".")
}

func validatePatchPaths(root string, patch string) error {
	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		for _, path := range patchPathsFromLine(line) {
			if path == "" || path == "/dev/null" {
				continue
			}
			if filepath.IsAbs(path) || path == ".." || strings.HasPrefix(path, "../") {
				return fmt.Errorf("patch path %q must stay inside the workspace", path)
			}
			if _, _, err := resolveWorkspaceTargetPath(root, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func recheckPatchWriteTargets(root string, patch string) error {
	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		for _, path := range patchPathsFromLine(line) {
			if path == "" || path == "/dev/null" {
				continue
			}
			if err := recheckWorkspaceWriteTarget(root, path); err != nil {
				return err
			}
		}
	}
	return nil
}

func patchPathsFromLine(line string) []string {
	if strings.HasPrefix(line, "diff --git ") {
		fields := strings.Fields(line)
		if len(fields) >= 4 {
			return []string{stripPatchPrefix(fields[2]), stripPatchPrefix(fields[3])}
		}
	}
	if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			return []string{stripPatchPrefix(fields[1])}
		}
	}
	return nil
}

func stripPatchPrefix(path string) string {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "a/")
	path = strings.TrimPrefix(path, "b/")
	return filepath.ToSlash(path)
}
