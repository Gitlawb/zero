package tui

// workingWords is a small ring buffer of liveness verbs that the assistant
// interim block cycles through while the model is generating. The list is
// tuned for the Gitlawb / OpenFable brand: a heavy dose of project-name and
// feature-name verbs (weighted 1.5x) so "gitlawbmaxxing" is the first thing a
// long-running user sees, plus a sprinkle of meme-y maxxing/pilled words and
// Claude-Code-style gerunds for variety.
//
// All verbs are lowercase to brand-differentiate from Claude Code's Title
// Case defaults — the spinner animates, the verb sits next to it, and the
// casing is a quiet visual marker of which agent you're using.
//
// The ring is built once at construction; Tick() walks it in order. We
// intentionally do not use a random pick per turn (Claude Code's behaviour)
// because the cadence is the same one that drives the spinner glyph, and a
// deterministic order is easier to test and reason about.
type workingWords struct {
	weighted []string // ring; brand entries appear 1.5x in this slice
	index    int
}

// brandVerbs are the project-name and core-feature verbs that anchor the
// rotation. Each appears 1.5 times in the ring (one full copy + one extra
// half-weight position determined by duplication in newWorkingWords), so a
// user staring at the spinner for 30s sees a brand word roughly every third
// tick instead of every ~36th.
var brandVerbs = []string{
	"gitlawbmaxxing",
	"openfablemaxxing",
	"openfabling",
	"gitlawbing",
	"gitifying",
	"tokenizing",
	"context-stuffing",
	"prompt-wrangling",
}

// featureVerbs turn OpenFable's actual features into present-participles. Anyone
// who has used the tool recognises what they do; the verb is also a quiet
// product tour for first-time users.
var featureVerbs = []string{
	"worktree-walking",
	"branch-bending",
	"diff-reading",
	"sandboxing",
	"swarming",
	"daemon-summoning",
	"tui-painting",
	"queue-juggling",
}

// vibeVerbs are the meme-y / gen-Z crowd-pleasers borrowed (loosely) from
// the Claude Code community spinner packs. Kept tight: -maxxing, -pilled,
// and one cooking word that doubles as a Claude Code original.
var vibeVerbs = []string{
	"maxxing",
	"pilled",
	"aura-farming",
	"vibe-checking",
	"cooking",
}

// classicsVerbs are the Claude-Code-original gerunds. They are quiet, old-
// fashioned, and pair well with the brand and feature verbs so the rotation
// never feels like it's all the same energy.
var classicsVerbs = []string{
	"cogitating",
	"contemplating",
	"synergizing",
	"brewing",
	"percolating",
	"fermenting",
	"wrangling",
	"schlepping",
	"booping",
	"tomfoolering",
	"merge-merging",
}

// baseVerbs is the deduplicated, unweighted concatenation used by tests and
// by the type's invariant checks. The order here is the display order: brand
// first, then feature, vibe, classics.
func baseVerbs() []string {
	out := make([]string, 0, len(brandVerbs)+len(featureVerbs)+len(vibeVerbs)+len(classicsVerbs))
	out = append(out, brandVerbs...)
	out = append(out, featureVerbs...)
	out = append(out, vibeVerbs...)
	out = append(out, classicsVerbs...)
	return out
}

// weightedRing builds the cyclic slice used at render time. Brand verbs are
// duplicated (each appears once more, interleaved) to land at ~1.5x
// frequency; other verbs appear once. The total length is
// len(brand) + len(brand) + len(feature) + len(vibe) + len(classics) = 8+8+8+5+11 = 40.
func weightedRing() []string {
	base := baseVerbs()
	ring := make([]string, 0, len(base)+len(brandVerbs))
	// Brand entries first (each appears once in base); we add a second copy
	// interleaved so they read naturally through the rotation.
	ring = append(ring, base...)
	for i, v := range base {
		if i < len(brandVerbs) {
			ring = append(ring, v)
		}
	}
	return ring
}

// newWorkingWords constructs a fresh ring buffer positioned at the first
// brand verb (the deterministic first frame). It is cheap to call from
// newModel; the underlying slice is small.
func newWorkingWords() *workingWords {
	return &workingWords{
		weighted: weightedRing(),
		index:    0,
	}
}

// Current returns the verb to render this frame. The first call returns
// "gitlawbmaxxing" by construction.
func (w *workingWords) Current() string {
	if w == nil || len(w.weighted) == 0 {
		return "working"
	}
	return w.weighted[w.index]
}

// Tick advances the index by one position, wrapping at the end. Safe to call
// on a nil receiver (no-op) so the call site doesn't need a nil check.
func (w *workingWords) Tick() {
	if w == nil || len(w.weighted) == 0 {
		return
	}
	w.index = (w.index + 1) % len(w.weighted)
}

// Reset rewinds the rotation to the first frame. Used when a new run starts
// so the user sees the brand word again instead of mid-rotation.
func (w *workingWords) Reset() {
	if w == nil {
		return
	}
	w.index = 0
}
