package testrunner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Kind describes the broad purpose of a detected verification check.
type Kind string

const (
	KindTest      Kind = "test"
	KindTypecheck Kind = "typecheck"
	KindBuild     Kind = "build"
	KindLint      Kind = "lint"
)

// Framework identifies the runner family used to execute or parse a check.
type Framework string

const (
	FrameworkGo     Framework = "go"
	FrameworkBun    Framework = "bun"
	FrameworkNode   Framework = "node"
	FrameworkPytest Framework = "pytest"
	FrameworkCargo  Framework = "cargo"
)

// Check is a runnable workspace verification command discovered from project files.
type Check struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Command   []string  `json:"command"`
	Kind      Kind      `json:"kind"`
	Framework Framework `json:"framework"`
}

// Summary is the structured test result parsed from runner output.
type Summary struct {
	Framework Framework `json:"framework"`
	Total     int       `json:"total,omitempty"`
	Passed    int       `json:"passed,omitempty"`
	Failed    int       `json:"failed,omitempty"`
	Skipped   int       `json:"skipped,omitempty"`
	Failures  []Failure `json:"failures,omitempty"`
}

// Failure captures the most useful location and message for a failing test.
type Failure struct {
	Name    string `json:"name"`
	File    string `json:"file,omitempty"`
	Message string `json:"message,omitempty"`
}

type packageJSON struct {
	PackageManager string            `json:"packageManager"`
	Scripts        map[string]string `json:"scripts"`
}

type packageManager struct {
	Name      string
	IDPrefix  string
	Framework Framework
}

var (
	goRunPattern       = regexp.MustCompile(`^=== RUN\s+(.+)$`)
	goPassPattern      = regexp.MustCompile(`^--- PASS:\s+([^\s(]+)`)
	goFailPattern      = regexp.MustCompile(`^--- FAIL:\s+([^\s(]+)`)
	goSkipPattern      = regexp.MustCompile(`^--- SKIP:\s+([^\s(]+)`)
	goFailureLocation  = regexp.MustCompile(`^\s*([^:\s]+\.go:\d+):\s*(.*)$`)
	goPackageOK        = regexp.MustCompile(`^ok\s+\S+`)
	bunPassPattern     = regexp.MustCompile(`^\(pass\)\s+(.+?)(?:\s+\[.*)?$`)
	bunFailPattern     = regexp.MustCompile(`^\(fail\)\s+(.+?)(?:\s+\[.*)?$`)
	bunSkipPattern     = regexp.MustCompile(`^\(skip\)\s+(.+?)(?:\s+\[.*)?$`)
	nodePassPattern    = regexp.MustCompile(`^ok\s+\d+(?:\s+-\s+(.+))?$`)
	nodeFailPattern    = regexp.MustCompile(`^not ok\s+\d+(?:\s+-\s+(.+))?$`)
	pytestFailureLine  = regexp.MustCompile(`^FAILED\s+(\S+)(?:\s+-\s*(.*))?$`)
	cargoTestLine      = regexp.MustCompile(`^test\s+(.+)\s+\.\.\.\s+(ok|FAILED|ignored)$`)
	summaryCountPrefix = regexp.MustCompile(`(?i)(\d+)\s+(pass|passes|passed|fail|fails|failed|skip|skips|skipped|ignored)\b`)
)

// Detect discovers common test and verification commands in root.
func Detect(root string) ([]Check, error) {
	resolvedRoot, err := resolveRoot(root)
	if err != nil {
		return nil, err
	}
	checks := []Check{}
	if fileExists(filepath.Join(resolvedRoot, "go.mod")) {
		checks = append(checks, Check{
			ID:        "go.test",
			Name:      "Go tests",
			Command:   []string{"go", "test", "./..."},
			Kind:      KindTest,
			Framework: FrameworkGo,
		})
	}
	checks = append(checks, detectPackageChecks(resolvedRoot)...)
	if detectsPytest(resolvedRoot) {
		checks = append(checks, Check{
			ID:        "python.pytest",
			Name:      "Python pytest",
			Command:   []string{"python", "-m", "pytest"},
			Kind:      KindTest,
			Framework: FrameworkPytest,
		})
	}
	if fileExists(filepath.Join(resolvedRoot, "Cargo.toml")) {
		checks = append(checks, Check{
			ID:        "cargo.test",
			Name:      "Cargo tests",
			Command:   []string{"cargo", "test"},
			Kind:      KindTest,
			Framework: FrameworkCargo,
		})
	}
	return checks, nil
}

// ParseSummary extracts structured test counts and failures from runner output.
func ParseSummary(check Check, stdout string, stderr string) *Summary {
	framework := check.Framework
	if framework == "" {
		framework = inferFramework(check.Command)
	}
	combined := strings.TrimSpace(strings.Join([]string{stdout, stderr}, "\n"))
	if combined == "" {
		return nil
	}
	switch framework {
	case FrameworkGo:
		return parseGoSummary(combined)
	case FrameworkBun:
		return parseBunSummary(combined)
	case FrameworkPytest:
		return parsePytestSummary(combined)
	case FrameworkCargo:
		return parseCargoSummary(combined)
	default:
		return parseNodeSummary(combined)
	}
}

func detectPackageChecks(root string) []Check {
	pkg, ok := readPackageJSON(filepath.Join(root, "package.json"))
	if !ok {
		return nil
	}
	manager := detectPackageManager(root, pkg.PackageManager)
	checks := []Check{}
	for _, candidate := range []struct {
		script string
		kind   Kind
		label  string
	}{
		{script: "typecheck", kind: KindTypecheck, label: "typecheck"},
		{script: "test", kind: KindTest, label: "tests"},
		{script: "build", kind: KindBuild, label: "build"},
		{script: "lint", kind: KindLint, label: "lint"},
	} {
		if strings.TrimSpace(pkg.Scripts[candidate.script]) == "" {
			continue
		}
		checks = append(checks, Check{
			ID:        manager.IDPrefix + "." + candidate.script,
			Name:      titleWord(manager.Name) + " " + candidate.label,
			Command:   []string{manager.Name, "run", candidate.script},
			Kind:      candidate.kind,
			Framework: manager.Framework,
		})
	}
	return checks
}

func readPackageJSON(path string) (packageJSON, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return packageJSON{}, false
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return packageJSON{}, false
	}
	return pkg, true
}

func detectPackageManager(root string, declared string) packageManager {
	declaredName := strings.ToLower(strings.TrimSpace(strings.Split(declared, "@")[0]))
	switch {
	case fileExists(filepath.Join(root, "bun.lock")) || fileExists(filepath.Join(root, "bun.lockb")) || declaredName == "bun":
		return packageManager{Name: "bun", IDPrefix: "bun", Framework: FrameworkBun}
	case fileExists(filepath.Join(root, "pnpm-lock.yaml")) || declaredName == "pnpm":
		return packageManager{Name: "pnpm", IDPrefix: "pnpm", Framework: FrameworkNode}
	case fileExists(filepath.Join(root, "yarn.lock")) || declaredName == "yarn":
		return packageManager{Name: "yarn", IDPrefix: "yarn", Framework: FrameworkNode}
	case fileExists(filepath.Join(root, "package-lock.json")) || declaredName == "npm":
		return packageManager{Name: "npm", IDPrefix: "npm", Framework: FrameworkNode}
	default:
		return packageManager{Name: "npm", IDPrefix: "npm", Framework: FrameworkNode}
	}
}

func detectsPytest(root string) bool {
	for _, name := range []string{"pytest.ini", "tox.ini"} {
		if fileExists(filepath.Join(root, name)) {
			return true
		}
	}
	if fileContains(filepath.Join(root, "pyproject.toml"), "pytest") {
		return true
	}
	if fileContains(filepath.Join(root, "setup.cfg"), "pytest") {
		return true
	}
	return false
}

func parseGoSummary(output string) *Summary {
	summary := &Summary{Framework: FrameworkGo}
	lines := splitLines(output)
	failureByName := map[string]int{}
	packagePasses := 0
	sawVerbosePerTestOutput := false
	for index, line := range lines {
		switch {
		case goRunPattern.MatchString(line):
			sawVerbosePerTestOutput = true
			summary.Total++
		case goPassPattern.MatchString(line):
			sawVerbosePerTestOutput = true
			summary.Passed++
		case goSkipPattern.MatchString(line):
			sawVerbosePerTestOutput = true
			summary.Skipped++
		case goFailPattern.MatchString(line):
			summary.Failed++
			match := goFailPattern.FindStringSubmatch(line)
			name := safeSubmatch(match, 1)
			failure := Failure{Name: name}
			if index+1 < len(lines) {
				if location := goFailureLocation.FindStringSubmatch(lines[index+1]); location != nil {
					failure.File = safeSubmatch(location, 1)
					failure.Message = safeSubmatch(location, 2)
				}
			}
			failureByName[name] = appendFailure(&summary.Failures, failure)
		case goPackageOK.MatchString(line):
			packagePasses++
		}
	}
	if !sawVerbosePerTestOutput {
		summary.Passed += packagePasses
	}
	for index, line := range lines {
		location := goFailureLocation.FindStringSubmatch(line)
		if location == nil || index == 0 {
			continue
		}
		previous := goFailPattern.FindStringSubmatch(lines[index-1])
		if previous == nil {
			continue
		}
		if failureIndex, ok := failureByName[safeSubmatch(previous, 1)]; ok {
			summary.Failures[failureIndex].File = safeSubmatch(location, 1)
			summary.Failures[failureIndex].Message = safeSubmatch(location, 2)
		}
	}
	normalizeTotals(summary)
	return nilIfEmpty(summary)
}

func parseBunSummary(output string) *Summary {
	summary := &Summary{Framework: FrameworkBun}
	for _, line := range splitLines(output) {
		switch {
		case bunPassPattern.MatchString(line):
			summary.Passed++
		case bunSkipPattern.MatchString(line):
			summary.Skipped++
		case bunFailPattern.MatchString(line):
			summary.Failed++
			match := bunFailPattern.FindStringSubmatch(line)
			summary.Failures = append(summary.Failures, Failure{Name: safeSubmatch(match, 1)})
		default:
			mergeSummaryCounts(summary, line)
		}
	}
	normalizeTotals(summary)
	return nilIfEmpty(summary)
}

func parseNodeSummary(output string) *Summary {
	summary := &Summary{Framework: FrameworkNode}
	for _, line := range splitLines(output) {
		switch {
		case nodePassPattern.MatchString(line):
			summary.Passed++
		case nodeFailPattern.MatchString(line):
			summary.Failed++
			match := nodeFailPattern.FindStringSubmatch(line)
			name := safeSubmatch(match, 1)
			if name == "" {
				name = "tap failure"
			}
			summary.Failures = append(summary.Failures, Failure{Name: name})
		default:
			mergeSummaryCounts(summary, line)
		}
	}
	normalizeTotals(summary)
	return nilIfEmpty(summary)
}

func parsePytestSummary(output string) *Summary {
	summary := &Summary{Framework: FrameworkPytest}
	for _, line := range splitLines(output) {
		if match := pytestFailureLine.FindStringSubmatch(line); match != nil {
			summary.Failures = append(summary.Failures, Failure{Name: safeSubmatch(match, 1), Message: safeSubmatch(match, 2)})
		}
		mergeSummaryCounts(summary, line)
	}
	if summary.Failed == 0 && len(summary.Failures) > 0 {
		summary.Failed = len(summary.Failures)
	}
	normalizeTotals(summary)
	return nilIfEmpty(summary)
}

func parseCargoSummary(output string) *Summary {
	summary := &Summary{Framework: FrameworkCargo}
	for _, line := range splitLines(output) {
		if match := cargoTestLine.FindStringSubmatch(line); match != nil {
			name := safeSubmatch(match, 1)
			switch safeSubmatch(match, 2) {
			case "ok":
				summary.Passed++
			case "FAILED":
				summary.Failed++
				summary.Failures = append(summary.Failures, Failure{Name: name})
			case "ignored":
				summary.Skipped++
			}
			continue
		}
		mergeSummaryCounts(summary, line)
	}
	normalizeTotals(summary)
	return nilIfEmpty(summary)
}

func mergeSummaryCounts(summary *Summary, line string) {
	for _, match := range summaryCountPrefix.FindAllStringSubmatch(line, -1) {
		count, err := strconv.Atoi(safeSubmatch(match, 1))
		if err != nil {
			continue
		}
		switch strings.ToLower(safeSubmatch(match, 2)) {
		case "pass", "passes", "passed":
			summary.Passed = maxInt(summary.Passed, count)
		case "fail", "fails", "failed":
			summary.Failed = maxInt(summary.Failed, count)
		case "skip", "skips", "skipped", "ignored":
			summary.Skipped = maxInt(summary.Skipped, count)
		}
	}
}

func normalizeTotals(summary *Summary) {
	if summary.Total == 0 {
		summary.Total = summary.Passed + summary.Failed + summary.Skipped
	}
}

func nilIfEmpty(summary *Summary) *Summary {
	if summary.Total == 0 && summary.Passed == 0 && summary.Failed == 0 && summary.Skipped == 0 && len(summary.Failures) == 0 {
		return nil
	}
	return summary
}

func appendFailure(failures *[]Failure, failure Failure) int {
	*failures = append(*failures, failure)
	return len(*failures) - 1
}

func inferFramework(command []string) Framework {
	if len(command) == 0 {
		return FrameworkNode
	}
	switch command[0] {
	case "go":
		return FrameworkGo
	case "bun":
		return FrameworkBun
	case "python", "python3", "pytest":
		return FrameworkPytest
	case "cargo":
		return FrameworkCargo
	default:
		return FrameworkNode
	}
}

func splitLines(value string) []string {
	raw := strings.Split(value, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

func safeSubmatch(match []string, index int) string {
	if index >= len(match) {
		return ""
	}
	return strings.TrimSpace(match[index])
}

func titleWord(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func fileContains(path string, needle string) bool {
	data, err := os.ReadFile(path)
	return err == nil && strings.Contains(strings.ToLower(string(data)), strings.ToLower(needle))
}

func resolveRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolve test root: %w", err)
		}
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve test root: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("test root must be an existing directory: %s", absolute)
	}
	return filepath.Clean(absolute), nil
}

func maxInt(left int, right int) int {
	if left > right {
		return left
	}
	return right
}
