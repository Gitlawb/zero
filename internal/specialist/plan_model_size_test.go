package specialist

import "testing"

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
