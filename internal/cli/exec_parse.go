package cli

import (
	"fmt"
	"strconv"
	"strings"
)

func parseExecArgs(args []string) (execOptions, bool, error) {
	options := execOptions{inputFormat: execInputText, outputFormat: execOutputText, autonomy: "low"}
	if len(args) == 0 {
		return options, false, execUsageError{"Prompt required. Use `zero exec \"prompt\"` or `zero exec --file prompt.txt`."}
	}

	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "-h" || arg == "--help" || arg == "help":
			return options, true, nil
		case arg == "--skip-permissions-unsafe":
			options.skipPermissionsUnsafe = true
		case arg == "--list-tools":
			options.listTools = true
		case arg == "--auto":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.autonomy = value
			index = next
		case strings.HasPrefix(arg, "--auto="):
			options.autonomy = strings.TrimSpace(strings.TrimPrefix(arg, "--auto="))
		case arg == "--enabled-tools":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.enabledTools = parseToolList(value)
			index = next
		case strings.HasPrefix(arg, "--enabled-tools="):
			options.enabledTools = parseToolList(strings.TrimSpace(strings.TrimPrefix(arg, "--enabled-tools=")))
		case arg == "--disabled-tools":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.disabledTools = parseToolList(value)
			index = next
		case strings.HasPrefix(arg, "--disabled-tools="):
			options.disabledTools = parseToolList(strings.TrimSpace(strings.TrimPrefix(arg, "--disabled-tools=")))
		case arg == "-f" || arg == "--file":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.file = value
			index = next
		case strings.HasPrefix(arg, "--file="):
			options.file = strings.TrimSpace(strings.TrimPrefix(arg, "--file="))
		case arg == "-m" || arg == "--model":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.model = value
			index = next
		case strings.HasPrefix(arg, "--model="):
			options.model = strings.TrimSpace(strings.TrimPrefix(arg, "--model="))
		case arg == "--max-turns":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			maxTurns, err := parseExecMaxTurns(value)
			if err != nil {
				return options, false, err
			}
			options.maxTurns = maxTurns
			index = next
		case strings.HasPrefix(arg, "--max-turns="):
			maxTurns, err := parseExecMaxTurns(strings.TrimSpace(strings.TrimPrefix(arg, "--max-turns=")))
			if err != nil {
				return options, false, err
			}
			options.maxTurns = maxTurns
		case arg == "-C" || arg == "--cwd":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.cwd = value
			index = next
		case strings.HasPrefix(arg, "--cwd="):
			options.cwd = strings.TrimSpace(strings.TrimPrefix(arg, "--cwd="))
		case arg == "-i" || arg == "--input-format":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			format, err := parseExecInputFormat(value)
			if err != nil {
				return options, false, err
			}
			options.inputFormat = format
			index = next
		case strings.HasPrefix(arg, "--input-format="):
			format, err := parseExecInputFormat(strings.TrimSpace(strings.TrimPrefix(arg, "--input-format=")))
			if err != nil {
				return options, false, err
			}
			options.inputFormat = format
		case arg == "-o" || arg == "--output-format":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			format, err := parseExecOutputFormat(value)
			if err != nil {
				return options, false, err
			}
			options.outputFormat = format
			index = next
		case strings.HasPrefix(arg, "--output-format="):
			format, err := parseExecOutputFormat(strings.TrimSpace(strings.TrimPrefix(arg, "--output-format=")))
			if err != nil {
				return options, false, err
			}
			options.outputFormat = format
		case arg == "--prompt":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.promptParts = append(options.promptParts, value)
			index = next
		case strings.HasPrefix(arg, "--prompt="):
			options.promptParts = append(options.promptParts, strings.TrimSpace(strings.TrimPrefix(arg, "--prompt=")))
		case arg == "--resume":
			if index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") && strings.TrimSpace(args[index+1]) != "" {
				options.resume = strings.TrimSpace(args[index+1])
				index++
			} else {
				options.resumeLatest = true
			}
		case strings.HasPrefix(arg, "--resume="):
			value := strings.TrimSpace(strings.TrimPrefix(arg, "--resume="))
			if value == "" {
				options.resumeLatest = true
			} else {
				options.resume = value
			}
		case arg == "--fork":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.fork = value
			index = next
		case strings.HasPrefix(arg, "--fork="):
			options.fork = strings.TrimSpace(strings.TrimPrefix(arg, "--fork="))
		case arg == "-w" || arg == "--worktree":
			options.worktree = true
			if index+1 < len(args) && !flagValueLooksLikeOption(strings.TrimSpace(args[index+1])) && strings.TrimSpace(args[index+1]) != "" {
				options.worktreeName = strings.TrimSpace(args[index+1])
				index++
			}
		case strings.HasPrefix(arg, "--worktree="):
			options.worktree = true
			options.worktreeName = strings.TrimSpace(strings.TrimPrefix(arg, "--worktree="))
		case arg == "--worktree-dir":
			value, next, err := nextFlagValue(args, index, arg)
			if err != nil {
				return options, false, err
			}
			options.worktreeDir = value
			index = next
		case strings.HasPrefix(arg, "--worktree-dir="):
			options.worktreeDir = strings.TrimSpace(strings.TrimPrefix(arg, "--worktree-dir="))
		case arg == "--":
			options.promptParts = append(options.promptParts, args[index+1:]...)
			index = len(args)
		case strings.HasPrefix(arg, "-"):
			return options, false, execUsageError{fmt.Sprintf("unknown exec flag %q", arg)}
		default:
			options.promptParts = append(options.promptParts, arg)
		}
	}

	if (options.resume != "" || options.resumeLatest) && options.fork != "" {
		return options, false, execUsageError{"Use either --resume or --fork, not both."}
	}
	if options.worktree && options.fork != "" {
		return options, false, execUsageError{"--fork cannot be used with --worktree. Forked sessions must continue in the source session workspace."}
	}
	if options.worktreeDir != "" && !options.worktree {
		return options, false, execUsageError{"--worktree-dir requires --worktree."}
	}
	if options.inputFormat == execInputStreamJSON && strings.TrimSpace(strings.Join(options.promptParts, " ")) != "" {
		return options, false, execUsageError{"Stream-json input does not accept positional prompt text. Pipe JSONL or use --file."}
	}
	if !options.listTools && options.file == "" && options.inputFormat != execInputStreamJSON && strings.TrimSpace(strings.Join(options.promptParts, " ")) == "" {
		return options, false, execUsageError{"Prompt required. Use `zero exec \"prompt\"` or `zero exec --file prompt.txt`."}
	}
	return options, false, nil
}

func parseExecMaxTurns(value string) (int, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, execUsageError{"--max-turns requires a value"}
	}
	maxTurns, err := strconv.Atoi(trimmed)
	if err != nil || maxTurns <= 0 {
		return 0, execUsageError{fmt.Sprintf("invalid --max-turns %q. Expected a positive integer.", value)}
	}
	return maxTurns, nil
}

func nextFlagValue(args []string, index int, flag string) (string, int, error) {
	if index+1 >= len(args) {
		return "", index, execUsageError{fmt.Sprintf("%s requires a value", flag)}
	}
	next := strings.TrimSpace(args[index+1])
	if next == "" || flagValueLooksLikeOption(next) {
		return "", index, execUsageError{fmt.Sprintf("%s requires a value", flag)}
	}
	return next, index + 1, nil
}

func flagValueLooksLikeOption(value string) bool {
	if !strings.HasPrefix(value, "-") {
		return false
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return false
	}
	return true
}

func parseExecOutputFormat(value string) (execOutputFormat, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(execOutputText):
		return execOutputText, nil
	case string(execOutputJSON):
		return execOutputJSON, nil
	case string(execOutputStreamJSON):
		return execOutputStreamJSON, nil
	default:
		return "", execUsageError{fmt.Sprintf("invalid output format %q. Expected text, json, or stream-json.", value)}
	}
}

func parseExecInputFormat(value string) (execInputFormat, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(execInputText):
		return execInputText, nil
	case string(execInputStreamJSON):
		return execInputStreamJSON, nil
	default:
		return "", execUsageError{fmt.Sprintf("Invalid input format %q. Expected text or stream-json.", value)}
	}
}
