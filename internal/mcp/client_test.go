package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
)

func TestStdioClientListsAndCallsTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	client, err := Connect(ctx, Server{
		Name:    "docs",
		Type:    ServerTypeStdio,
		Command: executable,
		Args:    []string{"-test.run=TestMCPStdioHelperProcess", "--"},
		Env:     map[string]string{"ZERO_MCP_STDIO_HELPER": "1"},
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer client.Close()

	listed, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "lookup" {
		t.Fatalf("listed tools = %#v, want lookup", listed)
	}
	if listed[0].InputSchema["type"] != "object" {
		t.Fatalf("lookup schema = %#v, want object schema", listed[0].InputSchema)
	}

	result, err := client.CallTool(ctx, "lookup", map[string]any{"query": "zero"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if result.IsError {
		t.Fatalf("CallTool() result IsError = true: %#v", result)
	}
	if got := TextContent(result.Content); got != "lookup: zero" {
		t.Fatalf("CallTool() text = %q, want lookup result", got)
	}
}

func TestConnectRejectsUnsupportedRunnableTransports(t *testing.T) {
	_, err := Connect(context.Background(), Server{
		Name: "web",
		Type: ServerTypeHTTP,
		URL:  "https://example.com/mcp",
	})
	if err == nil {
		t.Fatal("Connect() error = nil, want unsupported transport error")
	}
	if !strings.Contains(err.Error(), "not implemented") {
		t.Fatalf("error = %q, want not implemented", err.Error())
	}
}

func TestMCPStdioHelperProcess(t *testing.T) {
	if os.Getenv("ZERO_MCP_STDIO_HELPER") != "1" {
		return
	}

	reader := newMessageReader(os.Stdin)
	writer := newMessageWriter(os.Stdout)
	for {
		message, err := reader.read()
		if err != nil {
			if strings.Contains(err.Error(), "EOF") {
				os.Exit(0)
			}
			fmt.Fprintf(os.Stderr, "read helper message: %v\n", err)
			os.Exit(1)
		}
		if message.Method == "notifications/initialized" {
			continue
		}

		switch message.Method {
		case "initialize":
			_ = writer.write(rpcMessage{
				JSONRPC: "2.0",
				ID:      message.ID,
				Result: mustRaw(map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "test-docs", "version": "1.0.0"},
				}),
			})
		case "tools/list":
			_ = writer.write(rpcMessage{
				JSONRPC: "2.0",
				ID:      message.ID,
				Result: mustRaw(map[string]any{
					"tools": []map[string]any{{
						"name":        "lookup",
						"description": "Lookup documentation",
						"inputSchema": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"required":             []string{"query"},
							"properties": map[string]any{
								"query": map[string]any{"type": "string", "description": "Search query"},
							},
						},
					}},
				}),
			})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(message.Params, &params)
			_ = writer.write(rpcMessage{
				JSONRPC: "2.0",
				ID:      message.ID,
				Result: mustRaw(map[string]any{
					"content": []map[string]any{{
						"type": "text",
						"text": "lookup: " + strings.TrimSpace(fmt.Sprint(params.Arguments["query"])),
					}},
				}),
			})
		default:
			_ = writer.write(rpcMessage{
				JSONRPC: "2.0",
				ID:      message.ID,
				Error:   &rpcError{Code: -32601, Message: "method not found"},
			})
		}
	}
}

func mustRaw(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

func TestSchemaFromMCPInputSchema(t *testing.T) {
	schema := SchemaFromMCP(map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"query"},
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Search query",
				"enum":        []any{"zero", "docs"},
			},
			"limit": map[string]any{
				"type":    "integer",
				"default": float64(5),
				"minimum": float64(1),
				"maximum": float64(10),
			},
		},
	})

	if schema.Type != "object" || schema.AdditionalProperties {
		t.Fatalf("schema root = %#v", schema)
	}
	if len(schema.Required) != 1 || schema.Required[0] != "query" {
		t.Fatalf("required = %#v, want query", schema.Required)
	}
	query := schema.Properties["query"]
	if query.Type != "string" || len(query.Enum) != 2 {
		t.Fatalf("query schema = %#v", query)
	}
	limit := schema.Properties["limit"]
	if limit.Type != "integer" || limit.Minimum == nil || *limit.Minimum != 1 || limit.Maximum == nil || *limit.Maximum != 10 {
		t.Fatalf("limit schema = %#v", limit)
	}
}

func TestStdioClientServerFromConfig(t *testing.T) {
	servers, err := NormalizeConfig(config.MCPConfig{Servers: map[string]config.MCPServerConfig{
		"docs": {Type: "stdio", Command: "docs-mcp", Args: []string{"--root", "."}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if servers[0].Type != ServerTypeStdio || servers[0].Command != "docs-mcp" {
		t.Fatalf("server = %#v", servers[0])
	}
}
