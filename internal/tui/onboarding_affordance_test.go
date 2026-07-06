package tui

import (
	"strings"
	"testing"
)

// Each input/choice stage's error affordance names an accurate recovery step;
// stages without a tailored hint return empty.
func TestSetupErrorAffordance(t *testing.T) {
	endpoint := model{}
	endpoint.setup.stage = setupStageEndpoint
	if got := endpoint.setupErrorAffordance(); !strings.Contains(got, "endpoint") || !strings.Contains(got, "Enter") {
		t.Fatalf("endpoint affordance = %q", got)
	}

	name := model{}
	name.setup.stage = setupStageName
	if got := name.setupErrorAffordance(); !strings.Contains(got, "name") {
		t.Fatalf("name affordance = %q", got)
	}

	// A stage with no tailored hint (method chooser) returns empty.
	method := model{}
	method.setup.stage = setupStageMethod
	if got := method.setupErrorAffordance(); got != "" {
		t.Fatalf("method stage should have no affordance, got %q", got)
	}
}
