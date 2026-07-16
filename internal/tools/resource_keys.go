package tools

import (
	"path/filepath"
	"runtime"
	"strings"
)

// Resource-key prefixes used for future conflict detection. Keys are pure
// metadata in this PR — no locks or scheduling are applied.
const (
	ResourceKeyFile       = "file:"
	ResourceKeyDirectory  = "directory:"
	ResourceKeyRepository = "repository:"
	ResourceKeyProcess    = "process:"
	ResourceKeyPTY        = "pty:"
	ResourceKeyBrowser    = "browser:"
	ResourceKeyEndpoint   = "endpoint:"
	ResourceKeySession    = "session:"
	ResourceKeyWorkspace  = "workspace:"
)

// NormalizeResourcePath produces a deterministic path token for resource keys.
// It never touches the filesystem (no EvalSymlinks / Stat) so it cannot cause
// side effects or panic on missing paths. Empty input returns "".
//
// Rules:
//   - Trim space
//   - filepath.Clean
//   - Convert separators to '/'
//   - On Windows, lower-case for case-insensitive comparison
//   - Strip a leading "./"
//   - Never include credentials or query fragments (paths only)
func NormalizeResourcePath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	// Reject obviously secret-bearing URLs from path args — keep only the path
	// component if a scheme-like prefix appears with userinfo.
	if strings.Contains(path, "://") {
		// Network endpoints use endpoint: keys, not file: — refuse to emit
		// file keys for URL-shaped values.
		return ""
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." {
		return "."
	}
	// filepath.ToSlash for stable cross-platform keys in traces/tests.
	normalized := filepath.ToSlash(cleaned)
	normalized = strings.TrimPrefix(normalized, "./")
	if runtime.GOOS == "windows" {
		normalized = strings.ToLower(normalized)
	}
	return normalized
}

// fileResourceKeys extracts a file: resource key from common path argument names.
// Missing or empty path returns nil (not an error); never panics.
func fileResourceKeys(args map[string]any) []string {
	path := firstStringArg(args, "path", "file", "file_path", "filepath", "filename", "target")
	normalized := NormalizeResourcePath(path)
	if normalized == "" {
		return nil
	}
	return []string{ResourceKeyFile + normalized}
}

// directoryResourceKeys extracts a directory: key from path/directory/cwd args.
func directoryResourceKeys(args map[string]any) []string {
	path := firstStringArg(args, "path", "directory", "dir", "cwd", "workdir")
	normalized := NormalizeResourcePath(path)
	if normalized == "" {
		return nil
	}
	return []string{ResourceKeyDirectory + normalized}
}

// endpointResourceKeys extracts an endpoint: key from url/endpoint args.
// Host only is kept when a full URL is provided (no secrets/query/userinfo).
func endpointResourceKeys(args map[string]any) []string {
	raw := firstStringArg(args, "url", "endpoint", "uri")
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	host := resourceHost(raw)
	if host == "" {
		return nil
	}
	return []string{ResourceKeyEndpoint + host}
}

// sessionResourceKeys extracts a session: key from session_id args.
func sessionResourceKeys(args map[string]any) []string {
	id := firstStringArg(args, "session_id", "sessionId", "session")
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	// Never put free-form content in keys — IDs only, bounded length.
	if len(id) > 128 {
		id = id[:128]
	}
	return []string{ResourceKeySession + id}
}

// processResourceKeys extracts process:/pty: keys for retained process tools.
func processResourceKeys(args map[string]any) []string {
	id := firstStringArg(args, "session_id", "sessionId", "process_id", "pid", "id")
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	if len(id) > 128 {
		id = id[:128]
	}
	return []string{ResourceKeyProcess + id}
}

// workspaceResourceKeys returns a single workspace: root marker when the call
// affects the whole workspace (e.g. glob without a path root).
func workspaceResourceKeys(_ map[string]any) []string {
	return []string{ResourceKeyWorkspace + "root"}
}

// multiFileResourceKeys collects path and paths[] arguments into file: keys.
func multiFileResourceKeys(args map[string]any) []string {
	var keys []string
	keys = append(keys, fileResourceKeys(args)...)
	if raw, ok := args["paths"]; ok {
		switch typed := raw.(type) {
		case []string:
			for _, p := range typed {
				if n := NormalizeResourcePath(p); n != "" {
					keys = append(keys, ResourceKeyFile+n)
				}
			}
		case []any:
			for _, item := range typed {
				if s, ok := item.(string); ok {
					if n := NormalizeResourcePath(s); n != "" {
						keys = append(keys, ResourceKeyFile+n)
					}
				}
			}
		}
	}
	return uniqueKeys(keys)
}

func firstStringArg(args map[string]any, keys ...string) string {
	if args == nil {
		return ""
	}
	for _, key := range keys {
		value, ok := args[key]
		if !ok || value == nil {
			continue
		}
		if s, ok := value.(string); ok {
			return s
		}
	}
	return ""
}

// resourceHost returns a lower-cased host for endpoint keys, stripping
// userinfo, path, query, and fragment. Returns "" when not parseable safely.
func resourceHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Strip scheme.
	if idx := strings.Index(raw, "://"); idx >= 0 {
		raw = raw[idx+3:]
	}
	// Strip userinfo.
	if at := strings.LastIndex(raw, "@"); at >= 0 {
		raw = raw[at+1:]
	}
	// Strip path/query/fragment.
	if slash := strings.IndexAny(raw, "/?#"); slash >= 0 {
		raw = raw[:slash]
	}
	// Strip port for stable host keys (optional — keep host:port for uniqueness).
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.Contains(raw, "@") {
		return ""
	}
	return strings.ToLower(raw)
}

func uniqueKeys(keys []string) []string {
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(keys))
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}
