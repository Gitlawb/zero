package tools

import (
	"strings"
	"testing"
)

// The cheapest fix for #845/#847 is that the invalid shape is never emitted.
// A model only learns "omit it" from the schema it is shown, so both shell
// tools must say so in the field descriptions themselves — the validator errors
// are the backstop, and by the time they fire a turn has already been burned.
func TestShellToolsAdvertiseOmittingSandboxPermissionFields(t *testing.T) {
	root := t.TempDir()
	for _, shell := range []struct {
		name string
		tool Tool
	}{
		{"bash", NewScopedBashTool(root, nil)},
		{"exec_command", NewScopedExecCommandTool(root, nil, nil)},
	} {
		properties := shell.tool.Parameters().Properties

		mode, ok := properties["sandbox_permissions"]
		if !ok {
			t.Fatalf("%s: sandbox_permissions property missing", shell.name)
		}
		if !strings.Contains(mode.Description, "Omit it") {
			t.Errorf("%s: sandbox_permissions description = %q, want it to lead with omitting the field", shell.name, mode.Description)
		}
		if !strings.Contains(mode.Description, "read-only") {
			t.Errorf("%s: sandbox_permissions description = %q, want it to say read-only commands need no override", shell.name, mode.Description)
		}

		extra, ok := properties["additional_permissions"]
		if !ok {
			t.Fatalf("%s: additional_permissions property missing", shell.name)
		}
		if !strings.Contains(extra.Description, "Omit this field entirely") {
			t.Errorf("%s: additional_permissions description = %q, want an explicit instruction to omit the field", shell.name, extra.Description)
		}
		// Both invalid shapes from the issue must be named as invalid up front,
		// so neither is emitted "to be safe".
		if !strings.Contains(extra.Description, "with any other mode is rejected") {
			t.Errorf("%s: additional_permissions description = %q, want it to say the unflagged shape is rejected", shell.name, extra.Description)
		}
		if !strings.Contains(extra.Description, "empty or all-false object") {
			t.Errorf("%s: additional_permissions description = %q, want it to say an empty payload is rejected", shell.name, extra.Description)
		}
	}
}

// The two shell tools describe the same two fields; they were copy-pasted and
// must not drift apart again, or a model would be taught different rules by
// bash and exec_command in the same session.
func TestShellToolsShareSandboxPermissionDescriptions(t *testing.T) {
	root := t.TempDir()
	bash := NewScopedBashTool(root, nil).Parameters().Properties
	exec := NewScopedExecCommandTool(root, nil, nil).Parameters().Properties

	for _, field := range []string{"sandbox_permissions", "additional_permissions"} {
		if bash[field].Description != exec[field].Description {
			t.Errorf("%s description differs between bash and exec_command:\n bash = %q\n exec = %q",
				field, bash[field].Description, exec[field].Description)
		}
	}
}
