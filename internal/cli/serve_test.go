package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRunServeMCPListsReadOnlyToolsByDefault(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"serve", "--mcp"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
		stdin: bytes.NewReader(serveMCPInput(t)),
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected empty stderr, got %q", stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"read_file", "list_directory", "glob", "grep"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected MCP output to contain %q, got %q", want, output)
		}
	}
	for _, unwanted := range []string{"write_file", "apply_patch", "bash", "web_fetch"} {
		if strings.Contains(output, unwanted) {
			t.Fatalf("did not expect default MCP output to contain %q: %q", unwanted, output)
		}
	}
}

func TestRunServeMCPAllowsUnsafeToolsWithExplicitFlag(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"serve", "--mcp", "--allow-unsafe-tools"}, &stdout, &stderr, appDeps{
		getwd: func() (string, error) {
			return t.TempDir(), nil
		},
		stdin: bytes.NewReader(serveMCPInput(t)),
	})

	if exitCode != exitSuccess {
		t.Fatalf("expected exit code 0, got %d; stderr=%q", exitCode, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{"read_file", "write_file", "apply_patch", "bash", "web_fetch"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected unsafe MCP output to contain %q, got %q", want, output)
		}
	}
	if !strings.Contains(stderr.String(), "Unsafe MCP server tools enabled") {
		t.Fatalf("expected unsafe warning on stderr, got %q", stderr.String())
	}
}

func TestRunServeRequiresMCPMode(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"serve"}, &stdout, &stderr, appDeps{})

	if exitCode != exitUsage {
		t.Fatalf("expected exit code %d, got %d", exitUsage, exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("expected empty stdout, got %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "serve requires --mcp") {
		t.Fatalf("expected usage error, got %q", stderr.String())
	}
}

func serveMCPInput(t *testing.T) []byte {
	t.Helper()

	var input bytes.Buffer
	writeServeMCPMessage(t, &input, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	})
	writeServeMCPMessage(t, &input, map[string]any{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]any{},
	})
	writeServeMCPMessage(t, &input, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	return input.Bytes()
}

func writeServeMCPMessage(t *testing.T, buffer *bytes.Buffer, message map[string]any) {
	t.Helper()
	body, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fmt.Fprintf(buffer, "Content-Length: %d\r\n\r\n%s", len(body), body); err != nil {
		t.Fatal(err)
	}
}
