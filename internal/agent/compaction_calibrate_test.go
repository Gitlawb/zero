package agent

import "testing"

func TestCalibrationConvergesTowardActual(t *testing.T) {
	state := &compactionState{enabled: true}

	// Before any sample, the estimate is unchanged.
	if got := state.calibratedTokens(1000); got != 1000 {
		t.Fatalf("uncalibrated tokens = %d, want 1000", got)
	}

	// The estimator over-counts: our raw estimate is 1000 but the provider reports
	// 850 (a 0.85 ratio). Feeding that sample repeatedly should pull the ratio
	// toward 0.85, so a 1000-token raw estimate calibrates downward.
	for range 20 {
		state.calibrate(1000, 850)
	}
	got := state.calibratedTokens(1000)
	if got >= 1000 || got < 820 || got > 880 {
		t.Fatalf("calibrated tokens = %d, want ~850 after convergence", got)
	}
}

func TestCalibrateIgnoresDegenerateSamples(t *testing.T) {
	state := &compactionState{enabled: true}
	state.calibrate(0, 500) // zero estimate
	state.calibrate(500, 0) // zero actual
	if state.calibrationRatio != 0 {
		t.Fatalf("degenerate samples must not move the ratio, got %v", state.calibrationRatio)
	}
	// Disabled state never calibrates.
	disabled := &compactionState{enabled: false}
	disabled.calibrate(1000, 850)
	if disabled.calibrationRatio != 0 {
		t.Fatalf("disabled compaction must not calibrate, got %v", disabled.calibrationRatio)
	}
}

func TestCalibrateClampsOutliers(t *testing.T) {
	state := &compactionState{enabled: true}
	// A wild outlier (10x) is clamped to 2.0, so one bad sample can't blow up the
	// ratio and disable compaction.
	for range 50 {
		state.calibrate(1000, 10000)
	}
	if state.calibrationRatio > 2.0 {
		t.Fatalf("ratio should be clamped at 2.0, got %v", state.calibrationRatio)
	}
}
