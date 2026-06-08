package sandbox

import "testing"

func TestRequiredAutonomyForBatch(t *testing.T) {
	cases := []struct {
		name  string
		risks []Risk
		want  Autonomy
	}{
		{"empty slice is low", nil, AutonomyLow},
		{"single low", []Risk{{Level: RiskLow}}, AutonomyLow},
		{"mixed low and medium is medium", []Risk{{Level: RiskLow}, {Level: RiskMedium}}, AutonomyMedium},
		{"any high is high", []Risk{{Level: RiskLow}, {Level: RiskHigh}}, AutonomyHigh},
		{"any critical is high", []Risk{{Level: RiskMedium}, {Level: RiskCritical}}, AutonomyHigh},
		{"unknown risk fails closed to high", []Risk{{Level: RiskLevel("weird")}}, AutonomyHigh},
		{"empty-level risk fails closed to high", []Risk{{Level: RiskLevel("")}}, AutonomyHigh},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := RequiredAutonomyForBatch(tc.risks); got != tc.want {
				t.Fatalf("RequiredAutonomyForBatch(%v) = %q, want %q", tc.risks, got, tc.want)
			}
		})
	}
}
