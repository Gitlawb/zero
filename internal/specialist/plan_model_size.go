package specialist

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// modelSizeBillions estimates a model's parameter count in BILLIONS from its id,
// 0 when the id names no size.
//
// THIS IS THE CROSS-PROVIDER SIZE SIGNAL. A local/free provider prices every
// model at zero, so ranking it by price ranks it by nothing — but its ids name
// their size ("qwen3-coder:480b", "gpt-oss:20b", "phi3:3.8b", "smollm:135m"),
// and that is the capability signal price cannot give. Cloud ids ("gpt-4o",
// "claude-opus-4") carry no size and return 0; those providers are priced, so
// size is not the signal they rank by anyway.
//
// The LARGEST size-shaped token wins: an id can carry a version that looks
// numeric ("qwen3.5:397b" — 3.5 is a version, 397b is the size), but a version
// is not suffixed b/m, so only the real size matches; taking the largest is a
// second guard against an incidental small number.
// TRILLIONS TOO. The suffix set was [bm], so "kimi-k2:1t" parsed as size 0 —
// unknown — and on a free provider sorted BELOW gpt-oss:20b: the largest model
// on the account became the cheap tier every scan task was routed to, and the
// first thing the shortlist discarded.
var modelSizeToken = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)([bmt])(?:[^a-z]|$)`)

func modelSizeBillions(id string) float64 {
	var largest float64
	for _, match := range modelSizeToken.FindAllStringSubmatch(id, -1) {
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil || value <= 0 {
			continue
		}
		switch {
		case strings.EqualFold(match[2], "m"):
			value /= 1000 // millions -> billions
		case strings.EqualFold(match[2], "t"):
			value *= 1000 // trillions -> billions
		}
		if value > largest {
			largest = value
		}
	}
	return largest
}

// ModelSizeLabel renders a model's size for a human (or a router) to read —
// "20B", "135M" — or "" when the id names no size. A router shown "20b" vs "120b"
// can match a light model to a scan and a heavy one to a judgement directly,
// instead of inferring capability from list position alone.
func ModelSizeLabel(id string) string {
	size := modelSizeBillions(id)
	if size <= 0 {
		return ""
	}
	if size < 1 {
		return strconv.FormatFloat(size*1000, 'g', -1, 64) + "M"
	}
	return strconv.FormatFloat(size, 'g', -1, 64) + "B"
}

// applyMinSizeFloor drops models KNOWN to be smaller than the floor — a task
// then lands on a decent model rather than a toy the provider happened to list
// cheapest.
//
// A model whose id names NO size is kept: an unknown size is not evidence of a
// small one, and most cloud ids carry none — filtering them would silently empty
// a cloud provider's list. FAIL-OPEN at the end: if the floor leaves nothing, it
// is ignored, because a plan on a small model beats a plan with no model to run.
func applyMinSizeFloor(models []DiscoveredModel, floor float64) []DiscoveredModel {
	if floor <= 0 {
		return models
	}
	kept := make([]DiscoveredModel, 0, len(models))
	for _, model := range models {
		if size := modelSizeBillions(model.ID); size > 0 && size < floor {
			continue
		}
		kept = append(kept, model)
	}
	if len(kept) == 0 {
		return models
	}
	return kept
}

// sortModelsByCapability orders models from LEAST to MOST capable, so the tier
// builder can read cheap/balanced/strong off the ends.
//
// PRICED PROVIDERS ARE UNCHANGED: cost is their capability proxy, and the order
// is exactly what it was — cost, then cost, then id. A FREE provider (every cost
// zero) instead ranks by the size parsed from the id, because the cost sort there
// fell through to alphabetical and tiered by nothing. The size branch is skipped
// the moment any model reports a price, so a mixed or priced catalogue never
// changes.
// catalogueIsPriced reports whether ANY model in the set carries a price.
//
// One authority for the question, because two things now branch on it: the
// ranking (cost or size) and the shortlist (which must not narrow a priced
// catalogue at all). Two copies of this loop would be two chances to disagree
// about what "free provider" means.
func catalogueIsPriced(models []DiscoveredModel) bool {
	for _, model := range models {
		if model.InputCost > 0 || model.OutputCost > 0 {
			return true
		}
	}
	return false
}

func sortModelsByCapability(eligible []DiscoveredModel) {
	priced := catalogueIsPriced(eligible)
	sort.SliceStable(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		if !priced {
			if as, bs := modelSizeBillions(a.ID), modelSizeBillions(b.ID); as != bs {
				return as < bs
			}
		}
		if a.InputCost != b.InputCost {
			return a.InputCost < b.InputCost
		}
		if a.OutputCost != b.OutputCost {
			return a.OutputCost < b.OutputCost
		}
		return a.ID < b.ID
	})
}

// defaultTopRankedModels is how many of the most capable models a plan may
// route to, when nothing is configured.
//
// A provider can list far more than a plan can sensibly use. ollama-cloud
// serves around twenty, and the ranking below spans the whole field — so the
// cheap tier reads the SMALLEST thing the provider offers and the router is
// handed twenty candidates to choose between, most of which no task should get.
// Keeping the top ten narrows both to the models actually worth a sub-agent:
// the tiers then span "smallest of the good ten" to "best", rather than
// "smallest of everything" to "best".
//
// Ten rather than three: the tiers need spread (cheap/balanced/strong are read
// off the ends and the middle), and the router's whole value is having real
// choices — a shortlist of three is a tier table with extra steps.
const defaultTopRankedModels = 10

// applyTopRank keeps only the most capable N, from a list already sorted
// least-to-most capable.
//
// FAIL-OPEN, like every other narrowing here: a provider offering fewer than N
// keeps all of them, and a non-positive N means "the default" rather than
// "none" — a zero value must never be the thing that leaves a plan with no
// models to assign.
//
// The tail, not the head: the list is ascending, so the most capable models are
// at the end. Taking the head would keep exactly the toys this exists to drop.
func applyTopRank(models []DiscoveredModel, top int) []DiscoveredModel {
	// A PRICED CATALOGUE IS NEVER NARROWED, and this is the correction to the
	// version that shipped without it.
	//
	// The list is sorted least-to-most capable, but "capable" is measured by
	// SIZE on a free provider and by COST on a priced one. Taking the tail is
	// therefore "keep the biggest" in the first case and "keep the DEAREST" in
	// the second — which moved the cheap tier from the cheapest model in the
	// catalogue to the tenth-dearest, and every scan task in every auto-assigned
	// plan with it. Measured on a 12-model priced fixture: cheap went from cost
	// 1 to cost 3, and on a real 30-model account the gap is an order of
	// magnitude of real money.
	//
	// The asymmetry is the point. On a free provider the small models cost
	// nothing and buy nothing, so dropping them is pure gain. On a priced one
	// the cheap end is exactly what the cheap tier exists to spend, so dropping
	// it defeats the tiering rather than sharpening it.
	if top <= 0 {
		top = defaultTopRankedModels
	}
	if len(models) <= top || catalogueIsPriced(models) {
		return models
	}
	// A MODEL WHOSE SIZE CANNOT BE READ IS NEVER DROPPED FOR BEING SMALL — the
	// same fail-open rule applyMinSizeFloor already follows, and the first
	// version of this function broke it.
	//
	// The ranking sorts an unparseable id as size 0, which puts it at the
	// LEAST-capable end, so taking a plain tail deleted exactly the models whose
	// names happen not to state a parameter count. On a real ollama-cloud
	// account that meant kimi-k2.6, glm-5.2 and deepseek-v4-flash were removed
	// from routing while gpt-oss:20b survived — the operator's own guidance
	// called the first two the strongest reasoners on the machine. Dropping a
	// model because its name is uninformative is not a capability judgement, it
	// is a parsing accident.
	//
	// So the cut applies to SIZED models only: every unknown-size model is kept,
	// and the top N of the ones we can actually compare join them. Order is
	// preserved, so the caller still reads cheap/balanced/strong off the ends.
	sized := 0
	for _, model := range models {
		if modelSizeBillions(model.ID) > 0 {
			sized++
		}
	}
	if sized <= top {
		return models
	}
	drop := sized - top
	kept := make([]DiscoveredModel, 0, len(models)-drop)
	for _, model := range models {
		if drop > 0 && modelSizeBillions(model.ID) > 0 {
			drop--
			continue
		}
		kept = append(kept, model)
	}
	return kept
}
