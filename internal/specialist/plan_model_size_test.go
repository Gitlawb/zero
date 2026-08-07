package specialist

import (
	"strconv"
	"testing"
)

// The size parser handles real ids from both kinds of provider: local ids name
// their size, cloud ids do not (and are priced anyway).
func TestModelSizeBillionsParsesRealIds(t *testing.T) {
	cases := map[string]float64{
		"gpt-oss:120b":          120,
		"gpt-oss:20b":           20,
		"qwen3.5:397b":          397, // 3.5 is a version; 397b is the size
		"mistral-large-3:675b":  675, // -3 is a version; 675b is the size
		"deepseek-coder-v2:16b": 16,  // v2 is a version; 16b is the size
		"phi3:3.8b":             3.8, // fractional billions
		"codellama:13b":         13,
		"smollm2:135m":          0.135, // millions -> billions
		"mixtral:8x7b":          7,     // MoE: the b-suffixed token
		"gpt-4o":                0,     // cloud, no size in the id
		"claude-opus-4":         0,
	}
	for id, want := range cases {
		if got := modelSizeBillions(id); got != want {
			t.Errorf("modelSizeBillions(%q) = %v, want %v", id, got, want)
		}
	}
}

// The human/router-facing size label: billions above 1, millions below, empty
// when the id names no size.
func TestModelSizeLabel(t *testing.T) {
	cases := map[string]string{
		"gpt-oss:20b":  "20B",
		"phi3:3.8b":    "3.8B",
		"smollm2:135m": "135M",
		"gpt-4o":       "",
	}
	for id, want := range cases {
		if got := ModelSizeLabel(id); got != want {
			t.Errorf("ModelSizeLabel(%q) = %q, want %q", id, got, want)
		}
	}
}

// The decency floor drops models KNOWN to be small (a task should not land on a
// toy), keeps decent ones, and keeps models of UNKNOWN size — an unknown size is
// not evidence of a small one, and cloud ids carry none.
func TestMinSizeFloorDropsToysButKeepsDecentAndUnknown(t *testing.T) {
	models := []DiscoveredModel{
		{ID: "toy:1b"}, {ID: "tiny:135m"}, {ID: "decent:7b"}, {ID: "big:70b"}, {ID: "gpt-4o"},
	}
	kept := map[string]bool{}
	for _, m := range applyMinSizeFloor(models, 7) {
		kept[m.ID] = true
	}
	if kept["toy:1b"] || kept["tiny:135m"] {
		t.Fatalf("a sub-floor toy survived the decency floor: %v", kept)
	}
	if !kept["decent:7b"] || !kept["big:70b"] {
		t.Fatalf("a decent model was dropped: %v", kept)
	}
	if !kept["gpt-4o"] {
		t.Fatal("a model of unknown size was dropped — unknown is not evidence of small")
	}
}

// If the floor would leave NO models, it yields: a plan on a small model beats a
// plan with nothing to run.
func TestMinSizeFloorFailsOpenWhenItWouldEmptyThePool(t *testing.T) {
	models := []DiscoveredModel{{ID: "small-a:1b"}, {ID: "small-b:2b"}}
	if kept := applyMinSizeFloor(models, 70); len(kept) != 2 {
		t.Fatalf("the floor emptied the pool instead of failing open: kept %d", len(kept))
	}
}

// Floor 0 (unset) changes nothing — the default for every user who has not asked
// for one.
func TestMinSizeFloorOffKeepsEverything(t *testing.T) {
	models := []DiscoveredModel{{ID: "toy:1b"}, {ID: "big:70b"}}
	if kept := applyMinSizeFloor(models, 0); len(kept) != 2 {
		t.Fatalf("floor 0 (off) dropped models: kept %d", len(kept))
	}
}

// A FREE provider tiers by SIZE, not alphabet. The ids are arranged so the
// alphabetical order (zzz > mmm > aaa) disagrees with the size order
// (1b < 7b < 70b) — a passing test proves size won.
func TestFreeProviderTiersBySizeNotAlphabet(t *testing.T) {
	models := []DiscoveredModel{
		{ID: "zzz:1b"},
		{ID: "aaa:70b"},
		{ID: "mmm:7b"},
	}
	sortModelsByCapability(models)
	got := []string{models[0].ID, models[1].ID, models[2].ID}
	want := []string{"zzz:1b", "mmm:7b", "aaa:70b"} // light -> heavy
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("free-provider order = %v, want %v (by size, not alphabet)", got, want)
		}
	}
}

// A PRICED provider is unchanged: cost is its capability proxy, and the size
// branch must not leak in. The costs and sizes are arranged to DISAGREE, so if
// size influenced a priced sort this fails.
func TestPricedProviderTiersByCostUnchanged(t *testing.T) {
	models := []DiscoveredModel{
		{ID: "big-but-cheap:70b", InputCost: 1},
		{ID: "small-but-pricey:1b", InputCost: 5},
		{ID: "mid:7b", InputCost: 3},
	}
	sortModelsByCapability(models)
	got := []string{models[0].ID, models[1].ID, models[2].ID}
	want := []string{"big-but-cheap:70b", "mid:7b", "small-but-pricey:1b"} // cost asc
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("priced order = %v, want cost order %v (size must not leak in)", got, want)
		}
	}
}

// THE SHORTLIST. A provider can list far more than a plan can sensibly use, and
// ranking the whole field is what put the SMALLEST model the provider serves on
// the cheap tier — a toy doing a scan while nineteen better ones sat unused —
// and handed the router twenty candidates to choose between.
func TestOnlyTheTopRankedModelsAreOffered(t *testing.T) {
	// Twelve free models, sized in the id: the ranking is by size, ascending.
	sizes := []string{"1b", "3b", "7b", "8b", "14b", "20b", "27b", "32b", "70b", "120b", "235b", "397b"}
	models := make([]DiscoveredModel, 0, len(sizes))
	for _, size := range sizes {
		models = append(models, DiscoveredModel{ID: "m:" + size, ToolCall: true})
	}
	ranked := rankedEligibleModels(models, ModelPreferences{})
	if len(ranked) != defaultTopRankedModels {
		t.Fatalf("offered %d models, want the top %d", len(ranked), defaultTopRankedModels)
	}
	// The TAIL is kept — the most capable end. Keeping the head would preserve
	// exactly the toys this exists to drop.
	if ranked[0].ID != "m:7b" || ranked[len(ranked)-1].ID != "m:397b" {
		t.Fatalf("kept %q..%q, want the ten largest (7b..397b)", ranked[0].ID, ranked[len(ranked)-1].ID)
	}
	for _, dropped := range []string{"m:1b", "m:3b"} {
		for _, model := range ranked {
			if model.ID == dropped {
				t.Fatalf("%s survived the shortlist", dropped)
			}
		}
	}
	// The cheap tier now reads the smallest of the GOOD ten, not of everything.
	if tiers := buildModelTiers(models, ModelPreferences{}); tiers.cheap != "m:7b" {
		t.Fatalf("cheap tier = %q, want the smallest of the shortlist", tiers.cheap)
	}
}

// FAIL-OPEN, like every other narrowing here: a provider offering fewer than the
// cap keeps all of them, and a configured cap is honoured.
func TestTheShortlistNeverEmptiesTheField(t *testing.T) {
	three := []DiscoveredModel{{ID: "m:7b"}, {ID: "m:70b"}, {ID: "m:120b"}}
	if got := rankedEligibleModels(three, ModelPreferences{}); len(got) != 3 {
		t.Fatalf("a provider with three models kept %d", len(got))
	}
	if got := applyTopRank(three, 0); len(got) != 3 {
		t.Fatal("a zero cap must mean the default, never none")
	}
	if got := applyTopRank(three, -5); len(got) != 3 {
		t.Fatal("a negative cap must mean the default, never none")
	}
	if got := applyTopRank(three, 2); len(got) != 2 || got[0].ID != "m:70b" {
		t.Fatalf("a configured cap of 2 kept %v, want the two largest", got)
	}
	if got := rankedEligibleModels(nil, ModelPreferences{}); len(got) != 0 {
		t.Fatalf("no models in, %d out", len(got))
	}
}

// A PIN OUTSIDE THE SHORTLIST STILL APPLIES. The user naming a model is an
// instruction, not a suggestion, and pins are validated against what the
// provider SERVES rather than against this heuristic list.
func TestAPinOutsideTheShortlistStillRoutes(t *testing.T) {
	sizes := []string{"1b", "3b", "7b", "8b", "14b", "20b", "27b", "32b", "70b", "120b", "235b", "397b"}
	models := make([]DiscoveredModel, 0, len(sizes))
	served := map[string]bool{}
	for _, size := range sizes {
		models = append(models, DiscoveredModel{ID: "m:" + size, ToolCall: true})
		served["m:"+size] = true
	}
	prefs := ModelPreferences{Scan: "m:1b"} // deliberately the smallest, off the list
	tiers := buildModelTiers(models, prefs)
	if got := tiers.modelForRoleWith(TaskRoleScan, prefs, served); got != "m:1b" {
		t.Fatalf("scan resolved to %q; a pin the provider serves must win over the shortlist", got)
	}
}

// A PRICED CATALOGUE IS NEVER NARROWED, and this is the case the first version
// got wrong. The list is sorted least-to-most capable, but "capable" means SIZE
// on a free provider and COST on a priced one — so taking the tail kept the
// DEAREST ten and moved the cheap tier from the cheapest model in the catalogue
// to the tenth-dearest. Measured: 12 models priced 1..12 put cheap at cost 3.
func TestAPricedCatalogueKeepsItsCheapEnd(t *testing.T) {
	var priced []DiscoveredModel
	for i := 1; i <= 12; i++ {
		priced = append(priced, DiscoveredModel{ID: "m" + strconv.Itoa(i), ToolCall: true, InputCost: float64(i)})
	}
	ranked := rankedEligibleModels(priced, ModelPreferences{})
	if len(ranked) != 12 {
		t.Fatalf("a priced catalogue was narrowed to %d; the cheap end is what the cheap tier spends", len(ranked))
	}
	if tiers := buildModelTiers(priced, ModelPreferences{}); tiers.cheap != "m1" {
		t.Fatalf("cheap tier = %q, want the cheapest model in the catalogue", tiers.cheap)
	}
	// The free case is unchanged: still narrowed, still keeping the largest.
	var free []DiscoveredModel
	for _, size := range []string{"1b", "3b", "7b", "8b", "14b", "20b", "27b", "32b", "70b", "120b", "235b", "397b"} {
		free = append(free, DiscoveredModel{ID: "m:" + size, ToolCall: true})
	}
	if got := rankedEligibleModels(free, ModelPreferences{}); len(got) != defaultTopRankedModels {
		t.Fatalf("a free catalogue kept %d, want the top %d", len(got), defaultTopRankedModels)
	}
}

// TRILLIONS PARSE. "kimi-k2:1t" read as size 0 — unknown — and on a free
// provider sorted BELOW gpt-oss:20b, so the largest model on the account became
// the cheap tier every scan task was routed to, and the first thing the
// shortlist discarded.
func TestATrillionParameterModelIsNotSizeZero(t *testing.T) {
	if got := modelSizeBillions("kimi-k2:1t"); got != 1000 {
		t.Fatalf("modelSizeBillions(kimi-k2:1t) = %v, want 1000", got)
	}
	free := []DiscoveredModel{
		{ID: "gpt-oss:20b", ToolCall: true},
		{ID: "kimi-k2:1t", ToolCall: true},
		{ID: "qwen3-coder:480b", ToolCall: true},
	}
	tiers := buildModelTiers(free, ModelPreferences{})
	if tiers.cheap == "kimi-k2:1t" {
		t.Fatal("the trillion-parameter model became the cheap tier")
	}
	if tiers.strong != "kimi-k2:1t" {
		t.Fatalf("strong tier = %q, want the largest model", tiers.strong)
	}
}
