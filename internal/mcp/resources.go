package mcp

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Gitlawb/zero/internal/workspaceindex"
)

// jsonRPCResourceNotFound is the MCP convention for a resource the server knows
// about but cannot serve (missing or out-of-scope). It is distinct from the
// transport-level method-not-found and parameter codes already defined.
const jsonRPCResourceNotFound = -32002

// maxResourceBytes caps a single resources/read so a remote client cannot drive
// an unbounded allocation by pointing at an enormous file. It mirrors the
// transport's framing limit.
const maxResourceBytes = maxMessageBytes

// Resource describes a workspace file advertised through resources/list. Only
// the fields ZERO populates are emitted; pagination cursors and icons are not
// used.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	MimeType    string `json:"mimeType,omitempty"`
	Description string `json:"description,omitempty"`
}

// ResourceContents is one entry in a resources/read result. Exactly one of Text
// or Blob is populated: Text for UTF-8 files, base64 Blob for binary files.
type ResourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"`
}

// resourceRoots returns the absolute roots the server is allowed to expose.
// When a PathScope is configured its roots are authoritative (workspace root
// first, extra roots after); otherwise enumeration is confined to the single
// workspace root. Roots are cleaned to absolute paths so prefix containment
// checks in resolveResourcePath are reliable.
func (server toolServer) resourceRoots() []string {
	var raw []string
	if server.scope != nil {
		raw = server.scope.Roots()
	}
	if len(raw) == 0 {
		raw = []string{server.workspaceRoot}
	}
	roots := make([]string, 0, len(raw))
	for _, root := range raw {
		trimmed := strings.TrimSpace(root)
		if trimmed == "" {
			continue
		}
		if absolute, err := filepath.Abs(trimmed); err == nil {
			roots = append(roots, filepath.Clean(absolute))
		}
	}
	return roots
}

// listResources enumerates every in-scope, non-binary-by-extension file across
// the allowed roots and renders them as MCP resources. It reuses the shared
// workspace scanner so gitignore-style exclusions (.git, node_modules, vendor,
// binary extensions, ...) match the rest of ZERO instead of re-walking.
func (server toolServer) listResources() []Resource {
	roots := server.resourceRoots()
	resources := make([]Resource, 0)
	seen := map[string]struct{}{}
	for _, root := range roots {
		// MaxDepth -1 selects the scanner's default depth (Options{} would mean
		// root files only). MaxFiles 0 selects the default file cap.
		summary, err := workspaceindex.Scan(root, workspaceindex.Options{MaxDepth: -1})
		if err != nil {
			// A partial scan still yields the files it reached; skip a root that
			// could not be read at all rather than failing the whole listing.
			if len(summary.Files) == 0 {
				continue
			}
		}
		for _, file := range summary.Files {
			absolute := filepath.Join(root, filepath.FromSlash(file.Path))
			uri := fileURI(absolute)
			if _, ok := seen[uri]; ok {
				continue
			}
			seen[uri] = struct{}{}
			resources = append(resources, Resource{
				URI:         uri,
				Name:        file.Path,
				MimeType:    mimeTypeForPath(file.Path),
				Description: resourceDescription(file),
			})
		}
	}
	return resources
}

func resourceDescription(file workspaceindex.File) string {
	if file.Language != "" {
		return file.Language + " file"
	}
	return ""
}

// readResource resolves a resources/read URI to an in-scope absolute path and
// returns its contents. It enforces scope (no traversal, no out-of-scope
// absolute paths) and never writes. Binary files come back as a base64 blob.
func (server toolServer) readResource(rawParams json.RawMessage) ([]ResourceContents, int, error) {
	var params struct {
		URI string `json:"uri"`
	}
	if len(rawParams) > 0 {
		if err := json.Unmarshal(rawParams, &params); err != nil {
			return nil, jsonRPCInvalidParams, fmt.Errorf("invalid resources/read params: %w", err)
		}
	}
	uri := strings.TrimSpace(params.URI)
	if uri == "" {
		return nil, jsonRPCInvalidParams, errors.New("resources/read requires a uri")
	}

	requested, err := pathFromURI(uri)
	if err != nil {
		return nil, jsonRPCInvalidParams, err
	}

	absolute, err := server.resolveResourcePath(requested)
	if err != nil {
		// Out-of-scope and traversal attempts are reported as not-found with no
		// contents so a remote client learns nothing about the host filesystem
		// outside the granted roots.
		return nil, jsonRPCResourceNotFound, err
	}

	info, err := os.Stat(absolute)
	if err != nil {
		return nil, jsonRPCResourceNotFound, fmt.Errorf("resource not found: %s", uri)
	}
	if info.IsDir() {
		return nil, jsonRPCInvalidParams, fmt.Errorf("resource is a directory, not a file: %s", uri)
	}
	if info.Size() > maxResourceBytes {
		return nil, jsonRPCInvalidParams, fmt.Errorf("resource exceeds %d-byte limit: %s", maxResourceBytes, uri)
	}

	data, err := os.ReadFile(absolute)
	if err != nil {
		return nil, jsonRPCResourceNotFound, fmt.Errorf("resource not found: %s", uri)
	}

	contents := ResourceContents{
		URI:      fileURI(absolute),
		MimeType: mimeTypeForPath(absolute),
	}
	if looksBinary(absolute, data) {
		contents.Blob = base64.StdEncoding.EncodeToString(data)
		if contents.MimeType == "" || strings.HasPrefix(contents.MimeType, "text/") {
			contents.MimeType = "application/octet-stream"
		}
	} else {
		contents.Text = string(data)
		if contents.MimeType == "" {
			contents.MimeType = "text/plain; charset=utf-8"
		}
	}
	return []ResourceContents{contents}, 0, nil
}

// resolveResourcePath maps a requested absolute path to a real path inside an
// allowed root, rejecting traversal and out-of-scope locations. It mirrors the
// containment checks the scoped file tools use: the cleaned, symlink-resolved
// path must live within one of the roots (or equal a root).
func (server toolServer) resolveResourcePath(requested string) (string, error) {
	if !filepath.IsAbs(requested) {
		return "", errors.New("resource uri must be an absolute file path")
	}
	cleaned := filepath.Clean(requested)

	for _, root := range server.resourceRoots() {
		if containedInRoot(root, cleaned) {
			return cleaned, nil
		}
		// Resolve symlinks so an in-scope symlink that still lands inside a root
		// is accepted, while a link escaping the roots is rejected.
		if resolved, err := filepath.EvalSymlinks(cleaned); err == nil && containedInRoot(root, resolved) {
			return resolved, nil
		}
	}
	return "", errors.New("resource is outside the allowed workspace scope")
}

// containedInRoot reports whether target is root itself or lies beneath it,
// using a separator-aware prefix check so /a/bc is not treated as inside /a/b.
func containedInRoot(root string, target string) bool {
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	if target == root {
		return true
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return !filepath.IsAbs(rel)
}

// fileURI renders an absolute path as a file:// URI. The path is slash-normalized
// and, on Windows where absolute paths start with a drive letter, gains the
// extra leading slash file URIs require.
func fileURI(absolute string) string {
	slashed := filepath.ToSlash(absolute)
	if !strings.HasPrefix(slashed, "/") {
		slashed = "/" + slashed
	}
	return "file://" + slashed
}

// pathFromURI extracts an absolute filesystem path from a resource URI. It
// accepts file:// URIs (the scheme resources/list advertises); any other scheme
// is rejected so a client cannot smuggle in an unexpected locator.
func pathFromURI(uri string) (string, error) {
	const scheme = "file://"
	if !strings.HasPrefix(uri, scheme) {
		return "", fmt.Errorf("unsupported resource uri scheme: %s", uri)
	}
	rest := strings.TrimPrefix(uri, scheme)
	// file:///abs -> /abs ; file://host/abs is not supported (no remote hosts).
	if rest == "" {
		return "", errors.New("resource uri has no path")
	}
	// Drop an optional empty authority (file:///path) but reject a real host.
	if !strings.HasPrefix(rest, "/") {
		if slash := strings.IndexByte(rest, '/'); slash >= 0 {
			return "", fmt.Errorf("resource uri host is not supported: %s", uri)
		}
		return "", fmt.Errorf("unsupported resource uri: %s", uri)
	}
	decoded := filepath.FromSlash(rest)
	// On Windows a file URI like file:///C:/x yields /C:/x; strip the leading
	// slash so it becomes a valid drive-rooted path.
	if len(decoded) >= 3 && decoded[0] == filepath.Separator && decoded[2] == ':' {
		decoded = decoded[1:]
	}
	return decoded, nil
}

// mimeTypeForPath maps a file extension to a MIME type, falling back to text for
// known source/text extensions the standard library does not register.
func mimeTypeForPath(file string) string {
	ext := strings.ToLower(path.Ext(filepath.ToSlash(file)))
	if ext == "" {
		return "text/plain; charset=utf-8"
	}
	if mimeType := mime.TypeByExtension(ext); mimeType != "" {
		return mimeType
	}
	switch ext {
	case ".go", ".rs", ".py", ".rb", ".sh", ".bash", ".zsh", ".c", ".h", ".cc",
		".cpp", ".hpp", ".java", ".kt", ".swift", ".php", ".lua", ".sql", ".proto",
		".tf", ".dart", ".ex", ".exs", ".vue", ".svelte", ".toml":
		return "text/plain; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}

// looksBinary decides whether a file's contents should be returned as a base64
// blob. It treats known-binary extensions and any non-UTF-8 / NUL-containing
// payload as binary; everything else is served as text.
func looksBinary(file string, data []byte) bool {
	if workspaceindex.LooksBinaryPath(file) {
		return true
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return true
	}
	if !utf8.Valid(data) {
		return true
	}
	// Sniff the leading bytes as a final guard for content the extension did not
	// reveal (e.g. an unknown-extension binary).
	sniff := data
	if len(sniff) > 512 {
		sniff = sniff[:512]
	}
	contentType := http.DetectContentType(sniff)
	return strings.HasPrefix(contentType, "application/octet-stream")
}
