// Package reasoning models how each provider's models expose reasoning-effort
// control. Providers disagree on the concept — OpenAI uses a discrete effort
// enum, Anthropic and Gemini 2.5 use a thinking-token budget, some models only
// toggle thinking on/off — so a single flat effort string cannot describe them.
//
// Capability is the typed, per-model description of that control, sourced from a
// community capability catalog (models.dev). It is the data the rest of Zero
// consults to decide which reasoning tiers a model actually supports, replacing
// model-name guessing. This package depends only on the standard library, so the
// model registry and provider adapters can import it without a cycle.
package reasoning

import "strings"

// ControlKind enumerates how a model exposes reasoning control. The values
// mirror the models.dev reasoning_options[].type field.
type ControlKind string

const (
	// ControlEffort is a discrete effort enum (OpenAI reasoning_effort, Gemini 3
	// thinkingLevel, newer Claude output_config.effort).
	ControlEffort ControlKind = "effort"
	// ControlBudget is a thinking-token budget (Gemini 2.5 thinkingBudget, legacy
	// Claude thinking.budget_tokens).
	ControlBudget ControlKind = "budget_tokens"
	// ControlToggle is an on/off thinking switch with no levels.
	ControlToggle ControlKind = "toggle"
)

// Control is one reasoning control a model accepts. For an effort control,
// Values lists the accepted tiers ordered weakest to strongest. For a budget
// control, Min/Max bound the thinking-token budget (0 means "unspecified").
type Control struct {
	Kind   ControlKind `json:"type"`
	Values []string    `json:"values,omitempty"`
	Min    int         `json:"min,omitempty"`
	Max    int         `json:"max,omitempty"`
}

// Capability is the reasoning capability of a single model: whether it reasons
// at all, and through which controls. A model may carry more than one control
// (e.g. a newer Claude model exposes both an effort enum and a token budget); a
// reasoning model with no controls reasons but exposes no knob (always-on).
type Capability struct {
	Reasoning bool      `json:"reasoning"`
	Controls  []Control `json:"reasoning_options,omitempty"`
}

// Supported reports whether the model performs any reasoning.
func (c Capability) Supported() bool { return c.Reasoning }

// EffortControl returns the model's effort control and whether it has one.
func (c Capability) EffortControl() (Control, bool) {
	for _, ctrl := range c.Controls {
		if ctrl.Kind == ControlEffort {
			return ctrl, true
		}
	}
	return Control{}, false
}

// EffortValues returns the ordered effort tiers the model accepts, or nil when
// it has no effort control (a budget- or toggle-only model, or a non-reasoning
// model).
func (c Capability) EffortValues() []string {
	if ctrl, ok := c.EffortControl(); ok {
		return ctrl.Values
	}
	return nil
}

// SupportsEffort reports whether tier is one of the model's accepted effort
// values (case-insensitive).
func (c Capability) SupportsEffort(tier string) bool {
	tier = strings.ToLower(strings.TrimSpace(tier))
	for _, v := range c.EffortValues() {
		if strings.ToLower(v) == tier {
			return true
		}
	}
	return false
}

// Budget returns the model's token-budget bounds and whether it has a budget
// control.
func (c Capability) Budget() (min, max int, ok bool) {
	for _, ctrl := range c.Controls {
		if ctrl.Kind == ControlBudget {
			return ctrl.Min, ctrl.Max, true
		}
	}
	return 0, 0, false
}

// HasControl reports whether the model exposes a control of the given kind.
func (c Capability) HasControl(kind ControlKind) bool {
	for _, ctrl := range c.Controls {
		if ctrl.Kind == kind {
			return true
		}
	}
	return false
}
