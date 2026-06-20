package lsp

import (
	"context"
	"encoding/json"
	"fmt"
)

// NavOp is a supported LSP navigation operation.
type NavOp string

const (
	NavDefinition      NavOp = "definition"
	NavReferences      NavOp = "references"
	NavImplementation  NavOp = "implementation"
	NavWorkspaceSymbol NavOp = "workspace_symbol"
)

// SymbolResult is one workspace-symbol match (name + where it lives).
type SymbolResult struct {
	Name     string
	Kind     string
	Location Location
}

// NavRequest describes a navigation query. For the position-based ops
// (definition/references/implementation) Path + Line + Character are required
// (1-based, as the agent sees file:line:col). For workspace_symbol only Query
// is used. IncludeDeclaration applies to references.
type NavRequest struct {
	Op                 NavOp
	Path               string
	Line               int // 1-based
	Character          int // 1-based
	Query              string
	Text               string // current file content, for didOpen sync
	IncludeDeclaration bool
}

// Navigate runs an LSP navigation request and returns the resulting locations
// (for the position ops) or symbols (for workspace_symbol). A file whose
// extension has no available server, or a server lacking the capability,
// degrades to an empty result with ok=false rather than an error — LSP is an
// opportunistic layer. A genuine protocol/transport failure returns an error.
func (m *Manager) Navigate(ctx context.Context, req NavRequest) (locations []Location, symbols []SymbolResult, ok bool, err error) {
	switch req.Op {
	case NavWorkspaceSymbol:
		return m.workspaceSymbol(ctx, req)
	case NavDefinition, NavReferences, NavImplementation:
		return m.positionNav(ctx, req)
	default:
		return nil, nil, false, fmt.Errorf("lsp: unknown navigation op %q", req.Op)
	}
}

func (m *Manager) positionNav(ctx context.Context, req NavRequest) ([]Location, []SymbolResult, bool, error) {
	command, served := ServerFor(req.Path)
	if !served {
		return nil, nil, false, nil
	}
	languageID, _ := LanguageID(req.Path)
	abs := m.absPath(req.Path)
	uri := PathToURI(abs)

	sess, err := m.sessionFor(ctx, command)
	if err != nil {
		if isServerUnavailable(err) {
			return nil, nil, false, nil
		}
		return nil, nil, false, err
	}
	if err := sess.sync(ctx, abs, languageID, req.Text); err != nil {
		return nil, nil, false, err
	}

	// LSP positions are 0-based; the tool's API is 1-based (file:line:col).
	pos := Position{Line: maxZero(req.Line - 1), Character: maxZero(req.Character - 1)}
	method, params := positionRequest(req.Op, uri, pos, req.IncludeDeclaration)

	raw, err := sess.client.Call(ctx, method, params)
	if err != nil {
		return nil, nil, false, err
	}
	locs, err := decodeLocations(raw)
	if err != nil {
		return nil, nil, false, err
	}
	return locs, nil, true, nil
}

func (m *Manager) workspaceSymbol(ctx context.Context, req NavRequest) ([]Location, []SymbolResult, bool, error) {
	// workspace/symbol needs a running server; pick one by the request path if
	// given, else there is nothing to query against.
	command, served := ServerFor(req.Path)
	if !served {
		return nil, nil, false, nil
	}
	sess, err := m.sessionFor(ctx, command)
	if err != nil {
		if isServerUnavailable(err) {
			return nil, nil, false, nil
		}
		return nil, nil, false, err
	}
	// Open the anchor file so the server has the workspace loaded.
	if req.Text != "" {
		languageID, _ := LanguageID(req.Path)
		_ = sess.sync(ctx, m.absPath(req.Path), languageID, req.Text)
	}
	raw, err := sess.client.Call(ctx, "workspace/symbol", map[string]any{"query": req.Query})
	if err != nil {
		return nil, nil, false, err
	}
	symbols, err := decodeSymbols(raw)
	if err != nil {
		return nil, nil, false, err
	}
	return nil, symbols, true, nil
}

func positionRequest(op NavOp, uri string, pos Position, includeDecl bool) (string, any) {
	base := map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": pos.Line, "character": pos.Character},
	}
	switch op {
	case NavDefinition:
		return "textDocument/definition", base
	case NavImplementation:
		return "textDocument/implementation", base
	case NavReferences:
		base["context"] = map[string]any{"includeDeclaration": includeDecl}
		return "textDocument/references", base
	default:
		return "textDocument/definition", base
	}
}

// decodeLocations handles the three shapes definition/references can return: a
// single Location, an array of Location, or an array of LocationLink.
func decodeLocations(raw json.RawMessage) ([]Location, error) {
	trimmed := trimJSON(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	if trimmed[0] == '[' {
		// Try []Location first, then []LocationLink (targetUri/targetRange).
		var locs []Location
		if err := json.Unmarshal(trimmed, &locs); err == nil && allHaveURI(locs) {
			return locs, nil
		}
		var links []locationLink
		if err := json.Unmarshal(trimmed, &links); err != nil {
			return nil, err
		}
		out := make([]Location, 0, len(links))
		for _, l := range links {
			out = append(out, Location{URI: l.TargetURI, Range: l.TargetRange})
		}
		return out, nil
	}
	var single Location
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return nil, err
	}
	if single.URI == "" {
		return nil, nil
	}
	return []Location{single}, nil
}

type locationLink struct {
	TargetURI   string `json:"targetUri"`
	TargetRange Range  `json:"targetRange"`
}

type workspaceSymbol struct {
	Name     string   `json:"name"`
	Kind     int      `json:"kind"`
	Location Location `json:"location"`
}

func decodeSymbols(raw json.RawMessage) ([]SymbolResult, error) {
	trimmed := trimJSON(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	var syms []workspaceSymbol
	if err := json.Unmarshal(trimmed, &syms); err != nil {
		return nil, err
	}
	out := make([]SymbolResult, 0, len(syms))
	for _, s := range syms {
		out = append(out, SymbolResult{Name: s.Name, Kind: symbolKindName(s.Kind), Location: s.Location})
	}
	return out, nil
}

func allHaveURI(locs []Location) bool {
	for _, l := range locs {
		if l.URI == "" {
			return false
		}
	}
	return len(locs) > 0
}

func maxZero(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func trimJSON(raw json.RawMessage) json.RawMessage {
	s := 0
	for s < len(raw) && (raw[s] == ' ' || raw[s] == '\n' || raw[s] == '\t' || raw[s] == '\r') {
		s++
	}
	return raw[s:]
}

// symbolKindName maps the LSP SymbolKind enum to a short label.
func symbolKindName(kind int) string {
	switch kind {
	case 5:
		return "class"
	case 6:
		return "method"
	case 8:
		return "field"
	case 11:
		return "interface"
	case 12:
		return "function"
	case 13:
		return "variable"
	case 14:
		return "constant"
	case 23:
		return "struct"
	default:
		return "symbol"
	}
}
