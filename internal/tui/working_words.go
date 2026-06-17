package tui

// workingWords is a small ring buffer of liveness verbs that the assistant
// interim block cycles through while the model is generating. The list is
// tuned for the Gitlawb / OpenFable brand: a heavy dose of project-name and
// feature-name verbs (weighted 1.5x) so "gitlawbmaxxing" is the first thing a
// long-running user sees, plus a sprinkle of meme-y maxxing/pilled words and
// a few classic gerunds for variety.
//
// All verbs are lowercase to brand-differentiate from the all-caps defaults
// the upstream Claude-Code-compatible spinners use. The spinner animates,
// the verb sits next to it, and the casing is a quiet visual marker of
// which agent you're using.
//
// The ring is built once at construction; Tick() advances it by one slot.
// We intentionally do not use a random pick per turn because the cadence
// is the same one that drives the spinner glyph, and a deterministic order
// is easier to test and reason about.
type workingWords struct {
	weighted []string // ring; brand entries appear 1.5x in this slice
	index    int
}

// WorkingWordsStepEvery is the number of spinner ticks between verb
// advances. With a 80ms spinner tick and 12-step cadence that's ~960ms per
// word — fast enough to feel alive, slow enough to read. The model owns
// this counter so workingWords stays a dumb ring (Tick() = "advance one
// slot"); a test can still tick the ring rapidly without waiting for a
// spinner.
const WorkingWordsStepEvery = 12

// brandVerbs are the project-name and core-feature verbs that anchor the
// rotation. Each appears 1.5 times in the ring (one full copy in base +
// one duplicate in the weighted block) so a user staring at the spinner
// for 30s sees a brand word roughly every third frame instead of every
// ~36th.
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

// featureVerbs turn the project's actual features into present-participles.
// Anyone who has used the tool recognises what they do; the verb is also a
// quiet product tour for first-time users.
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

// vibeVerbs are the meme-y / gen-Z crowd-pleasers. Kept tight: -maxxing,
// -pilled, and one cooking word. The trade-off here is that these ship to
// every user with no config escape hatch, so the taste call is the author's
// and explicitly made up front.
var vibeVerbs = []string{
	"maxxing",
	"pilled",
	"aura-farming",
	"vibe-checking",
	"cooking",
}

// classicsVerbs are the old-fashioned gerunds. They are quiet and pair
// well with the brand and feature verbs so the rotation never feels like
// it's all the same energy.
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
// duplicated (each appears a second time, appended at the end) to land at
// ~1.5x frequency; other verbs appear once. The total length is
// len(brand) + len(brand) + len(feature) + len(vibe) + len(classics) = 8+8+8+5+11 = 40.
//
// Note: the duplicate brand block is appended sequentially (positions 32-39
// in the ring), not interleaved. The user-visible effect is that one full
// cycle shows all 32 unique verbs followed by 8 brand words, then wraps —
// the brand words are the "bookend" of each cycle rather than sprinkled
// throughout. If we ever want true interleaving, rewrite this to
// distribute duplicates by index parity (e.g. append brand[i] at position
// 2*i in the second half).
func weightedRing() []string {
	base := baseVerbs()
	ring := make([]string, 0, len(base)+len(brandVerbs))
	ring = append(ring, base...)
	ring = append(ring, brandVerbs...)
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
//
// Tick() is the dumb "advance one slot" call. The model wraps it with its
// own step counter (see WorkingWordsStepEvery) so the word rotates at ~1Hz
// instead of every glyph frame; tests can call Tick() directly to walk the
// ring rapidly without waiting for a real spinner.
func (w *workingWords) Tick() {
	if w == nil || len(w.weighted) == 0 {
		return
	}
	w.index = (w.index + 1) % len(w.weighted)
}

// Reset rewinds the rotation to the first frame. Used when a new run starts
// so the user sees the brand word again instead of mid-rotation.
func (w *workingWords) Reset() {
	if w == nil || len(w.weighted) == 0 {
		return
	}
	w.index = 0
}
