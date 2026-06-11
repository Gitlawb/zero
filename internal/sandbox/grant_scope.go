package sandbox

import (
	"path/filepath"
	"strings"
)

// ScopeKind classifies what a grant's scope covers. An empty kind is a tool-wide
// grant (it authorizes the whole tool, the pre-scoping behavior); a file scope
// matches one exact path; a dir scope matches the directory and any descendant.
type ScopeKind string

const (
	ScopeToolWide ScopeKind = ""
	ScopeFile     ScopeKind = "file"
	ScopeDir      ScopeKind = "dir"
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
}

// DeriveScope inspects a tool call's arguments and returns the raw (un-resolved)
// scope string and its kind. It returns ("", ScopeToolWide) when no path-like
// argument is present, the value is not a string, or the value points at the
// workspace root (".") — in those cases the grant is plainly tool-wide.
func DeriveScope(toolName string, args map[string]any) (string, ScopeKind) {
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

// grantCovers reports whether a stored grant covers a request whose absolute
// scope is reqAbs. A tool-wide grant covers everything (including a tool-wide
// request); a file grant matches its exact path; a dir grant matches the
// directory itself or any descendant. A narrower grant never covers a tool-wide
// request (reqAbs == ""), so such a request re-prompts (fail-safe).
func grantCovers(grant Grant, reqAbs string) bool {
	switch grant.ScopeKind {
	case ScopeToolWide:
		return true
	case ScopeFile:
		return reqAbs != "" && reqAbs == grant.Scope
	case ScopeDir:
		if reqAbs == "" || grant.Scope == "" {
			return false
		}
		if reqAbs == grant.Scope {
			return true
		}
		return strings.HasPrefix(reqAbs, grant.Scope+string(filepath.Separator))
	default:
		return false
	}
}

// scopeSpecificity ranks scope kinds so the most precise covering allow wins when
// several grants match the same request (file > dir > tool-wide).
func scopeSpecificity(kind ScopeKind) int {
	switch kind {
	case ScopeFile:
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
