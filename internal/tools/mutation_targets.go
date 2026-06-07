package tools

// MutationTargets returns the workspace-relative paths a tool call will write to,
// so the session layer can snapshot their before-state for safe rewind. It is a
// pure helper (no I/O beyond path resolution) and returns nil for read-only tools
// and for bash (whose affected paths are not knowable before execution).
func MutationTargets(workspaceRoot string, name string, args map[string]any) []string {
	switch name {
	case "write_file", "edit_file":
		// Resolve the path via the SAME alias key list write_file/edit_file use,
		// so a checkpoint is captured even when the model writes via an alias key
		// (e.g. {"file": ...}); otherwise /rewind could not undo the write.
		path, err := aliasedStringArg(args, []string{"path", "file", "file_path", "filename"}, "", true, false)
		if err != nil {
			return nil
		}
		_, relative, err := resolveWorkspaceTargetPath(workspaceRoot, path)
		if err != nil {
			return nil
		}
		return []string{relative}
	case "apply_patch":
		// Resolve the patch via the SAME alias key list apply_patch uses.
		patch, err := aliasedStringArg(args, []string{"patch", "diff"}, "", true, false)
		if err != nil {
			return nil
		}
		// Mirror apply_patch's cwd handling so the returned targets are
		// WORKSPACE-relative (cwd-prefixed) when cwd != ".". Without this, a
		// patch applied under a subdir would snapshot the wrong rewind path.
		cwd, err := stringArg(args, "cwd", ".", false)
		if err != nil {
			return nil
		}
		_, relativeRoot, err := resolveWorkspacePath(workspaceRoot, cwd)
		if err != nil {
			return nil
		}
		paths := changedFilesFromPatch(relativeRoot, patch)
		if len(paths) == 0 {
			return nil
		}
		return paths
	default:
		return nil
	}
}
