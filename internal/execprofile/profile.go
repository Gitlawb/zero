// Package execprofile defines named execution profiles: bundles of loop-posture
// knobs (turn budget, reasoning effort, self-correction, escalation triggers)
// that tune how hard the agent loop tries, independent of which model runs.
// Profiles COMPOSE with --mode: a mode answers "what runs" (model, tools),
// a profile answers "how hard the loop tries". Selection precedence is
// explicit flag > --mode > profile, enforced by the callers applying profiles
// last with the same fill-only-if-unset rule modes use.
//
// The catalog is deliberately tiny and value-stable: balanced is the empty
// profile (a balanced run is byte-identical to an unflagged run — asserted by
// test, not claimed), fast trades turn budget and effort for latency with a
// one-shot escalation back to the displaced posture as the safety net, and
// thorough raises the budget and arms full self-correction.
package execprofile

import (
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/sandbox"
)

// Profile is a named execution posture. Zero-valued fields inherit the run's
// existing value (flag, mode, config, or built-in default) — a profile only
// ever fills knobs the caller left unset, except MaxTurns which REPLACES the
// resolved budget (that displacement is what escalation restores).
type Profile struct {
	// Name identifies the profile in traces, help text, and diagnostics.
	Name string
	// MaxTurns replaces the resolved per-run tool-turn budget when > 0 and the
	// user did not pass an explicit --max-turns. The displaced resolved value
	// becomes the escalation target (see Policy).
	MaxTurns int
	// ReasoningEffort fills the run's effort when the user left it unset. It
	// still flows through the model-supported gating at the call site, so an
	// unsupported level degrades exactly like an explicit flag would.
	ReasoningEffort string
	// SelfCorrect arms the post-edit verify-and-correct loop. Profiles can only
	// turn it ON (the flag is presence-only, so false is indistinguishable from
	// unset and must never override an explicit opt-in).
	SelfCorrect bool

	// Escalation triggers. All zero means the profile never escalates and
	// Policy returns nil (the loop stays byte-identical to a nil-profile run).
	// Targets are NOT part of the catalog: escalation restores the values the
	// profile displaced at selection time, never invented ones, so targets are
	// stamped by Policy from what the caller measured.
	EscalateOnToolFailureStreak   int
	EscalateOnCompletionUncertain int
	EscalateOnSelfCorrectFailure  bool
	EscalateOnRiskyMutation       sandbox.RiskLevel
}

// The catalog. Values marked provisional are floors/ceilings derived from the
// Phase 0 baseline's read-class evidence (successful nav runs used single-digit
// turns; the mutating classes produced no successful samples to tune from) and
// are expected to be re-tuned from the post-oracle-fix re-capture. The
// escalation safety net is what makes shipping provisional Fast values safe:
// a run that hits trouble gets its displaced budget back mid-run.
var (
	// Balanced is the default posture: empty on purpose. Selecting it changes
	// nothing — the no-regression invariant of the whole feature.
	Balanced = Profile{Name: "balanced"}

	// Fast starts cheap (30 turns, low effort) and arms every escalation
	// trigger so a struggling run restores its displaced budget: two
	// same-tool retriable failures in a row, a second uncertain completion
	// evaluation, a failing self-correction cycle, or a high-risk mutation.
	Fast = Profile{
		Name:                          "fast",
		MaxTurns:                      30,
		ReasoningEffort:               "low",
		EscalateOnToolFailureStreak:   2,
		EscalateOnCompletionUncertain: 2,
		EscalateOnSelfCorrectFailure:  true,
		EscalateOnRiskyMutation:       sandbox.RiskHigh,
	}

	// Thorough doubles the default budget, asks for high effort, and arms the
	// full post-edit verify-and-correct loop. Already the maximum posture, so
	// it has nothing to escalate to.
	Thorough = Profile{
		Name:            "thorough",
		MaxTurns:        160,
		ReasoningEffort: "high",
		SelfCorrect:     true,
	}
)

var catalog = map[string]Profile{
	Balanced.Name: Balanced,
	Fast.Name:     Fast,
	Thorough.Name: Thorough,
}

// Lookup resolves a profile by name, case-insensitively and ignoring
// surrounding whitespace.
func Lookup(name string) (Profile, bool) {
	profile, ok := catalog[strings.ToLower(strings.TrimSpace(name))]
	return profile, ok
}

// Names returns the catalog's profile names, sorted, for usage errors and help.
func Names() []string {
	names := make([]string, 0, len(catalog))
	for name := range catalog {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Policy projects the profile into the loop-facing ProfilePolicy.
//
// displacedMaxTurns is the turn budget this profile displaced at selection time
// (what the run would have used without it) and becomes the escalation's
// restore target; pass 0 when the profile did not displace the budget (e.g.
// the user pinned --max-turns explicitly) so escalation leaves the ceiling
// untouched. A displaced reasoning effort is always "" by construction —
// profiles only fill effort when it was unset — so the escalation carries no
// effort target (zero-valued targets are no-ops in the loop).
//
// Profiles with no armed triggers return nil: the loop treats a nil policy as
// "no profile" (no observation, no counters), which keeps balanced and
// thorough runs byte-identical to the same knob values set by hand.
func (p Profile) Policy(displacedMaxTurns int) *agent.ProfilePolicy {
	if p.EscalateOnToolFailureStreak == 0 && p.EscalateOnCompletionUncertain == 0 &&
		!p.EscalateOnSelfCorrectFailure && p.EscalateOnRiskyMutation == "" {
		return nil
	}
	return &agent.ProfilePolicy{
		Name: p.Name,
		Escalate: &agent.PostureEscalation{
			MaxTurns:              displacedMaxTurns,
			OnToolFailureStreak:   p.EscalateOnToolFailureStreak,
			OnCompletionUncertain: p.EscalateOnCompletionUncertain,
			OnSelfCorrectFailure:  p.EscalateOnSelfCorrectFailure,
			OnRiskyMutation:       p.EscalateOnRiskyMutation,
		},
	}
}
