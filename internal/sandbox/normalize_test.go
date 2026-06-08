package sandbox

import "testing"

func TestAutonomyRankOrdering(t *testing.T) {
	if !(autonomyRank[AutonomyLow] < autonomyRank[AutonomyMedium] && autonomyRank[AutonomyMedium] < autonomyRank[AutonomyHigh]) {
		t.Fatalf("autonomyRank not strictly ordered: %v", autonomyRank)
	}
}

func TestAutonomyAllowedNormalizesBothOperands(t *testing.T) {
	cases := []struct {
		name      string
		requested Autonomy
		max       Autonomy
		want      bool
	}{
		{"low under high", AutonomyLow, AutonomyHigh, true},
		{"high under medium ceiling", AutonomyHigh, AutonomyMedium, false},
		{"equal medium", AutonomyMedium, AutonomyMedium, true},
		{"uppercase requested still ranked", Autonomy("HIGH"), AutonomyMedium, false},
		{"unknown requested fails closed against high ceiling", Autonomy("bogus"), AutonomyHigh, false},
		{"unknown ceiling fails closed against high request", AutonomyHigh, Autonomy("bogus"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := autonomyAllowed(tc.requested, tc.max); got != tc.want {
				t.Fatalf("autonomyAllowed(%q, %q) = %v, want %v", tc.requested, tc.max, got, tc.want)
			}
		})
	}
}
