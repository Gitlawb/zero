package review

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const Marker = "<!-- zero-auto-review -->"

type Outcome string

const (
	OutcomeSuccess        Outcome = "success"
	OutcomeFailure        Outcome = "failure"
	OutcomeCancelled      Outcome = "cancelled"
	OutcomeSkipped        Outcome = "skipped"
	OutcomeTimedOut       Outcome = "timed_out"
	OutcomeActionRequired Outcome = "action_required"
	OutcomeNeutral        Outcome = "neutral"
	OutcomeUnknown        Outcome = "unknown"
)

type Check struct {
	Label   string  `json:"label"`
	Command string  `json:"command"`
	Outcome Outcome `json:"outcome"`
}

type SummaryInput struct {
	Number       int
	HeadSHA      string
	Checks       []Check
	ChangedFiles []string
}

type checkSpec struct {
	Env     string
	Label   string
	Command string
}

var defaultCheckSpecs = []checkSpec{
	{Env: "ZERO_REVIEW_DIFF_CHECK", Label: "Diff hygiene", Command: "git diff --check"},
	{Env: "ZERO_REVIEW_TYPECHECK", Label: "Typecheck", Command: "bun run typecheck"},
	{Env: "ZERO_REVIEW_TEST", Label: "Tests", Command: "bun run test"},
	{Env: "ZERO_REVIEW_BUILD", Label: "Build", Command: "bun run build"},
	{Env: "ZERO_REVIEW_SMOKE", Label: "Smoke build", Command: "bun run smoke:build"},
}

func NormalizeOutcome(value string) Outcome {
	normalized := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(value)), "-", "_")
	switch Outcome(normalized) {
	case OutcomeSuccess,
		OutcomeFailure,
		OutcomeCancelled,
		OutcomeSkipped,
		OutcomeTimedOut,
		OutcomeActionRequired,
		OutcomeNeutral:
		return Outcome(normalized)
	default:
		return OutcomeUnknown
	}
}

func BuildChecksFromEnv(env map[string]string) []Check {
	checks := make([]Check, 0, len(defaultCheckSpecs))
	for _, spec := range defaultCheckSpecs {
		checks = append(checks, Check{
			Label:   spec.Label,
			Command: spec.Command,
			Outcome: NormalizeOutcome(env[spec.Env]),
		})
	}
	return checks
}

func HasBlockingChecks(checks []Check) bool {
	for _, check := range checks {
		if IsBlocking(check.Outcome) {
			return true
		}
	}
	return false
}

func IsBlocking(outcome Outcome) bool {
	switch outcome {
	case OutcomeFailure, OutcomeCancelled, OutcomeTimedOut, OutcomeActionRequired, OutcomeUnknown:
		return true
	default:
		return false
	}
}

func BuildSummaryInputFromEnv(env map[string]string) SummaryInput {
	return SummaryInput{
		Number:       parsePRNumber(env),
		HeadSHA:      firstNonEmpty(env["ZERO_REVIEW_HEAD_SHA"], env["GITHUB_SHA"]),
		Checks:       BuildChecksFromEnv(env),
		ChangedFiles: ParseChangedFiles(env["ZERO_CHANGED_FILES"]),
	}
}

func BuildMarkdown(input SummaryInput) string {
	blockers := blockingChecks(input.Checks)
	lines := []string{
		Marker,
		"## Zero automated PR review",
		"",
		fmt.Sprintf("Verdict: **%s**", verdict(blockers)),
		"",
		"### Blockers",
		"",
	}
	if len(blockers) == 0 {
		lines = append(lines, "- None found.")
	} else {
		for _, check := range blockers {
			lines = append(lines, fmt.Sprintf("- `%s` ended with `%s`.", check.Command, check.Outcome))
		}
	}

	lines = append(lines,
		"",
		"### Validation",
		"",
	)
	for _, check := range input.Checks {
		lines = append(lines, fmt.Sprintf("- %s %s: `%s`", FormatOutcome(check.Outcome), check.Label, check.Command))
	}

	lines = append(lines,
		"",
		"### Scope",
		"",
		scopeHeadLine(input),
		scopeChangedFiles(input.ChangedFiles),
		"",
		"This deterministic review checks validation status and basic diff hygiene. A human reviewer still owns product judgment and design quality.",
	)
	return strings.Join(lines, "\n")
}

func FormatOutcome(outcome Outcome) string {
	switch outcome {
	case OutcomeSuccess:
		return "[pass]"
	case OutcomeSkipped, OutcomeNeutral:
		return "[info]"
	default:
		return "[fail]"
	}
}

func ParseChangedFiles(value string) []string {
	lines := strings.FieldsFunc(value, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		if file := strings.TrimSpace(line); file != "" {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	return files
}

func blockingChecks(checks []Check) []Check {
	blockers := []Check{}
	for _, check := range checks {
		if IsBlocking(check.Outcome) {
			blockers = append(blockers, check)
		}
	}
	return blockers
}

func verdict(blockers []Check) string {
	if len(blockers) > 0 {
		return "Changes requested"
	}
	return "No blockers found"
}

func scopeHeadLine(input SummaryInput) string {
	if strings.TrimSpace(input.HeadSHA) != "" {
		return fmt.Sprintf("Head: `%s`", truncateSHA(input.HeadSHA))
	}
	return fmt.Sprintf("PR: #%d", input.Number)
}

func scopeChangedFiles(files []string) string {
	if len(files) == 0 {
		return "Changed files: unavailable in this run."
	}
	visible := files
	if len(visible) > 12 {
		visible = visible[:12]
	}
	quoted := make([]string, 0, len(visible))
	for _, file := range visible {
		quoted = append(quoted, fmt.Sprintf("`%s`", file))
	}
	suffix := ""
	if len(files) > len(visible) {
		suffix = fmt.Sprintf(", and %d more", len(files)-len(visible))
	}
	return fmt.Sprintf("Changed files (%d): %s%s", len(files), strings.Join(quoted, ", "), suffix)
}

func truncateSHA(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) <= 12 {
		return trimmed
	}
	return trimmed[:12]
}

func parsePRNumber(env map[string]string) int {
	value := firstNonEmpty(env["ZERO_PR_NUMBER"], strings.Split(env["GITHUB_REF_NAME"], "/")[0])
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return number
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
