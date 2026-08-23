package mcp

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/daemon/remote"
	"github.com/Gitlawb/zero/internal/tools"
)

func TestServeMCPExcludesDaemonTokenFromTools(t *testing.T) {
	workspace := t.TempDir()
	token := filepath.Join(workspace, "bridge-token")
	const secret = "mcp-bridge-secret"
	if err := os.WriteFile(token, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "ordinary.txt"), []byte("ordinary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(remote.EnvToken, "")
	t.Setenv(remote.EnvTokenFile, token)

	registry := tools.NewRegistry()
	toolset := append(tools.CoreReadOnlyToolsScoped(workspace, nil), tools.CoreWriteToolsScoped(workspace, nil)...)
	for _, tool := range toolset {
		registry.Register(tool)
	}
	options := ServeOptions{WorkspaceRoot: workspace, PermissionGranted: true}

	for _, tc := range []struct {
		name      string
		arguments map[string]any
		wantError bool
	}{
		{name: "read_file", arguments: map[string]any{"path": "bridge-token"}, wantError: true},
		{name: "read_minified_file", arguments: map[string]any{"path": "bridge-token"}, wantError: true},
		{name: "grep", arguments: map[string]any{"pattern": secret, "path": "."}},
		{name: "glob", arguments: map[string]any{"pattern": "*", "cwd": "."}},
		{name: "list_directory", arguments: map[string]any{"path": "."}},
		{name: "write_file", arguments: map[string]any{"path": "bridge-token", "content": "attacker\n", "overwrite": true}, wantError: true},
		{name: "edit_file", arguments: map[string]any{"path": "bridge-token", "old_string": secret, "new_string": "attacker"}, wantError: true},
		{name: "apply_patch", arguments: map[string]any{"patch": "--- a/bridge-token\n+++ b/bridge-token\n@@ -1 +1 @@\n-" + secret + "\n+attacker\n"}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := callServerTool(t, registry, options, tc.name, tc.arguments)
			if result.IsError != tc.wantError {
				t.Fatalf("IsError = %v, want %v; output=%q", result.IsError, tc.wantError, TextContent(result.Content))
			}
			output := TextContent(result.Content)
			if strings.Contains(output, secret) {
				t.Fatalf("MCP %s disclosed token bytes: %q", tc.name, output)
			}
			if !tc.wantError && strings.Contains(output, "bridge-token") {
				t.Fatalf("MCP %s disclosed token filename: %q", tc.name, output)
			}
		})
	}

	contents, err := os.ReadFile(token)
	if err != nil || string(contents) != secret+"\n" {
		t.Fatalf("token changed through MCP mutation tool: contents=%q err=%v", contents, err)
	}
}

func TestServeMCPExcludesDaemonTokenFromResources(t *testing.T) {
	workspace := t.TempDir()
	token := filepath.Join(workspace, "bridge-token")
	const secret = "mcp-resource-secret"
	if err := os.WriteFile(token, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "ordinary.txt"), []byte("ordinary\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(remote.EnvToken, "")
	t.Setenv(remote.EnvTokenFile, token)

	var input bytes.Buffer
	writeServerTestMessage(t, &input, rpcMessage{ID: 1, Method: "resources/list"})
	writeServerTestMessage(t, &input, rpcMessage{
		ID:     2,
		Method: "resources/read",
		Params: mustRaw(map[string]any{"uri": fileURI(token)}),
	})
	var output bytes.Buffer
	if err := Serve(context.Background(), &input, &output, tools.NewRegistry(), ServeOptions{WorkspaceRoot: workspace}); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	reader := newMessageReader(&output)
	var listed struct {
		Resources []Resource `json:"resources"`
	}
	decodeServerTestResult(t, readServerTestMessage(t, reader), &listed)
	foundOrdinary := false
	for _, resource := range listed.Resources {
		if resource.Name == "bridge-token" || strings.Contains(resource.URI, "bridge-token") {
			t.Fatalf("resources/list advertised the token: %#v", resource)
		}
		foundOrdinary = foundOrdinary || resource.Name == "ordinary.txt"
	}
	if !foundOrdinary {
		t.Fatalf("resources/list omitted ordinary file: %#v", listed.Resources)
	}

	read := readServerTestMessage(t, reader)
	if read.Error == nil || len(read.Result) != 0 {
		t.Fatalf("resources/read token response = %#v, want not-found without contents", read)
	}
	if strings.Contains(read.Error.Message, secret) {
		t.Fatalf("resources/read error disclosed token bytes: %q", read.Error.Message)
	}
}

// resources/read decides from the handle it opened, not from a pathname it
// checked and then reopened. A hard link is a second name for the same inode, so
// no pathname rule covers it — the exclusion has to compare the object, and the
// object it compares must be the one the read returns.
func TestResourcesReadRefusesHardLinkedToken(t *testing.T) {
	workspace := t.TempDir()
	token := filepath.Join(workspace, "bridge-token")
	const secret = "mcp-hardlink-secret"
	if err := os.WriteFile(token, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(workspace, "notes.txt")
	if err := os.Link(token, alias); err != nil {
		t.Skipf("workspace filesystem is not hard-linkable: %v", err)
	}
	t.Setenv(remote.EnvToken, "")
	t.Setenv(remote.EnvTokenFile, token)

	var input bytes.Buffer
	writeServerTestMessage(t, &input, rpcMessage{
		ID:     1,
		Method: "resources/read",
		Params: mustRaw(map[string]any{"uri": fileURI(alias)}),
	})
	var output bytes.Buffer
	if err := Serve(context.Background(), &input, &output, tools.NewRegistry(), ServeOptions{WorkspaceRoot: workspace}); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}

	read := readServerTestMessage(t, newMessageReader(&output))
	if read.Error == nil || len(read.Result) != 0 {
		t.Fatalf("resources/read alias response = %#v, want not-found without contents", read)
	}
	if strings.Contains(read.Error.Message, secret) {
		t.Fatalf("resources/read error disclosed token bytes: %q", read.Error.Message)
	}
}
