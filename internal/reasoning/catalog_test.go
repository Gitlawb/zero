package reasoning

import "testing"

func TestEmbeddedCatalogLoads(t *testing.T) {
	c := Embedded()
	if len(c.byProvider) == 0 {
		t.Fatal("embedded catalog is empty")
	}
	if _, ok := c.Lookup("openai", "gpt-5"); !ok {
		t.Fatal("embedded catalog missing a well-known model (gpt-5)")
	}
}

func TestParseCatalogRejectsEmpty(t *testing.T) {
	if _, err := ParseCatalog([]byte(`{}`)); err == nil {
		t.Fatal("expected an error for a catalog with no providers")
	}
	if _, err := ParseCatalog([]byte(`not json`)); err == nil {
		t.Fatal("expected an error for malformed json")
	}
}

func TestGroundTruthOpenAI(t *testing.T) {
	c := Embedded()
	cases := []struct {
		api    string
		reason bool
		kind   ControlKind
		values []string
	}{
		{"gpt-5", true, ControlEffort, []string{"minimal", "low", "medium", "high"}},
		{"gpt-5-codex", true, ControlEffort, []string{"low", "medium", "high"}},
		{"gpt-5-pro", true, ControlEffort, []string{"high"}},
		{"gpt-5.1", true, ControlEffort, []string{"none", "low", "medium", "high"}},
		{"gpt-5.1-codex-max", true, ControlEffort, []string{"low", "medium", "high", "xhigh"}},
		{"o3", true, ControlEffort, []string{"low", "medium", "high"}},
	}
	for _, tc := range cases {
		cap, ok := c.Lookup("openai", tc.api)
		if !ok {
			t.Errorf("%s: not found", tc.api)
			continue
		}
		if cap.Supported() != tc.reason {
			t.Errorf("%s: Supported=%v want %v", tc.api, cap.Supported(), tc.reason)
		}
		if !equalStrings(cap.EffortValues(), tc.values) {
			t.Errorf("%s: efforts=%v want %v", tc.api, cap.EffortValues(), tc.values)
		}
	}

	// Non-reasoning models report reasoning:false and expose no effort.
	for _, api := range []string{"gpt-4o", "gpt-4o-mini", "gpt-4.1", "gpt-4-turbo"} {
		cap, ok := c.Lookup("openai", api)
		if !ok {
			t.Errorf("%s: not found", api)
			continue
		}
		if cap.Supported() {
			t.Errorf("%s: Supported=true, want false (non-reasoning)", api)
		}
		if cap.EffortValues() != nil {
			t.Errorf("%s: efforts=%v, want nil", api, cap.EffortValues())
		}
	}

	// Version splits: minimal vs none are not interchangeable.
	gpt5, _ := c.Lookup("openai", "gpt-5")
	if !gpt5.SupportsEffort("minimal") || gpt5.SupportsEffort("none") {
		t.Error("gpt-5 should support minimal, not none")
	}
	gpt51, _ := c.Lookup("openai", "gpt-5.1")
	if gpt51.SupportsEffort("minimal") || !gpt51.SupportsEffort("none") {
		t.Error("gpt-5.1 should support none, not minimal")
	}
}

func TestGroundTruthAnthropic(t *testing.T) {
	c := Embedded()

	// Legacy / budget-only models: a thinking-token budget, no effort enum. The
	// budget carries an explicit min (1024) and no max — Max stays nil.
	for _, api := range []string{
		"claude-opus-4-1-20250805", "claude-sonnet-4-5-20250929", "claude-haiku-4-5-20251001",
	} {
		cap, ok := c.Lookup("anthropic", api)
		if !ok {
			t.Errorf("%s: not found", api)
			continue
		}
		ctrl, ok := cap.BudgetControl()
		if !ok {
			t.Errorf("%s: expected a budget control", api)
			continue
		}
		if ctrl.Min == nil || *ctrl.Min != 1024 {
			t.Errorf("%s: budget Min=%v, want non-nil 1024", api, ctrl.Min)
		}
		if ctrl.Max != nil {
			t.Errorf("%s: budget Max=%d, want nil (unbounded)", api, *ctrl.Max)
		}
		if _, ok := cap.EffortControl(); ok {
			t.Errorf("%s: did not expect an effort control", api)
		}
	}

	// Newer models: native effort enum with xhigh and the budget control removed.
	opus48, ok := c.Lookup("anthropic", "claude-opus-4-8")
	if !ok {
		t.Fatal("claude-opus-4-8 not found")
	}
	if !equalStrings(opus48.EffortValues(), []string{"low", "medium", "high", "xhigh", "max"}) {
		t.Errorf("opus-4-8 efforts=%v", opus48.EffortValues())
	}
	if _, ok := opus48.BudgetControl(); ok {
		t.Error("opus-4-8 should not carry a budget control (removed)")
	}
}

func TestGroundTruthGemini(t *testing.T) {
	c := Embedded()

	pro, ok := c.Lookup("gemini", "gemini-2.5-pro") // gemini kind -> google slug
	if !ok {
		t.Fatal("gemini-2.5-pro not found via the gemini provider kind")
	}
	ctrl, ok := pro.BudgetControl()
	if !ok || ctrl.Min == nil || *ctrl.Min != 128 || ctrl.Max == nil || *ctrl.Max != 32768 {
		t.Errorf("gemini-2.5-pro budget=%+v ok=%v, want min=128 max=32768", ctrl, ok)
	}
	if _, ok := pro.EffortControl(); ok {
		t.Error("gemini-2.5-pro should be budget-only, no effort control")
	}

	// gemini-2.5-flash can disable thinking: a toggle plus a budget whose explicit
	// min: 0 must survive (a plain int + omitempty would drop it).
	flash, _ := c.Lookup("google", "gemini-2.5-flash")
	if !flash.HasControl(ControlToggle) {
		t.Error("gemini-2.5-flash should expose a toggle (thinking can be disabled)")
	}
	if fctrl, ok := flash.BudgetControl(); !ok || fctrl.Min == nil || *fctrl.Min != 0 {
		t.Errorf("gemini-2.5-flash budget Min=%v, want a non-nil pointer to 0", fctrl.Min)
	}

	// Gemini 3.x switched to an effort enum.
	if g3, ok := c.Lookup("google", "gemini-3-pro-preview"); ok {
		if _, ok := g3.EffortControl(); !ok {
			t.Error("gemini-3-pro-preview should expose an effort control")
		}
	}
}

// TestCoversZeroShippedReasoningModels pins that every reasoning model Zero
// currently ships resolves in the catalog (by its api model id), so the
// models.dev fallback actually covers Zero's catalog rather than just well-known
// ids. The api ids mirror internal/modelregistry's curated entries.
func TestCoversZeroShippedReasoningModels(t *testing.T) {
	c := Embedded()
	shipped := []struct {
		provider, api string
		wantKind      ControlKind
	}{
		{"anthropic", "claude-opus-4-1-20250805", ControlBudget},
		{"anthropic", "claude-sonnet-4-5-20250929", ControlBudget},
		{"anthropic", "claude-haiku-4-5-20251001", ControlBudget},
		{"google", "gemini-2.5-pro", ControlBudget},
		{"google", "gemini-2.5-flash", ControlToggle},
		{"google", "gemini-2.5-flash-lite", ControlToggle},
	}
	for _, m := range shipped {
		cap, ok := c.Lookup(m.provider, m.api)
		if !ok {
			t.Errorf("%s/%s: not covered by the snapshot", m.provider, m.api)
			continue
		}
		if !cap.Supported() {
			t.Errorf("%s/%s: Supported=false, want a reasoning model", m.provider, m.api)
		}
		if !cap.HasControl(m.wantKind) {
			t.Errorf("%s/%s: missing expected control %q (controls=%+v)", m.provider, m.api, m.wantKind, cap.Controls)
		}
	}
}

// TestLookupReturnsDeepCopy pins that a caller cannot corrupt the shared
// embedded catalog by mutating a returned Capability's slices.
func TestLookupReturnsDeepCopy(t *testing.T) {
	c := Embedded()
	first, ok := c.Lookup("openai", "gpt-5")
	if !ok || len(first.Controls) == 0 || len(first.Controls[0].Values) == 0 {
		t.Fatal("setup: gpt-5 should have an effort control with values")
	}
	first.Controls[0].Values[0] = "MUTATED"
	first.Controls[0].Kind = "MUTATED"

	second, _ := c.Lookup("openai", "gpt-5")
	if second.Controls[0].Values[0] == "MUTATED" || second.Controls[0].Kind == "MUTATED" {
		t.Error("Lookup leaked a shared reference; a caller's mutation reached the catalog")
	}
}

func TestLookupMissFallsThrough(t *testing.T) {
	c := Embedded()
	if _, ok := c.Lookup("openai", "totally-made-up-model"); ok {
		t.Error("unknown model should miss")
	}
	if _, ok := c.Lookup("no-such-provider", "gpt-5"); ok {
		t.Error("unknown provider should miss")
	}
	if _, ok := c.Lookup("openai", "  "); ok {
		t.Error("blank api model should miss")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
