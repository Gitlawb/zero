package tools

import (
	"fmt"
	"strconv"
	"strings"
)

const commandReducerMinPassingLines = 12

type commandOutputReducer struct {
	kind   string
	label  string
	reduce func(string) (string, int)
}

// reduceCommandOutput removes repetitive success-only lines from recognized
// simple commands. Unsupported or compound shell input passes through exactly,
// and every changed result retains the pre-reduction output as an artifact.
func reduceCommandOutput(toolName string, args map[string]any, result Result) Result {
	if toolName != ExecCommandToolName && toolName != "bash" {
		return result
	}
	command, err := commandTextForReducer(toolName, args)
	if err != nil {
		return result
	}
	reducer, ok := commandOutputReducerFor(command)
	if !ok {
		return result
	}

	reduced, omitted := reducer.reduce(result.Output)
	if omitted < commandReducerMinPassingLines || len(reduced) >= len(result.Output) {
		return result
	}
	originalBytes := len(result.Output)
	originalTokens := estimateOutputTokens(result.Output)
	spillPath := spillTruncatedOutput(toolName, result.Output)
	if spillPath == "" {
		return result
	}

	notice := fmt.Sprintf("[zero] compacted %d %s; exact output saved to %s", omitted, reducer.label, spillPath)
	result.Output = strings.TrimRight(reduced, "\n") + "\n" + notice
	result.Truncated = true
	if result.Meta == nil {
		result.Meta = map[string]string{}
	}
	result.Meta["command_output_reduced"] = "true"
	result.Meta["command_output_reducer"] = reducer.kind
	result.Meta["command_output_omitted_lines"] = strconv.Itoa(omitted)
	result.Meta["command_output_original_bytes"] = strconv.Itoa(originalBytes)
	result.Meta["command_output_original_tokens"] = strconv.Itoa(originalTokens)
	result.Meta["command_output_retained_tokens"] = strconv.Itoa(estimateOutputTokens(result.Output))
	result.Meta["spill_path"] = spillPath
	result.Meta["truncation_reason"] = "command_aware_reduction"
	return result
}

func commandTextForReducer(toolName string, args map[string]any) (string, error) {
	if toolName == "bash" {
		return bashCommandArg(args)
	}
	return execCommandArg(args)
}

func simpleCommandFields(command string) ([]string, bool) {
	command = strings.TrimSpace(command)
	if separator := strings.LastIndexAny(command, " \t"); separator >= 0 && command[separator+1:] == "2>&1" {
		command = strings.TrimSpace(command[:separator])
	}
	if command == "" || strings.ContainsAny(command, "|;&<>\n\r`") || strings.Contains(command, "$(") {
		return nil, false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return nil, false
	}
	return fields, true
}

func commandOutputReducerFor(command string) (commandOutputReducer, bool) {
	fields, ok := simpleCommandFields(command)
	if !ok {
		return commandOutputReducer{}, false
	}
	if len(fields) >= 2 && fields[0] == "go" && fields[1] == "test" {
		return commandOutputReducer{kind: "go_test", label: "passing package lines", reduce: reduceGoTestPassingPackages}, true
	}
	if len(fields) >= 2 && fields[0] == "cargo" {
		switch fields[1] {
		case "test":
			return commandOutputReducer{kind: "cargo_test", label: "passing Rust test lines", reduce: reduceCargoTestPassingLines}, true
		case "build", "check":
			return commandOutputReducer{kind: "cargo_build", label: "successful Rust build progress lines", reduce: reduceCargoBuildProgressLines}, true
		}
	}
	if isPytestCommand(fields) {
		return commandOutputReducer{kind: "pytest", label: "passing pytest progress lines", reduce: reducePytestPassingLines}, true
	}
	if isJavaScriptTestCommand(fields) {
		return commandOutputReducer{kind: "javascript_test", label: "passing JavaScript test-file lines", reduce: reduceJavaScriptTestPassingLines}, true
	}
	return commandOutputReducer{}, false
}

func isPytestCommand(fields []string) bool {
	if fields[0] == "pytest" || fields[0] == "py.test" {
		return true
	}
	return len(fields) >= 3 && (fields[0] == "python" || fields[0] == "python3") && fields[1] == "-m" && fields[2] == "pytest"
}

func isJavaScriptTestCommand(fields []string) bool {
	switch fields[0] {
	case "jest", "vitest":
		return true
	case "npx":
		return len(fields) >= 2 && (fields[1] == "jest" || fields[1] == "vitest")
	case "npm", "pnpm", "yarn":
		if len(fields) >= 2 && fields[1] == "test" {
			return true
		}
		if len(fields) >= 3 && fields[1] == "run" && fields[2] == "test" {
			return true
		}
		return len(fields) >= 3 && fields[1] == "exec" && (fields[2] == "jest" || fields[2] == "vitest")
	default:
		return false
	}
}

func reduceGoTestPassingPackages(output string) (string, int) {
	return reduceRepeatedOutputLines(output, "passing package lines", func(line string) bool {
		packageSuccess := strings.HasPrefix(line, "ok  \t") || strings.HasPrefix(line, "?   \t") ||
			strings.HasPrefix(line, "ok  ") || strings.HasPrefix(line, "?   ")
		return packageSuccess && !strings.Contains(line, "coverage:") && !strings.Contains(line, "[no test files]")
	})
}

func reduceCargoTestPassingLines(output string) (string, int) {
	if !strings.Contains(output, "test result:") {
		return output, 0
	}
	return reduceRepeatedOutputLines(output, "passing Rust test lines", func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return strings.HasPrefix(trimmed, "test ") && strings.HasSuffix(trimmed, " ... ok")
	})
}

func reduceCargoBuildProgressLines(output string) (string, int) {
	if !outputHasCargoFinishedSummary(output) {
		return output, 0
	}
	return reduceRepeatedOutputLines(output, "successful Rust build progress lines", func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return strings.HasPrefix(trimmed, "Checking ") || strings.HasPrefix(trimmed, "Compiling ")
	})
}

func reducePytestPassingLines(output string) (string, int) {
	if !outputHasPytestSummary(output) {
		return output, 0
	}
	return reduceRepeatedOutputLines(output, "passing pytest progress lines", isPassingPytestProgressLine)
}

func isPassingPytestProgressLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	open := strings.LastIndexByte(trimmed, '[')
	if open < 0 || !strings.HasSuffix(trimmed, "%]") {
		return false
	}
	progress := strings.TrimSpace(trimmed[:open])
	fields := strings.Fields(progress)
	if len(fields) == 0 {
		return false
	}
	marks := fields[len(fields)-1]
	return marks != "" && strings.Trim(marks, ".") == ""
}

func reduceJavaScriptTestPassingLines(output string) (string, int) {
	if !outputHasJavaScriptTestSummary(output) {
		return output, 0
	}
	return reduceRepeatedOutputLines(output, "passing JavaScript test-file lines", func(line string) bool {
		trimmed := strings.TrimSpace(line)
		return strings.HasPrefix(line, "PASS ") || strings.HasPrefix(trimmed, "✓ ") || strings.HasPrefix(trimmed, "✔ ")
	})
}

func outputHasCargoFinishedSummary(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Finished `") && strings.Contains(trimmed, "` profile ") &&
			strings.Contains(trimmed, " target(s) in ") {
			return true
		}
	}
	return false
}

func outputHasPytestSummary(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		summary := strings.Trim(strings.TrimSpace(line), "= ")
		if summary == "short test summary info" || isPytestResultSummary(summary) {
			return true
		}
	}
	return false
}

func isPytestResultSummary(summary string) bool {
	fields := strings.Fields(summary)
	if len(fields) < 4 {
		return false
	}
	first := strings.Trim(fields[0], ",;:()")
	if _, err := strconv.Atoi(first); err != nil || !summaryHasCountedStatus(summary, "passed") {
		return false
	}
	for index := 1; index+1 < len(fields); index++ {
		if fields[index] != "in" {
			continue
		}
		duration := strings.TrimSuffix(strings.Trim(fields[index+1], ",;:()"), "s")
		if _, err := strconv.ParseFloat(duration, 64); err == nil {
			return true
		}
	}
	return false
}

func outputHasJavaScriptTestSummary(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Test Suites:") && summaryHasCountedStatus(trimmed, "passed") {
			return true
		}
		if strings.HasPrefix(trimmed, "Test Files ") && summaryHasCountedStatus(trimmed, "passed") {
			return true
		}
	}
	return false
}

func summaryHasCountedStatus(summary string, status string) bool {
	fields := strings.Fields(summary)
	for index := 1; index < len(fields); index++ {
		if strings.Trim(fields[index], ",;:()") != status {
			continue
		}
		count := strings.Trim(fields[index-1], ",;:()")
		if _, err := strconv.Atoi(count); err == nil {
			return true
		}
	}
	return false
}

func reduceRepeatedOutputLines(output string, label string, omit func(string) bool) (string, int) {
	lines := strings.Split(output, "\n")
	kept := make([]string, 0, len(lines))
	omitted := 0
	for _, line := range lines {
		if omit(line) {
			omitted++
			continue
		}
		kept = append(kept, line)
	}
	if omitted == 0 {
		return output, 0
	}

	insertAt := len(kept)
	for index, line := range kept {
		if strings.HasPrefix(line, "exit_code:") || strings.HasPrefix(line, "session_id:") {
			insertAt = index
			break
		}
	}
	summary := fmt.Sprintf("[zero] %d %s omitted", omitted, label)
	kept = append(kept, "")
	copy(kept[insertAt+1:], kept[insertAt:])
	kept[insertAt] = summary
	return strings.Join(kept, "\n"), omitted
}
