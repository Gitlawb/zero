package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/config"
	"github.com/Gitlawb/zero/internal/modelregistry"
	"github.com/Gitlawb/zero/internal/redaction"
)

type Status string

const (
	StatusPass Status = "pass"
	StatusWarn Status = "warn"
	StatusFail Status = "fail"
)

type Check struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Status  Status         `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type Report struct {
	GeneratedAt string  `json:"generatedAt"`
	OK          bool    `json:"ok"`
	Checks      []Check `json:"checks"`
}

type Options struct {
	Now           func() time.Time
	Runtime       string
	UserConfig    string
	ProjectConfig string
	Provider      config.ProviderProfile
	Connectivity  bool
}

func Run(options Options) Report {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	checks := []Check{
		runtimeCheck(options.Runtime),
		configFilesCheck(options.UserConfig, options.ProjectConfig),
		configValidationCheck(options.UserConfig, options.ProjectConfig),
	}
	providerCheck := providerConfigCheck(options.Provider)
	checks = append(checks, providerCheck)
	modelCheck := providerModelCheck(options.Provider)
	checks = append(checks, modelCheck)
	checks = append(checks, connectivityCheck(options.Provider, options.Connectivity, modelCheck.Status))

	report := Report{
		GeneratedAt: now().UTC().Format(time.RFC3339),
		OK:          true,
		Checks:      checks,
	}
	for _, check := range checks {
		if check.Status == StatusFail {
			report.OK = false
			break
		}
	}
	return report
}

func (report Report) Check(id string) *Check {
	for index := range report.Checks {
		if report.Checks[index].ID == id {
			return &report.Checks[index]
		}
	}
	return nil
}

func Format(report Report) string {
	lines := []string{
		fmt.Sprintf("Zero doctor report (%s)", redaction.RedactString(report.GeneratedAt, redaction.Options{})),
		fmt.Sprintf("Overall: %s", passFail(report.OK)),
	}
	for _, check := range report.Checks {
		lines = append(lines, fmt.Sprintf("[%s] %s - %s", check.Status, redaction.RedactString(check.ID, redaction.Options{}), redaction.RedactString(check.Message, redaction.Options{})))
		if details := formatDetails(check.Details); details != "" {
			lines = append(lines, "  "+details)
		}
	}
	return strings.Join(lines, "\n")
}

func runtimeCheck(runtime string) Check {
	runtime = strings.TrimSpace(runtime)
	if runtime == "" {
		runtime = "go"
	}
	return check("runtime.go", "Go runtime", StatusPass, fmt.Sprintf("Zero Go runtime is available (%s).", runtime), map[string]any{"runtime": runtime})
}

func configFilesCheck(userPath string, projectPath string) Check {
	details := map[string]any{}
	if strings.TrimSpace(userPath) != "" {
		details["userConfigPath"] = userPath
	}
	if strings.TrimSpace(projectPath) != "" {
		details["projectConfigPath"] = projectPath
	}
	if len(details) == 0 {
		return check("config.files", "Config files", StatusWarn, "No explicit Zero config files were inspected.", nil)
	}
	return check("config.files", "Config files", StatusPass, "Zero config file inputs are available for inspection.", details)
}

func providerConfigCheck(profile config.ProviderProfile) Check {
	if emptyProviderProfile(profile) {
		return check("provider.config", "Provider config", StatusFail, "No LLM provider is configured.", map[string]any{"help": "Set a provider in config or environment."})
	}
	return check("provider.config", "Provider config", StatusPass, fmt.Sprintf("Provider config loaded for %s.", providerName(profile)), map[string]any{
		"name":     profile.Name,
		"provider": profile.ProviderKind,
		"baseURL":  profile.BaseURL,
		"model":    profile.Model,
		"apiKey":   profile.APIKey,
	})
}

func providerModelCheck(profile config.ProviderProfile) Check {
	if emptyProviderProfile(profile) {
		return check("provider.model", "Provider model", StatusWarn, "Model validity was skipped because provider config is unavailable.", nil)
	}
	registry, err := modelregistry.DefaultRegistry()
	if err != nil {
		return check("provider.model", "Provider model", StatusFail, "Model registry could not be loaded: "+err.Error(), nil)
	}
	model, err := registry.Require(profile.Model)
	if err != nil {
		if profile.ProviderKind == config.ProviderKindOpenAICompatible || profile.ProviderKind == config.ProviderKindAnthropicCompat {
			return check("provider.model", "Provider model", StatusWarn, fmt.Sprintf("Custom %s model was not found in the Zero registry; runtime will pass it through to the configured provider.", profile.ProviderKind), map[string]any{"model": profile.Model, "provider": providerName(profile)})
		}
		return check("provider.model", "Provider model", StatusFail, "Provider model is invalid: "+err.Error(), map[string]any{"model": profile.Model})
	}
	if !model.AllowsProvider(toModelProvider(profile)) {
		return check("provider.model", "Provider model", StatusFail, fmt.Sprintf("Model %s is not available for provider %s.", model.ID, providerName(profile)), map[string]any{"model": model.ID, "provider": providerName(profile)})
	}
	return check("provider.model", "Provider model", StatusPass, fmt.Sprintf("Model %s resolves to %s.", model.ID, model.Provider), map[string]any{
		"modelId":      model.ID,
		"apiModel":     model.APIModel,
		"provider":     model.Provider,
		"capabilities": model.Capabilities,
	})
}

func connectivityCheck(profile config.ProviderProfile, enabled bool, modelStatus Status) Check {
	if emptyProviderProfile(profile) || modelStatus == StatusFail {
		return check("provider.connectivity", "Provider connectivity", StatusWarn, "Connectivity check was skipped because provider runtime did not resolve.", nil)
	}
	if !enabled {
		return check("provider.connectivity", "Provider connectivity", StatusWarn, "Connectivity probe skipped. Run `zero doctor --connectivity` to probe the provider endpoint.", map[string]any{"baseURL": profile.BaseURL})
	}
	return check("provider.connectivity", "Provider connectivity", StatusWarn, "Connectivity probing is not wired in the Go doctor backend yet.", map[string]any{"baseURL": profile.BaseURL})
}

func emptyProviderProfile(profile config.ProviderProfile) bool {
	return !config.HasProviderProfile(profile)
}

func check(id string, label string, status Status, message string, details map[string]any) Check {
	redacted := redaction.RedactValue(map[string]any{
		"id":      id,
		"label":   label,
		"status":  string(status),
		"message": message,
		"details": details,
	}, redaction.Options{}).(map[string]any)
	out := Check{
		ID:      redacted["id"].(string),
		Label:   redacted["label"].(string),
		Status:  Status(redacted["status"].(string)),
		Message: redacted["message"].(string),
	}
	if detailsValue, ok := redacted["details"].(map[string]any); ok && len(detailsValue) > 0 {
		out.Details = detailsValue
	}
	return out
}

func providerName(profile config.ProviderProfile) string {
	if strings.TrimSpace(profile.Name) != "" {
		return strings.TrimSpace(profile.Name)
	}
	if strings.TrimSpace(string(profile.ProviderKind)) != "" {
		return strings.TrimSpace(string(profile.ProviderKind))
	}
	return strings.TrimSpace(profile.Provider)
}

func toModelProvider(profile config.ProviderProfile) modelregistry.ProviderKind {
	switch profile.ProviderKind {
	case config.ProviderKindAnthropic, config.ProviderKindAnthropicCompat:
		return modelregistry.ProviderAnthropic
	case config.ProviderKindGoogle:
		return modelregistry.ProviderGoogle
	case config.ProviderKindOpenAICompatible:
		return modelregistry.ProviderOpenAICompatible
	default:
		return modelregistry.ProviderOpenAI
	}
}

func formatDetails(details map[string]any) string {
	if len(details) == 0 {
		return ""
	}
	keys := make([]string, 0, len(details))
	for key := range details {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(details))
	for _, key := range keys {
		value := details[key]
		parts = append(parts, fmt.Sprintf("%s: %v", redaction.RedactString(key, redaction.Options{}), redaction.RedactValue(value, redaction.Options{})))
	}
	return strings.Join(parts, " | ")
}

func configValidationCheck(userPath string, projectPath string) Check {
	paths := make([]string, 0, 2)
	for _, path := range []string{userPath, projectPath} {
		if strings.TrimSpace(path) != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return check("config.validation", "Config validation", StatusWarn, "No Zero config files were available to validate.", nil)
	}

	status := StatusPass
	issueCount := 0
	details := map[string]any{}
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			// A genuinely-missing path is configFilesCheck's job to report, and a
			// "missing" path must stay a skip — so a not-exist read error is ignored
			// here. Any OTHER read error (permissions, is-a-directory) means a
			// present-but-unreadable config that would otherwise silently pass
			// validation, so surface it as a failing per-path detail.
			if os.IsNotExist(readErr) {
				continue
			}
			details[path] = map[string]any{"error": "unreadable: " + readErr.Error()}
			status = StatusFail
			issueCount++
			continue
		}
		if line, col, ok := jsonParsePosition(data); ok {
			_, issues := config.ValidateBytes(data)
			errMsg := ""
			if len(issues) > 0 {
				errMsg = issues[0].Message
			}
			details[path] = map[string]any{"line": line, "col": col, "error": errMsg}
			status = StatusFail
			issueCount++
			continue
		}
		_, issues := config.ValidateBytes(data)
		if len(issues) == 0 {
			continue
		}
		messages := make([]string, 0, len(issues))
		for _, issue := range issues {
			messages = append(messages, issue.Message)
		}
		details[path] = map[string]any{"issues": messages}
		status = StatusFail
		issueCount += len(issues)
	}

	if status == StatusPass {
		return check("config.validation", "Config validation", StatusPass, "Zero config files parsed and validated successfully.", nil)
	}
	return check("config.validation", "Config validation", StatusFail, fmt.Sprintf("Zero config validation found %d issue(s).", issueCount), details)
}

// jsonParsePosition reports whether data fails to parse as JSON and, if so, the
// 1-based line/col of the failure using the concrete json error offset.
//
// The probe is a config.FileConfig (not `any`) so that a structurally-valid
// document with a wrong field type (e.g. {"maxTurns":"twelve"}) surfaces a
// *json.UnmarshalTypeError carrying the offset. FileConfig.UnmarshalJSON returns
// the underlying json error unchanged, preserving that offset. Documents that
// are structurally valid AND type-correct unmarshal cleanly (ok=false) and fall
// through to the semantic ValidateBytes branch.
func jsonParsePosition(data []byte) (int, int, bool) {
	var probe config.FileConfig
	err := json.Unmarshal(data, &probe)
	if err == nil {
		return 0, 0, false
	}
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) {
		line, col := offsetToLineCol(data, syntaxErr.Offset)
		return line, col, true
	}
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		line, col := offsetToLineCol(data, typeErr.Offset)
		return line, col, true
	}
	// Non-positional JSON error (e.g. unexpected EOF without offset): do not
	// fabricate a (1,1) position. Returning ok=false routes the error through the
	// no-position ValidateBytes path so it is reported without a fake line/col.
	return 0, 0, false
}

func passFail(ok bool) string {
	if ok {
		return "pass"
	}
	return "fail"
}

// offsetToLineCol converts a byte offset (as reported by *json.SyntaxError /
// *json.UnmarshalTypeError) into a 1-based line and column. Offsets out of range
// are clamped into [0, len(data)].
func offsetToLineCol(data []byte, offset int64) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	line := 1
	col := 1
	for index := int64(0); index < offset; index++ {
		if data[index] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}
