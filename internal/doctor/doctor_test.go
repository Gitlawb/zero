package doctor

import (
	"os"
	"path/filepath"
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

func writeDoctorConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestConfigValidationCheckPassesForValidConfig(t *testing.T) {
	path := writeDoctorConfig(t, `{
		"activeProvider": "main",
		"providers": [{"name": "main", "provider_kind": "openai", "model": "gpt-4.1"}]
	}`)

	report := Run(Options{Now: fixedDoctorClock("2026-06-08T10:00:00Z"), Runtime: "go", ProjectConfig: path})

	check := report.Check("config.validation")
	if check == nil || check.Status != StatusPass {
		t.Fatalf("expected config.validation pass, got %#v", report.Checks)
	}
}

func TestConfigValidationCheckFailsMalformedJSONWithLineCol(t *testing.T) {
	path := writeDoctorConfig(t, "{\n  \"activeProvider\": \"openai\",\n")

	report := Run(Options{Now: fixedDoctorClock("2026-06-08T10:05:00Z"), Runtime: "go", ProjectConfig: path})

	if report.OK {
		t.Fatalf("malformed config should fail report: %#v", report)
	}
	check := report.Check("config.validation")
	if check == nil || check.Status != StatusFail {
		t.Fatalf("expected config.validation fail, got %#v", report.Checks)
	}
	if check.Details["line"] == nil || check.Details["col"] == nil {
		t.Fatalf("expected line/col in details, got %#v", check.Details)
	}
}

func TestConfigValidationCheckFailsSemanticIssue(t *testing.T) {
	path := writeDoctorConfig(t, `{
		"activeProvider": "main",
		"providers": [{"name": "main", "provider_kind": "openai", "baseURL": "https://example.test/v1", "model": "gpt-4.1"}]
	}`)

	report := Run(Options{Now: fixedDoctorClock("2026-06-08T10:10:00Z"), Runtime: "go", ProjectConfig: path})

	check := report.Check("config.validation")
	if check == nil || check.Status != StatusFail {
		t.Fatalf("expected semantic fail, got %#v", report.Checks)
	}
}

func TestConfigValidationCheckSkippedWhenNoPaths(t *testing.T) {
	report := Run(Options{Now: fixedDoctorClock("2026-06-08T10:15:00Z"), Runtime: "go"})

	check := report.Check("config.validation")
	if check == nil || check.Status != StatusWarn {
		t.Fatalf("expected config.validation warn-skip, got %#v", report.Checks)
	}
}

func TestConfigValidationCheckDoesNotLeakSecret(t *testing.T) {
	path := writeDoctorConfig(t, `{
		"activeProvider": "main",
		"providers": [{"name": "main", "provider_kind": "openai", "baseURL": "https://example.test/v1", "apiKey": "sk-proj-secret1234567890", "model": "gpt-4.1"}]
	}`)

	report := Run(Options{Now: fixedDoctorClock("2026-06-08T10:20:00Z"), Runtime: "go", ProjectConfig: path})

	if strings.Contains(Format(report), "sk-proj-secret") {
		t.Fatalf("config.validation leaked apiKey: %q", Format(report))
	}
}

func TestOffsetToLineCol(t *testing.T) {
	data := []byte("{\n  \"a\": 1,\n  bad\n}")
	cases := []struct {
		name     string
		offset   int64
		wantLine int
		wantCol  int
	}{
		{name: "start", offset: 0, wantLine: 1, wantCol: 1},
		{name: "after first newline", offset: 2, wantLine: 2, wantCol: 1},
		{name: "mid second line", offset: 7, wantLine: 2, wantCol: 6},
		{name: "negative clamps to start", offset: -5, wantLine: 1, wantCol: 1},
		{name: "past end clamps to last", offset: 9999, wantLine: 4, wantCol: 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line, col := offsetToLineCol(data, tc.offset)
			if line != tc.wantLine || col != tc.wantCol {
				t.Fatalf("offsetToLineCol(%d) = (%d,%d), want (%d,%d)", tc.offset, line, col, tc.wantLine, tc.wantCol)
			}
		})
	}
}

func fixedDoctorClock(value string) func() time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return func() time.Time { return parsed }
}
