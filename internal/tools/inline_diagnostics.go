package tools

import "context"

// inlineDiagnostics renders the post-write diagnostics block a mutating tool
// appends to its output. Empty when no Diagnostics callback is wired, the file
// is clean, or no language server is available — the tool output is then
// byte-identical to the pre-diagnostics behavior.
func inlineDiagnostics(ctx context.Context, options RunOptions, absolutePath, relativePath string) string {
	if options.Diagnostics == nil {
		return ""
	}
	block := options.Diagnostics(ctx, absolutePath)
	if block == "" {
		return ""
	}
	return "\n\nDiagnostics in " + relativePath + " after this change (fix any errors you introduced):\n" + block
}
