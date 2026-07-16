package agent

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	maxUnflattenIndex             = 10_000
	maxUnflattenMaterializedSlots = maxUnflattenIndex + 1
)

// unflattenToolArguments repairs tool-call argument objects that some models emit
// in STREAMING mode using flattened dotted/bracketed path keys instead of a nested
// structure. Gemini on the GitHub Copilot endpoint is the known offender: for a
// tool whose schema expects {"plan":[{"content":"x","notes":"y"}]} it streams
//
//	{"plan[0].content":"x","plan[0].notes":"y"}
//
// which decodes to a flat map with no "plan" array, so a tool like update_plan
// rejects it ("plan must be an array") and the run stalls. The same model returns
// correct nesting in NON-streaming mode, so this is purely a streaming artifact.
//
// This parses each top-level key as a path of object keys and array indices and
// rebuilds the nested value. It is deliberately conservative: the ORIGINAL string
// is returned unchanged when the payload is not a JSON object, has no flattened
// keys, contains a key it cannot parse, or produces a structural conflict (a path
// used as both a leaf and a container, or as both an object and an array). That
// keeps normal payloads and every other provider byte-for-byte unaffected.
func unflattenToolArguments(arguments string) string {
	trimmed := strings.TrimSpace(arguments)
	if !strings.HasPrefix(trimmed, "{") {
		return arguments
	}
	var flat map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &flat); err != nil {
		return arguments
	}
	if !hasFlattenedKey(flat) {
		return arguments
	}

	root := &unflattenNode{}
	for key, raw := range flat {
		segments, ok := parseFlatKey(key)
		if !ok {
			return arguments
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return arguments
		}
		if !root.set(segments, value) {
			return arguments
		}
	}

	remainingSlots := maxUnflattenMaterializedSlots
	built, ok := root.build(&remainingSlots)
	if !ok {
		return arguments
	}
	out, err := json.Marshal(built)
	if err != nil {
		return arguments
	}
	return string(out)
}

// hasFlattenedKey reports whether any top-level key carries a nested path, i.e.
// contains a "." object separator or an "[" array-index marker.
func hasFlattenedKey(flat map[string]json.RawMessage) bool {
	for key := range flat {
		if strings.ContainsAny(key, ".[") {
			return true
		}
	}
	return false
}

// flatSegment is one step of a flattened key path: either an object key or an
// array index.
type flatSegment struct {
	key     string
	index   int
	isIndex bool
}

// parseFlatKey splits a flattened key such as `plan[0].content` into ordered
// segments (["plan", 0, "content"]). ok is false for any shape it does not
// understand (empty segment, unbalanced/ non-numeric brackets, leading dot),
// so the caller can safely leave the payload untouched.
func parseFlatKey(key string) (segments []flatSegment, ok bool) {
	i := 0
	n := len(key)
	first := true
	for i < n {
		switch key[i] {
		case '[':
			end := strings.IndexByte(key[i:], ']')
			if end < 0 {
				return nil, false
			}
			end += i
			idxStr := key[i+1 : end]
			idx, err := strconv.Atoi(idxStr)
			if err != nil || idx < 0 || idx > maxUnflattenIndex {
				return nil, false
			}
			segments = append(segments, flatSegment{index: idx, isIndex: true})
			i = end + 1
			first = false
		case '.':
			if first {
				return nil, false
			}
			i++ // skip the separator; the key follows
			start := i
			for i < n && key[i] != '.' && key[i] != '[' {
				i++
			}
			if i == start {
				return nil, false
			}
			segments = append(segments, flatSegment{key: key[start:i]})
			first = false
		default:
			start := i
			for i < n && key[i] != '.' && key[i] != '[' {
				i++
			}
			if i == start {
				return nil, false
			}
			segments = append(segments, flatSegment{key: key[start:i]})
			first = false
		}
	}
	if len(segments) == 0 {
		return nil, false
	}
	return segments, true
}

// unflattenNode is a scratch tree used to rebuild nested structure from flat
// keys. A node is exactly one of: a leaf (isLeaf), an object (objChildren), or an
// array (arrChildren). Mixing kinds is a conflict that aborts un-flattening.
type unflattenNode struct {
	isLeaf      bool
	value       any
	objChildren map[string]*unflattenNode
	arrChildren map[int]*unflattenNode
}

// set walks/creates the path described by segments and stores value at the leaf.
// It returns false on a structural conflict so the caller can bail out safely.
func (node *unflattenNode) set(segments []flatSegment, value any) bool {
	if len(segments) == 0 {
		if node.objChildren != nil || node.arrChildren != nil {
			return false
		}
		if node.isLeaf {
			return false // duplicate assignment to the same path
		}
		node.isLeaf = true
		node.value = value
		return true
	}
	if node.isLeaf {
		return false
	}
	segment := segments[0]
	if segment.isIndex {
		if node.objChildren != nil {
			return false
		}
		if node.arrChildren == nil {
			node.arrChildren = map[int]*unflattenNode{}
		}
		child := node.arrChildren[segment.index]
		if child == nil {
			child = &unflattenNode{}
			node.arrChildren[segment.index] = child
		}
		return child.set(segments[1:], value)
	}
	if node.arrChildren != nil {
		return false
	}
	if node.objChildren == nil {
		node.objChildren = map[string]*unflattenNode{}
	}
	child := node.objChildren[segment.key]
	if child == nil {
		child = &unflattenNode{}
		node.objChildren[segment.key] = child
	}
	return child.set(segments[1:], value)
}

// build materializes the scratch tree into plain map[string]any / []any values.
// Array gaps (missing indices) are filled with nil to preserve positions.
func (node *unflattenNode) build(remainingSlots *int) (any, bool) {
	if node.isLeaf {
		return node.value, true
	}
	if node.objChildren != nil {
		out := make(map[string]any, len(node.objChildren))
		for key, child := range node.objChildren {
			built, ok := child.build(remainingSlots)
			if !ok {
				return nil, false
			}
			out[key] = built
		}
		return out, true
	}
	if node.arrChildren != nil {
		max := -1
		for idx := range node.arrChildren {
			if idx > max {
				max = idx
			}
		}
		slots := max + 1
		if slots > *remainingSlots {
			return nil, false
		}
		*remainingSlots -= slots
		out := make([]any, slots)
		for idx, child := range node.arrChildren {
			built, ok := child.build(remainingSlots)
			if !ok {
				return nil, false
			}
			out[idx] = built
		}
		return out, true
	}
	// An empty node has no assigned value or children; treat as empty object.
	return map[string]any{}, true
}
