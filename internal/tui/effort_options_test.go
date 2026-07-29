package tui

import (
	"strings"
	"testing"

	"github.com/Gitlawb/zero/internal/execprofile"
)

// THE CARD AND THE COMMAND ARE TWO DOORS onto "which efforts can I set?", so
// the relationship is EQUALITY (RULES.md §3): every value /effort offers must
// be one /effort accepts, and refusing something it just advertised is the same
// disagreement in the other direction.
//
// Asserted by feeding each advertised value back through the real command
// rather than by comparing two lists — a list comparison would pass while the
// command rejected them all.
func TestEveryAdvertisedEffortIsAccepted(t *testing.T) {
	for _, name := range []string{"glm-5.2", "claude-sonnet-4.5", "gpt-4o", "gpt-5-mini"} {
		t.Run(name, func(t *testing.T) {
			m := model{modelName: name}
			for _, value := range m.settableEfforts() {
				_, out := m.handleEffortCommand(value)
				for _, refusal := range []string{"Unknown reasoning effort", "is not supported by", "does not expose reasoning effort"} {
					if strings.Contains(out, refusal) {
						t.Errorf("the card offers %q on %s but the command refuses it: %s", value, name, out)
					}
				}
			}
		})
	}
}

// A model the catalog VOUCHES has no reasoning controls must not be offered
// any. gpt-4o is catalogued with an empty ring, and offering low/medium/high
// there would advertise exactly what the command rejects.
func TestACataloguedModelWithNoControlsOffersOnlyThePosture(t *testing.T) {
	m := model{modelName: "gpt-4o"}
	settable := m.settableEfforts()
	if len(settable) != 1 || settable[0] != execprofile.Name {
		t.Fatalf("settable = %v, want only the posture: the catalog says this model has no reasoning controls", settable)
	}
}

// An UNCATALOGUED model is a different case: Zero cannot vouch either way, the
// levels are forwarded, and telling the user "not listed" while showing nothing
// typeable leaves them with no way to act.
func TestAnUncataloguedModelStillOffersTheLevels(t *testing.T) {
	m := model{modelName: "glm-5.2"}
	card := m.effortText()
	if !strings.Contains(card, "you can set") {
		t.Fatalf("an uncatalogued model must still say what can be set:\n%s", card)
	}
	for _, want := range []string{"low", "medium", "high", execprofile.Name} {
		if !strings.Contains(card, want) {
			t.Errorf("the card does not offer %q on an uncatalogued model:\n%s", want, card)
		}
	}
}

// The posture is selected through this namespace, so it belongs in the list on
// every model — it was missing from all of them while the actions line offered
// it.
func TestThePostureIsOfferedOnEveryModel(t *testing.T) {
	for _, name := range []string{"glm-5.2", "claude-sonnet-4.5", "gpt-4o"} {
		m := model{modelName: name}
		if !strings.Contains(strings.Join(m.settableEfforts(), ","), execprofile.Name) {
			t.Errorf("%s does not offer %q, which /effort accepts", name, execprofile.Name)
		}
	}
}

// ...and NOT when the workspace disabled it. Advertising a command the run will
// refuse is worse than saying nothing.
func TestADisabledPostureIsNotOffered(t *testing.T) {
	m := model{modelName: "glm-5.2", zeromaxingDisabled: true}
	if strings.Contains(strings.Join(m.settableEfforts(), ","), execprofile.Name) {
		t.Error("the posture is disabled for this workspace and must not be offered")
	}
	if strings.Contains(m.effortText(), "for the maximal posture") {
		t.Error("the actions line still suggests a command the run will refuse")
	}
}

// With no model selected there is nothing to claim support for, but the posture
// is a Zero-side setting and stays available.
func TestWithNoModelOnlyThePostureIsOffered(t *testing.T) {
	m := model{}
	settable := m.settableEfforts()
	if len(settable) != 1 || settable[0] != execprofile.Name {
		t.Fatalf("settable = %v, want only the posture when no model is selected", settable)
	}
}
