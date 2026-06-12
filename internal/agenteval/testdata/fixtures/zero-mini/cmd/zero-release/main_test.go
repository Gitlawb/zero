package main

import "testing"

func TestSmokeTargetUsesLocalBinary(t *testing.T) {
	if got := smokeTarget(true); got != "local-binary" {
		t.Fatalf("smokeTarget(true) = %q, want local-binary", got)
	}
}
