package sandbox

import (
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

// ScopeKind classifies what a grant's scope covers. An empty kind is a
// tool-wide grant (it authorizes the whole tool, the pre-scoping behavior); a
// file scope matches one exact path; a dir scope matches the directory and any
// descendant; a host scope matches one normalized network host.
type ScopeKind string

const (
	ScopeToolWide ScopeKind = ""
	ScopeFile     ScopeKind = "file"
	ScopeDir      ScopeKind = "dir"
	ScopeHost     ScopeKind = "host"
)

// scopeArgKeys lists the path-like argument keys in priority order (most specific
// first) together with the kind of scope each denotes. This is the single source
// of truth for which arguments carry a scope, shared by the permission-card
// display, grant persistence, and grant matching.
var scopeArgKeys = []struct {
	key  string
	kind ScopeKind
}{
	{"path", ScopeFile},
	{"file", ScopeFile},
	{"directory", ScopeDir},
	{"dir", ScopeDir},
	{"cwd", ScopeDir},
	{"workdir", ScopeDir},
}

// DeriveScope inspects a tool call's arguments and returns the raw
// (un-resolved) scope string and its kind. It returns ("", ScopeToolWide) when
// no scoped argument is present, the value is not a string, or the value points
// at the workspace root (".") -- in those cases the grant is plainly tool-wide.
func DeriveScope(toolName string, args map[string]any) (string, ScopeKind) {
	if toolName == "browser_open" {
		if host, ok := deriveBrowserHostScope(args["url"]); ok {
			return host, ScopeHost
		}
	}
	if toolName == "web_fetch" {
		if host, ok := deriveHostScope(args["url"]); ok {
			return host, ScopeHost
		}
	}
	for _, candidate := range scopeArgKeys {
		value, ok := args[candidate.key].(string)
		if !ok {
			continue
		}
		trimmed := strings.TrimSpace(value)
		// Clean so every root-equivalent spelling ("." / "./" / "./." / "a/..")
		// collapses to the workspace root and surfaces as tool-wide, not a narrower
		// directory grant. Otherwise the same root-level action could persist either
		// a tool-wide or a dir grant depending only on how the path was spelled,
		// re-prompting inconsistently on later root-level requests.
		if trimmed == "" || filepath.Clean(trimmed) == "." {
			continue // workspace root — no extra scope to surface
		}
		return trimmed, candidate.kind
	}
	return "", ScopeToolWide
}

func deriveHostScope(value any) (string, bool) {
	raw, ok := value.(string)
	if !ok {
		return "", false
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return "", false
	}
	host := normalizeHostScope(parsed.Hostname())
	return host, host != ""
}

func deriveBrowserHostScope(value any) (string, bool) {
	raw, ok := value.(string)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" && !strings.Contains(trimmed, "://") {
		if !browserURLLooksLikeBareHost(trimmed) {
			return "", false
		}
		trimmed = "https://" + trimmed
	}
	return deriveHostScope(trimmed)
}

func browserURLLooksLikeBareHost(raw string) bool {
	if raw == "" || strings.HasPrefix(raw, "//") {
		return false
	}
	authority := raw
	if cut := strings.IndexAny(authority, "/?#"); cut >= 0 {
		authority = authority[:cut]
	}
	if authority == "" || strings.Contains(authority, "@") {
		return false
	}
	host := authority
	if strings.HasPrefix(authority, "[") {
		end := strings.Index(authority, "]")
		if end <= 1 {
			return false
		}
		host = authority[1:end]
		if rest := authority[end+1:]; rest != "" {
			if !strings.HasPrefix(rest, ":") || !isDecimalPort(rest[1:]) {
				return false
			}
		}
	} else if strings.Contains(authority, ":") {
		before, after, _ := strings.Cut(authority, ":")
		if before == "" || !isDecimalPort(after) {
			return false
		}
		host = before
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	return host == "localhost" || strings.Contains(host, ".") || net.ParseIP(host) != nil
}

func isDecimalPort(raw string) bool {
	if raw == "" {
		return false
	}
	for _, r := range raw {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// resolveScopeAbs converts a raw scope to an absolute, cleaned path. A relative
// scope is anchored to workspaceRoot; when workspaceRoot is empty it falls back
// to filepath.Abs (process working directory). An empty scope resolves to "".
func resolveScopeAbs(raw string, workspaceRoot string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	if root := strings.TrimSpace(workspaceRoot); root != "" {
		return filepath.Clean(filepath.Join(root, trimmed))
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return abs
	}
	return filepath.Clean(trimmed)
}

func resolveScopeForKind(raw string, kind ScopeKind, workspaceRoot string) string {
	scope, kind := reconcileScope(strings.TrimSpace(raw), kind)
	switch kind {
	case ScopeFile, ScopeDir:
		return resolveScopeAbs(scope, workspaceRoot)
	case ScopeHost:
		return normalizeHostScope(scope)
	default:
		return ""
	}
}

func normalizeHostScope(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if parsed, err := url.Parse(trimmed); err == nil && parsed.Host != "" {
		trimmed = parsed.Hostname()
	} else if host, _, err := net.SplitHostPort(trimmed); err == nil {
		trimmed = host
	}
	trimmed = strings.Trim(trimmed, "[]")
	trimmed = strings.TrimSuffix(trimmed, ".")
	return strings.ToLower(strings.TrimSpace(trimmed))
}

// grantCovers reports whether a stored grant covers a request whose absolute
// scope is reqScope. A tool-wide grant covers everything (including a tool-wide
// request); a file grant matches its exact path; a dir grant matches the
// directory itself or any descendant; a host grant matches its exact normalized
// host. A narrower grant never covers a tool-wide request (reqScope == ""), so
// such a request re-prompts (fail-safe).
func grantCovers(grant Grant, reqScope string) bool {
	switch grant.ScopeKind {
	case ScopeToolWide:
		return true
	case ScopeFile:
		return reqScope != "" && reqScope == grant.Scope
	case ScopeDir:
		if reqScope == "" || grant.Scope == "" {
			return false
		}
		if reqScope == grant.Scope {
			return true
		}
		return strings.HasPrefix(reqScope, grant.Scope+string(filepath.Separator))
	case ScopeHost:
		return reqScope != "" && normalizeHostScope(reqScope) == normalizeHostScope(grant.Scope)
	default:
		return false
	}
}

// scopeSpecificity ranks scope kinds so the most precise covering allow wins when
// several grants match the same request.
func scopeSpecificity(kind ScopeKind) int {
	switch kind {
	case ScopeFile, ScopeHost:
		return 2
	case ScopeDir:
		return 1
	default:
		return 0
	}
}

// moreSpecific reports whether allow grant a should be preferred over b when both
// cover a request: by kind specificity, then longer (deeper) path, then recency.
func moreSpecific(a Grant, b Grant) bool {
	if sa, sb := scopeSpecificity(a.ScopeKind), scopeSpecificity(b.ScopeKind); sa != sb {
		return sa > sb
	}
	if len(a.Scope) != len(b.Scope) {
		return len(a.Scope) > len(b.Scope)
	}
	return a.ApprovedAt > b.ApprovedAt
}
