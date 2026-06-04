package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/Gitlawb/zero/internal/config"
)

func TestRunReportRedactsProviderSecretsAndWarnsWithoutConnectivity(t *testing.T) {
	report := Run(Options{
		Now:        fixedDoctorClock("2026-06-04T15:00:00Z"),
		Runtime:    "go",
		UserConfig: "missing",
		Provider: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      config.OpenAIBaseURL,
			APIKey:       "sk-proj-secret1234567890",
			Model:        "gpt-4.1",
		},
	})

	if !report.OK {
		t.Fatalf("report should be ok when only connectivity is skipped: %#v", report)
	}
	if got := report.Check("provider.config"); got == nil || got.Status != StatusPass {
		t.Fatalf("provider config check missing/pass expected: %#v", report.Checks)
	}
	formatted := Format(report)
	if strings.Contains(formatted, "sk-proj-secret") {
		t.Fatalf("formatted report leaked provider secret: %q", formatted)
	}
	if !strings.Contains(formatted, "[warn] provider.connectivity") {
		t.Fatalf("expected skipped connectivity warning: %q", formatted)
	}
}

func TestRunReportFailsInvalidModelAndMissingProvider(t *testing.T) {
	missing := Run(Options{Now: fixedDoctorClock("2026-06-04T15:30:00Z"), Runtime: "go"})
	if missing.OK {
		t.Fatalf("missing provider should fail: %#v", missing)
	}
	if check := missing.Check("provider.config"); check == nil || check.Status != StatusFail {
		t.Fatalf("expected provider config failure: %#v", missing.Checks)
	}

	invalid := Run(Options{
		Now:     fixedDoctorClock("2026-06-04T15:30:00Z"),
		Runtime: "go",
		Provider: config.ProviderProfile{
			Name:         "openai",
			ProviderKind: config.ProviderKindOpenAI,
			BaseURL:      config.OpenAIBaseURL,
			Model:        "not-a-zero-model",
		},
	})
	if invalid.OK {
		t.Fatalf("invalid model should fail: %#v", invalid)
	}
	if check := invalid.Check("provider.model"); check == nil || check.Status != StatusFail || !strings.Contains(check.Message, "unknown Zero model") {
		t.Fatalf("expected model failure: %#v", invalid.Checks)
	}
}

func TestRunReportWarnsForUnknownOpenAICompatibleModel(t *testing.T) {
	report := Run(Options{
		Now:     fixedDoctorClock("2026-06-04T15:45:00Z"),
		Runtime: "go",
		Provider: config.ProviderProfile{
			Name:         "local",
			ProviderKind: config.ProviderKindOpenAICompatible,
			BaseURL:      "http://127.0.0.1:11434/v1",
			Model:        "local-custom-model",
		},
	})

	if !report.OK {
		t.Fatalf("unknown custom model should warn, not fail: %#v", report)
	}
	if check := report.Check("provider.model"); check == nil || check.Status != StatusWarn || !strings.Contains(check.Message, "pass it through") {
		t.Fatalf("expected custom model warning: %#v", report.Checks)
	}
}

func fixedDoctorClock(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
}
