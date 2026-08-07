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
var modelSizeToken = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)([bm])(?:[^a-z]|$)`)

func modelSizeBillions(id string) float64 {
	var largest float64
	for _, match := range modelSizeToken.FindAllStringSubmatch(id, -1) {
		value, err := strconv.ParseFloat(match[1], 64)
		if err != nil || value <= 0 {
			continue
		}
		if strings.EqualFold(match[2], "m") {
			value /= 1000 // millions -> billions
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
func sortModelsByCapability(eligible []DiscoveredModel) {
	priced := false
	for _, model := range eligible {
		if model.InputCost > 0 || model.OutputCost > 0 {
			priced = true
			break
		}
	}
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
	if top <= 0 {
		top = defaultTopRankedModels
	}
	if len(models) <= top {
		return models
	}
	return models[len(models)-top:]
}
