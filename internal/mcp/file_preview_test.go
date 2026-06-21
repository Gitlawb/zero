package mcp

import (
	"strings"
	"testing"
)

// An MCP filesystem write_file call should yield a preview body from its content
// argument; directory-creation and read tools must not.
func TestMCPFileWriteArgsDetection(t *testing.T) {
	path, content, ok := mcpFileWriteArgs("write_file", map[string]any{
		"path": "site/index.html", "content": "<html>\n<body>hi</body>\n</html>\n",
	})
	if !ok || path != "site/index.html" || !strings.Contains(content, "<html>") {
		t.Fatalf("write_file should be detected, got ok=%v path=%q", ok, path)
	}
	if _, _, ok := mcpFileWriteArgs("create_directory", map[string]any{"path": "site"}); ok {
		t.Error("create_directory (no content) must not be treated as a file write")
	}
	if _, _, ok := mcpFileWriteArgs("read_file", map[string]any{"path": "x", "content": "y"}); ok {
		t.Error("read_file must not be treated as a file write")
	}
	if _, _, ok := mcpFileWriteArgs("write_file", map[string]any{"path": "x"}); ok {
		t.Error("a write_file without content must not produce a preview")
	}
}
