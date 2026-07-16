package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

// promptSubstrings are the seven cacheable sub-components of the prompt a run
// sends to a model. Each is the literal content the sub-component would emit
// into the system prompt or tool list, hashed so a downstream observer can
// detect drift turn-over-turn. The substrings are produced by
// buildPromptSubstrings and consumed by ComputePrefixFingerprint.
type promptSubstrings struct {
	baseInstructions   string
	confirmationPolicy string
	projectContext     string
	skills             string
	tools              string
	schema             string
}

// buildPromptSubstrings assembles the seven cacheable sub-components of the
// prompt without joining them. The system prompt sections are produced by the
// same private builders that buildSystemPrompt uses, so the substrings are
// byte-identical to what would have appeared in the joined prompt. The tools
// and schema substrings are derived from the partitioned tool list (caller-
// provided) so this helper does not need to re-run the partition.
//
// This is the seam ComputePrefixFingerprint reads from. It exists as a
// separate function (rather than returning both prompt and substrings from
// buildSystemPrompt) so existing callers of buildSystemPrompt are unaffected
// and the substrings helper is independently testable.
func buildPromptSubstrings(options Options, exposed []zeroruntime.ToolDefinition) promptSubstrings {
	core := strings.TrimSpace(options.SystemPrompt)
	if core == "" {
		core = strings.TrimSpace(coreSystemPrompt)
	}
	if core == "" {
		core = fallbackSystemPrompt
	}

	// The confirmation policy is appended unconditionally in buildSystemPrompt;
	// the substring here is the same constant.
	policy := strings.TrimSpace(confirmationPolicy)

	// projectContext is the joined output of workspaceContext, which itself
	// walks the AGENTS.md / ZERO.md / .zero/AGENTS.md chain. Hashing the
	// joined string captures every guideline file's contribution and the
	// ordering between them.
	project := workspaceContext(options.Cwd)

	// skills is the joined output of skillsContext, with each skill on its own
	// line. The substring is the literal text the model would see.
	skills := skillsContext(options)

	// tools and schema are derived from the partitioned tool list. The list
	// passed in is already in stable order (partitionToolsCached is the
	// canonical stable partitioner) so concatenating in slice order is
	// sufficient — we sort names again defensively in case a future caller
	// hands us a list from a different partitioner.
	toolsSubstr, schemaSubstr := toolSubstrings(exposed)

	return promptSubstrings{
		baseInstructions:   core,
		confirmationPolicy: policy,
		projectContext:     project,
		skills:             skills,
		tools:              toolsSubstr,
		schema:             schemaSubstr,
	}
}

// toolSubstrings extracts a stable, name-sorted digest of the partitioned tool
// list. The tools substring is "name1\nname2\n..." (sorted) so a reordering of
// the partition produces a different hash. The schema substring is the
// concatenation of each tool's JSON schema in name order, so a schema change
// is detected independently of the tool ordering.
func toolSubstrings(exposed []zeroruntime.ToolDefinition) (toolsSubstr, schemaSubstr string) {
	if len(exposed) == 0 {
		return "", ""
	}
	names := make([]string, 0, len(exposed))
	byName := make(map[string]zeroruntime.ToolDefinition, len(exposed))
	for _, def := range exposed {
		names = append(names, def.Name)
		byName[def.Name] = def
	}
	sort.Strings(names)
	var toolsSB, schemaSB strings.Builder
	for i, name := range names {
		if i > 0 {
			toolsSB.WriteByte('\n')
		}
		toolsSB.WriteString(name)
		def := byName[name]
		// Parameters is rendered to a canonical JSON string per tool. The
		// schema-render cache in internal/agent (used by partitionToolsCached)
		// guarantees the same render is byte-identical across turns for the
		// same tool, so this substring is a stable hash input.
		schemaSB.WriteString(name)
		schemaSB.WriteByte('\n')
		schemaSB.WriteString(canonicalSchemaString(def.Parameters))
		schemaSB.WriteByte('\n')
	}
	return toolsSB.String(), schemaSB.String()
}

// canonicalSchemaString renders a tool's parameter schema to a stable string.
// tools.ToolDef.Parameters is a map[string]any in the common case; Go's
// fmt.Sprintf("%v", m) iterates a map in random order, which would produce
// a different hash for the same Parameters value across calls and defeat
// the fingerprint. encoding/json marshals maps with keys sorted
// alphabetically, so json.Marshal is the primary stable render. The
// fallback for the rare non-JSON-compatible value (functions, channels,
// NaN/Inf floats, cyclic references) is a stable key-sorted stringifier
// that walks the value with sorted keys for maps and a leading
// "__non_json:" prefix so a future schema change is visible in the trace
// (a SchemaHash collision from the same value) rather than a silent hash
// drift.
//
// Note: SchemaHash is a stability signal, not a wire-identity signal. The
// bytes json.Marshal produces for a Go map[string]any may differ from
// the bytes the provider actually sends on the wire (provider encoders
// use their own JSON conventions; an Anthropic schema may serialize with
// different whitespace, key order, or number formatting). A consumer
// must NOT assume SchemaHash matches the provider's wire schema — only
// that two turns with the same Go-side Parameters produce the same
// SchemaHash. This is sufficient for the trace's purpose (drift
// detection) but not for a content-based equality check.
func canonicalSchemaString(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	data, err := json.Marshal(params)
	if err != nil {
		return "__non_json:" + stableStringify(params)
	}
	return string(data)
}

// stableStringify renders v to a deterministic string. Maps have their keys
// sorted alphabetically; slices are walked in order; primitives use their
// natural Go format. The function is recursive but bounded by the size of
// the input, so a cyclic reference is not reachable here (the json.Marshal
// fallback is hit when json.Marshal itself returns an error, which for the
// common case is a non-JSON-compatible value, not a cycle — cycles are
// rare in tool schemas and acceptable to mis-render in the fallback path
// since the trace's contract is "the hash is stable for the same input,"
// not "the fallback is lossless"). Used only when json.Marshal fails.
func stableStringify(v any) string {
	var sb strings.Builder
	writeStable(&sb, v)
	return sb.String()
}

func writeStable(sb *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		sb.WriteString("null")
	case bool:
		if x {
			sb.WriteString("true")
		} else {
			sb.WriteString("false")
		}
	case string:
		sb.WriteByte('"')
		sb.WriteString(x)
		sb.WriteByte('"')
	case float64:
		// Use %g for compact, deterministic float rendering. NaN and Inf are
		// not JSON-encodable (which is why we are in the fallback path) and
		// render as "NaN" / "+Inf" / "-Inf" — distinct, stable strings.
		fmt.Fprintf(sb, "%g", x)
	case float32:
		fmt.Fprintf(sb, "%g", float64(x))
	case int:
		fmt.Fprintf(sb, "%d", x)
	case int64:
		fmt.Fprintf(sb, "%d", x)
	case int32:
		fmt.Fprintf(sb, "%d", x)
	case uint:
		fmt.Fprintf(sb, "%d", x)
	case uint64:
		fmt.Fprintf(sb, "%d", x)
	case uint32:
		fmt.Fprintf(sb, "%d", x)
	case map[string]any:
		sb.WriteByte('{')
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteByte('"')
			sb.WriteString(k)
			sb.WriteString(`":`)
			writeStable(sb, x[k])
		}
		sb.WriteByte('}')
	case []any:
		sb.WriteByte('[')
		for i, item := range x {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeStable(sb, item)
		}
		sb.WriteByte(']')
	default:
		// Last-resort: include the type so distinct values produce distinct
		// strings even if their default format collides. Without the type
		// tag, fmt.Sprintf("%v", x) for two different types could produce
		// the same bytes (rare, but the type prefix makes it impossible).
		fmt.Fprintf(sb, "<%T:%v>", x, x)
	}
}

// ComputePrefixFingerprint returns a trace.PrefixHash (a 7-field fingerprint
// of the prompt prefix) for one turn of a run. The seven sub-hashes are
// independent SHA-256s of the corresponding sub-component; the complete-prefix
// hash is a SHA-256 of the canonical concatenation of the other six, so any
// sub-component drift is observable both individually and in aggregate.
//
// I/O side effects: buildPromptSubstrings calls workspaceContext, which runs
// git (gitBranchForPrompt, FindProjectGitRoot) and reads the AGENTS.md /
// ZERO.md / .zero/AGENTS.md chain plus the repo map. The same workspace-
// context work is performed separately by buildSystemPrompt a few lines
// later in the request-build path, so on every traced turn the file and
// git reads happen twice. The duplication is intentional for this PR (a
// follow-up will pass substrings through) and is a known perf cost only on
// the opt-in trace path.
//
// A stable CompletePrefixHash across turns means the four captured
// sub-components (baseInstructions, confirmationPolicy, projectContext,
// skills) are byte-identical. It does NOT rule out drift in the seven
// uncaptured sections of buildSystemPrompt (modelPromptAddendum,
// sessionRuntimeContext, approvedCommandPrefixContext, workspaceSeedContext,
// userGuidelines, specialistDelegationContext, responseStyleContext). For
// default Options the four captured substrings are the full prompt; for
// non-default Options the seven uncaptured sections may contribute, and a
// consumer correlating CompletePrefix stability with cached_input_tokens
// must cross-check the model_switches counter (modelPromptAddendum changes
// on a model switch) to disambiguate "no drift" from "drift in an
// uncaptured section."

func ComputePrefixFingerprint(options Options, exposed []zeroruntime.ToolDefinition) prefixFingerprint {
	subs := buildPromptSubstrings(options, exposed)
	base := sha256hex(subs.baseInstructions)
	policy := sha256hex(subs.confirmationPolicy)
	project := sha256hex(subs.projectContext)
	skills := sha256hex(subs.skills)
	toolsH := sha256hex(subs.tools)
	schema := sha256hex(subs.schema)
	complete := sha256hex(strings.Join([]string{
		base, policy, project, skills, toolsH, schema,
	}, "|"))
	return prefixFingerprint{
		BaseInstructionsHash:   base,
		ConfirmationPolicyHash: policy,
		ProjectContextHash:     project,
		SkillsHash:             skills,
		ToolsHash:              toolsH,
		SchemaHash:             schema,
		CompletePrefixHash:     complete,
	}
}

// prefixFingerprint is the agent-side shape of a prompt-prefix fingerprint. It
// is converted to a trace.PrefixHash at the loop boundary (see EmitPrefixHash
// in loop.go) so the trace package does not need to import this one. The field
// names match the trace.PrefixHash JSON tags 1:1.
type prefixFingerprint struct {
	BaseInstructionsHash   string
	ConfirmationPolicyHash string
	ProjectContextHash     string
	SkillsHash             string
	ToolsHash              string
	SchemaHash             string
	CompletePrefixHash     string
}

// sha256hex returns the hex-encoded SHA-256 of s. Empty input produces the
// hash of the empty string, which is a constant; callers that want to
// distinguish "absent" from "empty" should check s before calling.
func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
