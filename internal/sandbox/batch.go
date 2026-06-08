package sandbox

// RequiredAutonomyForBatch returns the highest autonomy tier required to run a
// batch of operations without further prompting, given each operation's
// classified risk. RiskCritical and RiskHigh both map to AutonomyHigh because
// the autonomy ladder has three tiers and the risk ladder has four. Any risk
// level that is empty or unrecognised fails closed to AutonomyHigh so an
// unclassified operation can never silently lower the batch ceiling. An empty
// batch requires only AutonomyLow.
func RequiredAutonomyForBatch(risks []Risk) Autonomy {
	required := AutonomyLow
	for _, risk := range risks {
		tier := riskToAutonomy(risk.Level)
		if autonomyRank[tier] > autonomyRank[required] {
			required = tier
		}
	}
	return required
}

func riskToAutonomy(level RiskLevel) Autonomy {
	switch level {
	case RiskLow:
		return AutonomyLow
	case RiskMedium:
		return AutonomyMedium
	case RiskHigh, RiskCritical:
		return AutonomyHigh
	default:
		// Unknown / empty risk is treated as the highest tier (fail-closed).
		return AutonomyHigh
	}
}
