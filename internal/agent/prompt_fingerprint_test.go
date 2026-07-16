package agent

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// TestComputePrefixFingerprintStableAcrossCalls asserts the headline property
// the trace is meant to expose: two ComputePrefixFingerprint calls over the
// same Options and the same exposed tool list produce byte-identical hashes.
// This is the regression catch for "did anyone introduce non-determinism into
// the prompt prefix."
func TestComputePrefixFingerprintStableAcrossCalls(t *testing.T) {
	opts := Options{Cwd: t.TempDir(), SystemPrompt: "core"}
	exposed := []zeroruntime.ToolDefinition{
		{Name: "read_file", Parameters: map[string]any{"type": "object"}},
		{Name: "grep", Parameters: map[string]any{"type": "object"}},
	}
	first := ComputePrefixFingerprint(opts, exposed)
	second := ComputePrefixFingerprint(opts, exposed)
	if first != second {
		t.Fatalf("fingerprint must be stable across calls with the same input:\n  first=%+v\n second=%+v", first, second)
	}
	if first.CompletePrefixHash == "" {
		t.Fatalf("CompletePrefixHash must be non-empty for a non-trivial prompt")
	}
}

// TestComputePrefixFingerprintToolsHashIsNameSetSensitive asserts the
// name-set sensitivity of the ToolsHash. The test name used to read as
// "reorder changes hash" but the assertion is the opposite: the hash is
// name-set sensitive, not name-order sensitive (toolSubstrings sorts
// names before hashing). The partitioner is expected to produce a stable
// order, so a name-order change is not a hash signal — a name-set change
// is. The test asserts both: permutation of the same tools produces the
// same hash, and adding a tool produces a different hash.
func TestComputePrefixFingerprintToolsHashIsNameSetSensitive(t *testing.T) {
	opts := Options{Cwd: t.TempDir(), SystemPrompt: "core"}
	a := []zeroruntime.ToolDefinition{
		{Name: "read_file", Parameters: map[string]any{"schema": "schema-read"}},
		{Name: "grep", Parameters: map[string]any{"schema": "schema-grep"}},
	}
	b := []zeroruntime.ToolDefinition{
		{Name: "grep", Parameters: map[string]any{"schema": "schema-grep"}},
		{Name: "read_file", Parameters: map[string]any{"schema": "schema-read"}},
	}
	// toolSubstrings sorts names internally, so the ToolsHash is identical
	// for a permutation of the same tools. We assert that here to document
	// the contract: name-order independence, name-set sensitivity.
	fpA := ComputePrefixFingerprint(opts, a)
	fpB := ComputePrefixFingerprint(opts, b)
	if fpA.ToolsHash != fpB.ToolsHash {
		t.Fatalf("ToolsHash must be name-set sensitive, not name-order sensitive: a=%s b=%s", fpA.ToolsHash, fpB.ToolsHash)
	}
	// A different tool set must produce a different ToolsHash.
	c := append([]zeroruntime.ToolDefinition{}, a...)
	c = append(c, zeroruntime.ToolDefinition{Name: "bash", Parameters: map[string]any{"schema": "schema-bash"}})
	fpC := ComputePrefixFingerprint(opts, c)
	if fpA.ToolsHash == fpC.ToolsHash {
		t.Fatalf("ToolsHash must change when the tool set changes: a=%s c=%s", fpA.ToolsHash, fpC.ToolsHash)
	}
}

// TestComputePrefixFingerprintSchemaChangeChangesSchemaHash asserts that a
// change to a tool's Parameters schema is visible in the SchemaHash. A schema
// edit that should be cache-stable (e.g. field reorder, doc tweak) will move
// the hash and surface in the trace; a schema edit that legitimately
// invalidates the cache also moves the hash, which is the right behavior.
func TestComputePrefixFingerprintSchemaChangeChangesSchemaHash(t *testing.T) {
	opts := Options{Cwd: t.TempDir(), SystemPrompt: "core"}
	a := []zeroruntime.ToolDefinition{
		{Name: "read_file", Parameters: map[string]any{"version": "v1"}},
	}
	b := []zeroruntime.ToolDefinition{
		{Name: "read_file", Parameters: map[string]any{"version": "v2"}},
	}
	fpA := ComputePrefixFingerprint(opts, a)
	fpB := ComputePrefixFingerprint(opts, b)
	if fpA.SchemaHash == fpB.SchemaHash {
		t.Fatalf("SchemaHash must change when tool Parameters change: a=%s b=%s", fpA.SchemaHash, fpB.SchemaHash)
	}
}

// TestComputePrefixFingerprintSystemPromptChangeChangesBaseHash asserts the
// most common drift path: a system-prompt edit (e.g. an agent.md update, a
// model addendum change) must move the base-instructions hash, which moves
// the complete-prefix hash, which is what the trace is for.
func TestComputePrefixFingerprintSystemPromptChangeChangesBaseHash(t *testing.T) {
	optsA := Options{Cwd: t.TempDir(), SystemPrompt: "core v1"}
	optsB := Options{Cwd: t.TempDir(), SystemPrompt: "core v2"}
	exposed := []zeroruntime.ToolDefinition{{Name: "noop", Parameters: map[string]any{}}}
	fpA := ComputePrefixFingerprint(optsA, exposed)
	fpB := ComputePrefixFingerprint(optsB, exposed)
	if fpA.BaseInstructionsHash == fpB.BaseInstructionsHash {
		t.Fatalf("BaseInstructionsHash must change when SystemPrompt changes: a=%s b=%s", fpA.BaseInstructionsHash, fpB.BaseInstructionsHash)
	}
	if fpA.CompletePrefixHash == fpB.CompletePrefixHash {
		t.Fatalf("CompletePrefixHash must change when any sub-hash changes: a=%s b=%s", fpA.CompletePrefixHash, fpB.CompletePrefixHash)
	}
}

// TestComputePrefixFingerprintCompleteIsAggregateOfSubHashes asserts the
// canonical-join property: CompletePrefixHash must depend on every sub-hash.
// This is the regression catch for "did anyone reorder or drop a sub-hash
// from the canonical join without updating CompletePrefixHash."
func TestComputePrefixFingerprintCompleteIsAggregateOfSubHashes(t *testing.T) {
	opts := Options{Cwd: t.TempDir(), SystemPrompt: "core"}
	exposed := []zeroruntime.ToolDefinition{{Name: "read_file", Parameters: map[string]any{"k": "v"}}}
	fp := ComputePrefixFingerprint(opts, exposed)
	// Manually compute what the canonical join should be.
	expected := sha256hex(strings.Join([]string{
		fp.BaseInstructionsHash,
		fp.ConfirmationPolicyHash,
		fp.ProjectContextHash,
		fp.SkillsHash,
		fp.ToolsHash,
		fp.SchemaHash,
	}, "|"))
	if fp.CompletePrefixHash != expected {
		t.Fatalf("CompletePrefixHash must equal sha256hex of the canonical join of the other six:\n  got:      %s\n  expected: %s", fp.CompletePrefixHash, expected)
	}
}

// TestBuildPromptSubstringsDefaultOptions asserts the invariants of the
// substrings helper for default Options: which substrings are non-empty,
// which are empty, and that the substring-to-hash round-trip is lossless.
func TestBuildPromptSubstringsDefaultOptions(t *testing.T) {
	opts := Options{
		Cwd:          t.TempDir(),
		SystemPrompt: "test core",
	}
	subs := buildPromptSubstrings(opts, nil)
	// Invariants the trace depends on (default Options):
	//   1. baseInstructions substring equals the core system prompt bytes.
	//   2. confirmationPolicy substring is non-empty (the embedded policy
	//      is always present, post TrimSpace).
	//   3. skills, tools, and schema substrings are empty (no skills or
	//      tools configured for default Options).
	if subs.baseInstructions != "test core" {
		t.Fatalf("baseInstructions substring must equal the core system prompt: got %q want %q", subs.baseInstructions, "test core")
	}
	if subs.confirmationPolicy == "" {
		t.Fatalf("confirmationPolicy substring must be non-empty (the embedded policy is always present)")
	}
	if subs.skills != "" {
		t.Fatalf("skills substring must be empty for default Options: got %q", subs.skills)
	}
	// Round-trip: the corresponding fingerprint hashes must equal
	// sha256hex of the substrings, with no truncation or reformatting
	// between the two layers.
	fp := ComputePrefixFingerprint(opts, nil)
	if fp.BaseInstructionsHash != sha256hex(subs.baseInstructions) {
		t.Fatalf("BaseInstructionsHash in fingerprint must equal sha256hex of the substring")
	}
	if fp.ConfirmationPolicyHash != sha256hex(subs.confirmationPolicy) {
		t.Fatalf("ConfirmationPolicyHash in fingerprint must equal sha256hex of the substring")
	}
}

// TestCanonicalSchemaStringStableForMapParameters asserts the headline
// property the trace needs: the same map[string]any produces the same
// canonical string across calls, even though Go's fmt.Sprintf("%v", m)
// iterates a map in random order. json.Marshal sorts map keys
// alphabetically, which is the fix.
func TestCanonicalSchemaStringStableForMapParameters(t *testing.T) {
	params := map[string]any{
		"description": "read a file",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": []any{"path"},
		"type":     "object",
	}
	first := canonicalSchemaString(params)
	second := canonicalSchemaString(params)
	if first != second {
		t.Fatalf("canonicalSchemaString must be stable across calls for the same Parameters:\n  first=%s\n second=%s", first, second)
	}
	if first == "" {
		t.Fatal("canonicalSchemaString must produce a non-empty string for a non-empty map")
	}
	// Two maps with the same key/value pairs (different declaration
	// order in source) must produce identical canonical strings. This
	// is the property that defeats Go's map iteration randomization.
	reordered := map[string]any{
		"type":     "object",
		"required": []any{"path"},
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"description": "read a file",
	}
	if canonicalSchemaString(params) != canonicalSchemaString(reordered) {
		t.Fatal("canonicalSchemaString must produce identical output for maps with the same key/value pairs in different declaration orders")
	}
}

// TestCanonicalSchemaStringFallbackStableForNonJSONValue exercises the
// non-JSON fallback in canonicalSchemaString: when Parameters contains a
// value json.Marshal cannot encode (here, a channel), the function must
// fall back to a key-sorted stringification that is stable across calls
// and across map-iteration order. The previous fallback used
// fmt.Sprintf("%v", params), which iterates the map in random order and
// produced a different hash for the same value across calls — defeating
// the fingerprint. The current fallback is stable by construction.
func TestCanonicalSchemaStringFallbackStableForNonJSONValue(t *testing.T) {
	ch := make(chan int) // channels are not JSON-encodable
	params := map[string]any{
		"description": "a tool with a non-JSON value",
		"channel":     ch,
		"z_first":     1,
		"a_second":    2,
	}
	first := canonicalSchemaString(params)
	second := canonicalSchemaString(params)
	if first == "" {
		t.Fatal("canonicalSchemaString must produce a non-empty string for the fallback path")
	}
	if first != second {
		t.Fatalf("canonicalSchemaString fallback must be stable across calls:\n  first=%s\n second=%s", first, second)
	}
	// A re-ordered map (same key/value pairs, different declaration order)
	// must produce the same fallback string. This is the property the
	// key-sorted stringifier provides that fmt.Sprintf("%v", m) does not.
	reordered := map[string]any{
		"a_second":    2,
		"z_first":     1,
		"channel":     ch,
		"description": "a tool with a non-JSON value",
	}
	if canonicalSchemaString(params) != canonicalSchemaString(reordered) {
		t.Fatalf("canonicalSchemaString fallback must produce identical output for maps with the same key/value pairs in different declaration orders:\n  first=%s\n second=%s", first, canonicalSchemaString(reordered))
	}
	// The fallback must include the "__non_json:" prefix so a consumer
	// can tell the bytes are not a JSON-marshaled schema (a hash collision
	// between a json.Marshal result and a stableStringify result is
	// possible in theory; the prefix makes the source observable).
	if !strings.HasPrefix(first, "__non_json:") {
		t.Fatalf("canonicalSchemaString fallback must start with the __non_json: prefix, got: %s", first)
	}
}
