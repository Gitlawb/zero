package gemini

import (
	"testing"

	"github.com/Gitlawb/zero/internal/zeroruntime"
)

func TestMapFinishReasonNonNormal(t *testing.T) {
	for _, normal := range []string{"", "STOP", "FINISH_REASON_UNSPECIFIED"} {
		if got := mapFinishReason(normal); got != "" {
			t.Errorf("%q should be a normal stop (empty), got %q", normal, got)
		}
	}
	for _, cf := range []string{"SAFETY", "RECITATION", "IMAGE_SAFETY", "PROHIBITED_CONTENT", "BLOCKLIST", "SPII"} {
		if got := mapFinishReason(cf); got != zeroruntime.FinishReasonContentFilter {
			t.Errorf("%q → %q, want content_filter (M3)", cf, got)
		}
	}
	if got := mapFinishReason("MAX_TOKENS"); got != zeroruntime.FinishReasonLength {
		t.Errorf("MAX_TOKENS → %q, want length", got)
	}
	// Remaining non-STOP reasons surface the raw reason (non-empty) so the turn is
	// not mistaken for a clean completion.
	if got := mapFinishReason("MALFORMED_FUNCTION_CALL"); got != "MALFORMED_FUNCTION_CALL" {
		t.Errorf("MALFORMED_FUNCTION_CALL → %q, want the raw reason", got)
	}
}
