package tools

import (
	"fmt"
	"strconv"
	"strings"
)

const commandReducerMinPassingLines = 12

// reduceCommandOutput removes repetitive success-only lines from recognized
// simple commands. Unsupported or compound shell input passes through exactly,
// and every changed result retains the pre-reduction output as an artifact.
func reduceCommandOutput(toolName string, args map[string]any, result Result) Result {
	if toolName != ExecCommandToolName && toolName != "bash" {
		return result
	}
	command, err := commandTextForReducer(toolName, args)
	if err != nil || !isSimpleGoTestCommand(command) {
		return result
	}

	reduced, omitted := reduceGoTestPassingPackages(result.Output)
	if omitted < commandReducerMinPassingLines || len(reduced) >= len(result.Output) {
		return result
	}
	originalBytes := len(result.Output)
	originalTokens := estimateOutputTokens(result.Output)
	spillPath := spillTruncatedOutput(toolName, result.Output)
	if spillPath == "" {
		return result
	}

	notice := fmt.Sprintf("[zero] compacted %d passing package lines; exact output saved to %s", omitted, spillPath)
	result.Output = strings.TrimRight(reduced, "\n") + "\n" + notice
	result.Truncated = true
	if result.Meta == nil {
		result.Meta = map[string]string{}
	}
	result.Meta["command_output_reduced"] = "true"
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

func isSimpleGoTestCommand(command string) bool {
	command = strings.TrimSpace(command)
	if command == "" || strings.ContainsAny(command, "|;&<>\n\r`") || strings.Contains(command, "$(") {
		return false
	}
	fields := strings.Fields(command)
	return len(fields) >= 2 && fields[0] == "go" && fields[1] == "test"
}

func reduceGoTestPassingPackages(output string) (string, int) {
	lines := strings.Split(output, "\n")
	kept := make([]string, 0, len(lines))
	omitted := 0
	for _, line := range lines {
		packageSuccess := strings.HasPrefix(line, "ok  \t") || strings.HasPrefix(line, "?   \t") ||
			strings.HasPrefix(line, "ok  ") || strings.HasPrefix(line, "?   ")
		if packageSuccess && !strings.Contains(line, "coverage:") && !strings.Contains(line, "[no test files]") {
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
	summary := fmt.Sprintf("[zero] %d passing package lines omitted", omitted)
	kept = append(kept, "")
	copy(kept[insertAt+1:], kept[insertAt:])
	kept[insertAt] = summary
	return strings.Join(kept, "\n"), omitted
}
