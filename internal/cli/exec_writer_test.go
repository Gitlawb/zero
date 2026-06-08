package cli

import (
	"testing"

	"github.com/Gitlawb/zero/internal/tools"
)

// escalate_model is a control-only tool (SideEffectNone). The stream-json tool
// call must report sideEffect "none", not "unknown", so automation sees the
// promised value.
func TestStreamJSONSideEffectReportsNoneForControlTool(t *testing.T) {
	registry := tools.NewRegistry()
	registry.Register(tools.NewEscalateModelTool())
	if got := streamJSONSideEffect("escalate_model", registry); got != "none" {
		t.Fatalf("streamJSONSideEffect(escalate_model) = %q, want none", got)
	}
}
