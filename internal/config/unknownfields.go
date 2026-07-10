package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// unknownFieldIssues scans data for JSON object keys that do not correspond to
// any known config field, recursing into nested objects, arrays, and maps so
// that typos such as "maxTurn" or "sandbox.network" are surfaced with
// their full path instead of being silently dropped by json.Unmarshal.
//
// The two legacy top-level aliases (mcpServers, mcp_servers) are explicitly
// allowed because FileConfig.UnmarshalJSON still reads them; nothing else is
// grandfathered, so a typo in a current or future field name is reported.
// Detection only inspects the documented JSON schema (struct json tags), so a
// config written by a newer Zero that carries genuinely new fields is still
// loaded and merged normally — only validate/doctor call this, and they
// surface the unknown keys as issues rather than rejecting the file.
func unknownFieldIssues(data []byte) []Issue {
	var issues []Issue
	collectUnknownFields(reflect.TypeOf(FileConfig{}), data, "", []string{"mcpServers", "mcp_servers"}, &issues)
	return issues
}

func collectUnknownFields(t reflect.Type, raw json.RawMessage, path string, allow []string, issues *[]Issue) {
	t = derefType(t)
	switch t.Kind() {
	case reflect.Struct:
		var present map[string]json.RawMessage
		if err := json.Unmarshal(raw, &present); err != nil {
			return
		}
		known := knownJSONFields(t)
		allowSet := toSet(allow)
		for key, val := range present {
			if allowSet[key] {
				continue
			}
			if ft, ok := known[key]; ok {
				collectUnknownFields(ft, val, joinPath(path, key), nil, issues)
			} else {
				*issues = append(*issues, Issue{
					FieldPath: joinPath(path, key),
					Message:   unknownFieldMessage(joinPath(path, key), key, keysOf(known)),
				})
			}
		}
	case reflect.Slice, reflect.Array:
		var elems []json.RawMessage
		if err := json.Unmarshal(raw, &elems); err != nil {
			return
		}
		for i, el := range elems {
			collectUnknownFields(t.Elem(), el, fmt.Sprintf("%s[%d]", path, i), nil, issues)
		}
	case reflect.Map:
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return
		}
		for k, v := range m {
			collectUnknownFields(t.Elem(), v, joinPath(path, k), nil, issues)
		}
	}
}

// knownJSONFields returns the JSON field names of a struct (and their Go
// types, for recursion) by reading each field's json tag. Fields without a
// json tag (unexported helpers such as *Set markers) are omitted: they never
// appear in serialized config and must not be treated as valid keys.
func knownJSONFields(t reflect.Type) map[string]reflect.Type {
	t = derefType(t)
	out := make(map[string]reflect.Type, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		if name == "" {
			continue
		}
		out[name] = f.Type
	}
	return out
}

func derefType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	return t
}

func joinPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

func toSet(items []string) map[string]bool {
	s := make(map[string]bool, len(items))
	for _, it := range items {
		s[it] = true
	}
	return s
}

func keysOf(m map[string]reflect.Type) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func unknownFieldMessage(fullPath, key string, candidates []string) string {
	if near := nearestKey(key, candidates); near != "" {
		return fmt.Sprintf("unknown config field %q; did you mean %q?", fullPath, suggestPath(fullPath, near))
	}
	return fmt.Sprintf("unknown config field %q", fullPath)
}

// suggestPath rewrites only the leaf of fullPath with the suggested key so the
// hint reads "sandbox.network" rather than just "network".
func suggestPath(fullPath, suggestedLeaf string) string {
	if i := strings.LastIndex(fullPath, "."); i >= 0 {
		return fullPath[:i+1] + suggestedLeaf
	}
	return suggestedLeaf
}

func nearestKey(key string, candidates []string) string {
	best := ""
	bestDist := -1
	for _, c := range candidates {
		d := editDistance(key, c)
		if bestDist == -1 || d < bestDist {
			bestDist = d
			best = c
		}
	}
	if bestDist >= 0 && bestDist <= 2 && bestDist < len(key) {
		return best
	}
	return ""
}

func editDistance(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur := make([]int, lb+1)
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
