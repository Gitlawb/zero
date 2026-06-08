package tools

import (
	"strings"
	"testing"
)

func TestFormatBashOutputRedactsSecrets(t *testing.T) {
	out := formatBashOutput("aws key: AKIAIOSFODNN7EXAMPLE\n", "", 0)
	if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
		t.Fatalf("bash output leaked a secret: %q", out)
	}
	if !strings.Contains(out, "[REDACTED:aws_access_key_id]") {
		t.Fatalf("expected typed redaction placeholder, got %q", out)
	}
	if !strings.Contains(out, "redacted 1 likely secret") {
		t.Fatalf("expected a redaction notice, got %q", out)
	}
}

func TestFormatBashOutputLeavesCleanOutputAlone(t *testing.T) {
	out := formatBashOutput("build succeeded\n", "", 0)
	if strings.Contains(out, "REDACTED") || strings.Contains(out, "redacted") {
		t.Fatalf("clean output should not be altered, got %q", out)
	}
}
