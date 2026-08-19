package tools

// ModelVisible reports whether a registered tool is advertised to an agent.
// edit_file remains implemented for compatibility with existing integrations,
// but its text-only replacement contract cannot safely disambiguate repeated
// source fragments. Agents use apply_patch for contextual existing-file edits.
func ModelVisible(tool Tool) bool {
	return tool != nil && tool.Name() != "edit_file"
}
