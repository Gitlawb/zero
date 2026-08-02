package secrets

import "testing"

// Kebab-case words must NOT be redacted: "sk-" inside "task"/"ask"/"risk"/etc.
// is not an openai_key. This is the over-redaction the \b anchors fix.
func TestScan_NoOverRedactionOnKebabWords(t *testing.T) {
	clean := []string{
		"see task-management-and-coordination-plan for the roadmap",
		"we should ask-the-user-about-this-before-proceeding now",
		"the risk-assessment-and-mitigation-strategy-doc is ready",
		"disk-usage-monitoring-and-alerting-subsystem looks good",
		"desk-booking-and-reservation-management-flow updated",
	}
	for _, c := range clean {
		red, f := Redact(c)
		if len(f) != 0 || red != c {
			t.Errorf("over-redacted %q -> %q (findings=%d)", c, red, len(f))
		}
	}
}

// Real secrets preceded by a delimiter (space, =, :, quote, start) still match —
// the \b is satisfied by all of those, so the fix loses no real coverage.
func TestScan_RealSecretsStillCaught(t *testing.T) {
	cases := []string{
		"export OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwx",
		`token: "sk-abcdefghijklmnopqrstuvwxyz"`,
		"key is sk-svcacct-abcdefghijklmnopqrstuvwx at the end",
		"github_pat_11ABCDEFG0abcdefghijklmnopqrst",
		"creds AKIAIOSFODNN7EXAMPLE here",
	}
	for _, c := range cases {
		if _, f := Redact(c); len(f) == 0 {
			t.Errorf("real secret NOT caught in %q", c)
		}
	}
}
