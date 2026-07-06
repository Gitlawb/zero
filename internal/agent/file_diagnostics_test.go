package agent

import (
	"path/filepath"
	"testing"
)

// Diagnostics are model-facing: absolute paths would leak the local username
// and directory layout into the prompt and session transcript on every edit.
func TestDiagnosticsDisplayPath(t *testing.T) {
	root := filepath.Join("/Users", "someone", "project")
	cases := []struct {
		root, abs, want string
	}{
		{root, filepath.Join(root, "internal", "a.go"), filepath.Join("internal", "a.go")},
		{root, filepath.Join("/etc", "other.go"), "other.go"}, // outside root -> base name only
		{"", filepath.Join("/home", "user", "x.go"), "x.go"},  // no root -> base name only
	}
	for _, c := range cases {
		if got := diagnosticsDisplayPath(c.root, c.abs); got != c.want {
			t.Errorf("diagnosticsDisplayPath(%q, %q) = %q, want %q", c.root, c.abs, got, c.want)
		}
	}
}
