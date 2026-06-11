package agent

import (
	"strings"
	"testing"
)

func TestPermissionScope(t *testing.T) {
	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{name: "write file path", tool: "write_file", args: map[string]any{"path": "src/main.go", "content": "x"}, want: "src/main.go"},
		{name: "edit file path", tool: "edit_file", args: map[string]any{"path": "a/b.txt"}, want: "a/b.txt"},
		{name: "bash explicit cwd", tool: "bash", args: map[string]any{"command": "ls", "cwd": "services/api"}, want: "services/api"},
		{name: "bash workspace-root cwd is no scope", tool: "bash", args: map[string]any{"command": "ls", "cwd": "."}, want: ""},
		{name: "no path-like args", tool: "bash", args: map[string]any{"command": "ls"}, want: ""},
		{name: "directory key", tool: "list_directory", args: map[string]any{"directory": "pkg"}, want: "pkg"},
		{name: "non-string path ignored", tool: "write_file", args: map[string]any{"path": 42}, want: ""},
		{name: "path wins over cwd", tool: "x", args: map[string]any{"cwd": "a", "path": "b"}, want: "b"},
		{name: "whitespace path is no scope", tool: "write_file", args: map[string]any{"path": "  "}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := permissionScope(tt.tool, tt.args); got != tt.want {
				t.Fatalf("permissionScope(%q, %v) = %q, want %q", tt.tool, tt.args, got, tt.want)
			}
		})
	}
}

func TestPermissionScopeTruncatesLongPaths(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := permissionScope("write_file", map[string]any{"path": long})
	if runes := len([]rune(got)); runes > 80 {
		t.Fatalf("scope not truncated: %d runes", runes)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated scope should end with an ellipsis: %q", got)
	}
}
