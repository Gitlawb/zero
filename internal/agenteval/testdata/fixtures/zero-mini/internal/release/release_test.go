package release

import "testing"

func TestSmokeTargetNamesLocalBinary(t *testing.T) {
	if got := SmokeTarget(true); got != "local-binary" {
		t.Fatalf("SmokeTarget(true) = %q, want local-binary", got)
	}
}
