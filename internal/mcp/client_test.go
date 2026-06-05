package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
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
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

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

func TestStdioClientCloseAllowsConcurrentCallers(t *testing.T) {
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

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			errs <- client.Close()
		}()
	}
	close(start)
	wait.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}
}

func TestHTTPClientListsAndCallsTools(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	testServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", request.Method)
			http.Error(response, "bad method", http.StatusMethodNotAllowed)
			return
		}
		if request.URL.Path != "/mcp" {
			t.Errorf("path = %s, want /mcp", request.URL.Path)
			http.Error(response, "bad path", http.StatusNotFound)
			return
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test" {
			t.Errorf("Authorization = %q, want bearer header", got)
			http.Error(response, "missing auth", http.StatusUnauthorized)
			return
		}

		message := readHTTPRPCMessage(t, request)
		switch message.Method {
		case "initialize":
			response.Header().Set("Mcp-Session-Id", "session-123")
			writeHTTPRPCResponse(t, response, message.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "http-docs", "version": "1.0.0"},
			})
		case "notifications/initialized":
			if got := request.Header.Get("Mcp-Session-Id"); got != "session-123" {
				t.Errorf("initialized session header = %q, want session-123", got)
				http.Error(response, "missing session", http.StatusBadRequest)
				return
			}
			response.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if got := request.Header.Get("Mcp-Session-Id"); got != "session-123" {
				t.Errorf("tools/list session header = %q, want session-123", got)
				http.Error(response, "missing session", http.StatusBadRequest)
				return
			}
			writeHTTPRPCResponse(t, response, message.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "lookup",
					"description": "Lookup documentation",
					"inputSchema": map[string]any{"type": "object"},
				}},
			})
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(message.Params, &params); err != nil {
				t.Errorf("decode tools/call params: %v", err)
				http.Error(response, "bad params", http.StatusBadRequest)
				return
			}
			if params.Name != "lookup" || params.Arguments["query"] != "zero" {
				t.Errorf("tools/call params = %#v", params)
				http.Error(response, "bad tool call", http.StatusBadRequest)
				return
			}
			writeHTTPRPCResponse(t, response, message.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "lookup: zero"}},
			})
		default:
			t.Errorf("unexpected method %q", message.Method)
			writeHTTPRPCError(t, response, message.ID, "method not found")
		}
	}))
	defer testServer.Close()

	client, err := Connect(ctx, Server{
		Name:    "docs",
		Type:    ServerTypeHTTP,
		URL:     testServer.URL + "/mcp",
		Headers: map[string]string{"Authorization": "Bearer test"},
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	listed, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "lookup" {
		t.Fatalf("listed tools = %#v, want lookup", listed)
	}

	result, err := client.CallTool(ctx, "lookup", map[string]any{"query": "zero"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if got := TextContent(result.Content); got != "lookup: zero" {
		t.Fatalf("CallTool() text = %q, want lookup result", got)
	}
}

func TestSSEClientListsAndCallsToolsFromRemoteStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events := make(chan string, 4)
	testServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer test" {
			t.Errorf("Authorization = %q, want bearer header", got)
			http.Error(response, "missing auth", http.StatusUnauthorized)
			return
		}

		if request.Method == http.MethodGet && request.URL.Path == "/sse" {
			if got := request.Header.Get("Accept"); !strings.Contains(got, "text/event-stream") {
				t.Errorf("Accept = %q, want text/event-stream", got)
				http.Error(response, "bad accept", http.StatusBadRequest)
				return
			}
			flusher, ok := response.(http.Flusher)
			if !ok {
				t.Errorf("test response writer does not support flushing")
				http.Error(response, "streaming unsupported", http.StatusInternalServerError)
				return
			}
			response.Header().Set("Content-Type", "text/event-stream")
			if _, err := fmt.Fprint(response, "event: endpoint\ndata: /messages\n\n"); err != nil {
				t.Errorf("write endpoint event: %v", err)
				return
			}
			flusher.Flush()
			for {
				select {
				case event := <-events:
					if _, err := fmt.Fprint(response, event); err != nil {
						t.Errorf("write SSE event: %v", err)
						return
					}
					flusher.Flush()
				case <-request.Context().Done():
					return
				}
			}
		}

		if request.Method != http.MethodPost || request.URL.Path != "/messages" {
			t.Errorf("request = %s %s, want POST /messages", request.Method, request.URL.Path)
			http.Error(response, "bad request", http.StatusNotFound)
			return
		}

		message := readHTTPRPCMessage(t, request)
		switch message.Method {
		case "initialize":
			events <- formatSSERPCResponse(t, message.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "sse-docs", "version": "1.0.0"},
			})
			response.WriteHeader(http.StatusAccepted)
		case "notifications/initialized":
			response.WriteHeader(http.StatusNoContent)
		case "tools/list":
			events <- formatSSERPCResponse(t, message.ID, map[string]any{
				"tools": []map[string]any{{
					"name":        "lookup",
					"description": "Lookup documentation",
					"inputSchema": map[string]any{"type": "object"},
				}},
			})
			response.WriteHeader(http.StatusAccepted)
		case "tools/call":
			var params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			if err := json.Unmarshal(message.Params, &params); err != nil {
				t.Errorf("decode tools/call params: %v", err)
				http.Error(response, "bad params", http.StatusBadRequest)
				return
			}
			if params.Name != "lookup" || params.Arguments["query"] != "zero" {
				t.Errorf("tools/call params = %#v", params)
				http.Error(response, "bad tool call", http.StatusBadRequest)
				return
			}
			events <- formatSSERPCResponse(t, message.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "lookup: zero"}},
			})
			response.WriteHeader(http.StatusAccepted)
		default:
			t.Errorf("unexpected method %q", message.Method)
			events <- formatSSERPCError(t, message.ID, "method not found")
			response.WriteHeader(http.StatusAccepted)
		}
	}))
	defer testServer.Close()

	client, err := Connect(ctx, Server{
		Name:    "docs",
		Type:    ServerTypeSSE,
		URL:     testServer.URL + "/sse",
		Headers: map[string]string{"Authorization": "Bearer test"},
	})
	if err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	listed, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Name != "lookup" {
		t.Fatalf("listed tools = %#v, want lookup", listed)
	}

	result, err := client.CallTool(ctx, "lookup", map[string]any{"query": "zero"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if got := TextContent(result.Content); got != "lookup: zero" {
		t.Fatalf("CallTool() text = %q, want lookup result", got)
	}
}

func TestHTTPClientReportsNonOKStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	testServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Error(response, "server failed", http.StatusBadGateway)
	}))
	defer testServer.Close()

	_, err := Connect(ctx, Server{
		Name: "web",
		Type: ServerTypeHTTP,
		URL:  testServer.URL + "/mcp",
	})
	if err == nil {
		t.Fatal("Connect() error = nil, want status error")
	}
	if !strings.Contains(err.Error(), "502") {
		t.Fatalf("error = %q, want HTTP status", err.Error())
	}
}

func TestConnectRejectsUnsupportedTransport(t *testing.T) {
	_, err := Connect(context.Background(), Server{Name: "web", Type: ServerType("websocket")})
	if err == nil {
		t.Fatal("Connect() error = nil, want unsupported transport error")
	}
	if !strings.Contains(err.Error(), "unsupported MCP transport") {
		t.Fatalf("error = %q, want unsupported transport", err.Error())
	}
}

func TestClientRequestWaitsForMatchingResponseID(t *testing.T) {
	var incoming bytes.Buffer
	incomingWriter := newMessageWriter(&incoming)
	if err := incomingWriter.write(rpcMessage{Method: "notifications/progress"}); err != nil {
		t.Fatal(err)
	}
	if err := incomingWriter.write(rpcMessage{
		ID:    99,
		Error: &rpcError{Code: -32000, Message: "wrong response"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := incomingWriter.write(rpcMessage{
		ID:     1,
		Result: mustRaw(map[string]any{"value": "matched"}),
	}); err != nil {
		t.Fatal(err)
	}

	var outgoing bytes.Buffer
	client := &Client{
		reader: newMessageReader(&incoming),
		writer: newMessageWriter(&outgoing),
		nextID: 1,
	}
	var result struct {
		Value string `json:"value"`
	}
	if err := client.request(context.Background(), "tools/list", map[string]any{}, &result); err != nil {
		t.Fatalf("request() error = %v", err)
	}
	if result.Value != "matched" {
		t.Fatalf("result.Value = %q, want matched response", result.Value)
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

func readHTTPRPCMessage(t *testing.T, request *http.Request) rpcMessage {
	t.Helper()

	defer func() {
		if err := request.Body.Close(); err != nil {
			t.Fatalf("close HTTP JSON-RPC request body: %v", err)
		}
	}()
	var message rpcMessage
	if err := json.NewDecoder(request.Body).Decode(&message); err != nil {
		t.Fatalf("decode HTTP JSON-RPC request: %v", err)
	}
	return message
}

func writeHTTPRPCResponse(t *testing.T, response http.ResponseWriter, id any, result any) {
	t.Helper()

	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(rpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Result:  mustRaw(result),
	}); err != nil {
		t.Fatalf("write HTTP JSON-RPC response: %v", err)
	}
}

func writeHTTPRPCError(t *testing.T, response http.ResponseWriter, id any, message string) {
	t.Helper()

	response.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(response).Encode(rpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: -32601, Message: message},
	}); err != nil {
		t.Fatalf("write HTTP JSON-RPC error: %v", err)
	}
}

func formatSSERPCResponse(t *testing.T, id any, result any) string {
	t.Helper()

	message := rpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Result:  mustRaw(result),
	}
	body, err := json.Marshal(message)
	if err != nil {
		t.Fatalf("marshal SSE JSON-RPC response: %v", err)
	}
	return fmt.Sprintf("event: message\ndata: %s\n\n", body)
}

func formatSSERPCError(t *testing.T, id any, message string) string {
	t.Helper()

	payload := rpcMessage{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: -32601, Message: message},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal SSE JSON-RPC error: %v", err)
	}
	return fmt.Sprintf("event: message\ndata: %s\n\n", body)
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
