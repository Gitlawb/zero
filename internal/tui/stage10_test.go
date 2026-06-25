package tui

import (
	"testing"
)

func TestAssistantMeasureCap(t *testing.T) {
	if got := assistantMeasure(200); got != assistantMeasureCap {
		t.Errorf("wide: assistantMeasure(200) = %d, want %d", got, assistantMeasureCap)
	}
	if got := assistantMeasure(80); got != 80 {
		t.Errorf("under cap: assistantMeasure(80) = %d, want 80", got)
	}
	if got := assistantMeasure(5); got != 16 {
		t.Errorf("floor: assistantMeasure(5) = %d, want 16", got)
	}
	if assistantMeasureCap < 80 || assistantMeasureCap > 100 {
		t.Errorf("cap %d outside the 80-100 readability range", assistantMeasureCap)
	}
}
