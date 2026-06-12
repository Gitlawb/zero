// Package lsp is a direct Language Server Protocol client: ZERO spawns the
// language server itself and speaks LSP JSON-RPC over stdio, so an unattended
// run (cron/CI, no open editor) can still see real compiler diagnostics. This
// file holds the minimal LSP data model and file<->URI helpers; client.go is the
// transport and server.go the process lifecycle.
package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
)

// Position is a zero-based line/character offset in a document.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range is an inclusive-start, exclusive-end span.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a range within a specific document URI.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// DiagnosticSeverity mirrors the LSP severity enum.
type DiagnosticSeverity int

const (
	SeverityError       DiagnosticSeverity = 1
	SeverityWarning     DiagnosticSeverity = 2
	SeverityInformation DiagnosticSeverity = 3
	SeverityHint        DiagnosticSeverity = 4
)

// Diagnostic is one compiler/linter finding for a document.
type Diagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Code     any                `json:"code,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
}

// TextDocumentItem is the payload for textDocument/didOpen.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// InitializeParams is the (trimmed) payload for the LSP initialize request.
type InitializeParams struct {
	ProcessID    int            `json:"processId"`
	RootURI      string         `json:"rootUri"`
	Capabilities map[string]any `json:"capabilities"`
}

// InitializeResult is the (trimmed) initialize response; we only need the
// server's reported capabilities.
type InitializeResult struct {
	Capabilities map[string]any `json:"capabilities"`
}

// PublishDiagnosticsParams is the payload of the textDocument/publishDiagnostics
// notification (consumed in stage 03).
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     int          `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// PathToURI converts an absolute filesystem path to a file:// URI, handling the
// Windows drive-letter form (C:\x -> file:///C:/x). It is the inverse of
// URIToPath; the pair round-trips for any absolute path.
func PathToURI(path string) string {
	if path == "" {
		return ""
	}
	slashed := filepath.ToSlash(path)
	// Windows absolute paths ("C:/x") need a leading slash so the drive becomes
	// part of the URI path: /C:/x.
	if len(slashed) >= 2 && slashed[1] == ':' {
		slashed = "/" + slashed
	}
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	u := url.URL{Scheme: "file", Path: slashed}
	return u.String()
}

// URIToPath converts a file:// URI back to an OS-native absolute path. A
// non-file URI (or unparseable input) is returned unchanged.
func URIToPath(uri string) string {
	if uri == "" {
		return ""
	}
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return uri
	}
	path := parsed.Path
	// Strip the synthetic leading slash from a Windows drive path: /C:/x -> C:/x.
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path)
}
