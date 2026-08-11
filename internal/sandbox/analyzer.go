package sandbox

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// AnalysisResult is a static, AST-based assessment of a shell script. It is a
// more precise second opinion than the regex detector in safe_command.go:
// because it walks the parsed command tree, a program name is only counted when
// it is an actual command, never when it appears inside a quoted argument (so
// `echo "git rebase -i"` and `node -e "require('repl').start()"` are clean).
type AnalysisResult struct {
	Interactive bool
	Destructive bool
	Network     bool
	// TooComplex is set when the script cannot be parsed (obfuscated or invalid),
	// so a caller can treat it as higher-risk instead of trusting a clean result.
	TooComplex bool
	// Programs lists the distinct top-level command names found, for diagnostics.
	Programs []string
}

// destructivePrograms are commands that can irrecoverably destroy data.
var destructivePrograms = map[string]bool{
	"mkfs": true, "fdisk": true, "shred": true, "dd": true, "parted": true,
}

var powerShellRemoveItemPrograms = map[string]bool{
	"remove-item": true,
	"ri":          true,
	"rd":          true,
	"rmdir":       true,
	"del":         true,
	"erase":       true,
	"rm":          true,
}

// networkPrograms are commands that perform network egress/ingress.
var networkPrograms = map[string]bool{
	"curl": true, "wget": true, "fetch": true, "aria2c": true,
	"ssh": true, "scp": true, "sftp": true,
	"rsync": true, "nc": true, "ncat": true, "netcat": true, "telnet": true,
	"ftp": true, "iwr": true, "irm": true, "invoke-webrequest": true,
	"invoke-restmethod": true,
}

var localServerPrograms = map[string]bool{
	"http-server": true,
	"serve":       true,
	"vite":        true,
	"next":        true,
	"nuxt":        true,
	"astro":       true,
}

// AnalyzeCommand parses script and reports interactive/destructive/network usage
// from the shell AST. A script that cannot be parsed yields TooComplex (with no
// other flags set) so the caller can decide how to treat an unanalyzable command.
// maxAnalyzerDepth bounds recursion into `sh -c <payload>` launchers so a
// pathologically nested script cannot cause unbounded work.
const maxAnalyzerDepth = 4

// shellPrograms run their `-c` argument as a fresh command, so the analyzer
// recurses into that payload instead of classifying on the shell name.
var shellPrograms = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "ksh": true, "dash": true,
}

func AnalyzeCommand(script string) AnalysisResult {
	result := AnalysisResult{}
	if strings.TrimSpace(script) == "" {
		return result
	}
	analyzeInto(script, &result, map[string]bool{}, 0)
	return result
}

// astCommandFields parses command with the shell parser and returns each simple
// command as its literal field slice (program + args as text), resolving the
// real command positions across quoting, command substitution, subshells, and
// newline separators — the constructs the hand-written splitter in
// safe_command.go mis-handles (issue #473). It returns nil when the command
// cannot be parsed (e.g. a Windows cmd.exe string), so callers fall through to
// the regex path rather than hard-blocking.
func astCommandFields(command string) [][]string {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	var commands [][]string
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		// EVERY word must be a static literal, not just the program name.
		// wordText keeps only the literal/quoted parts of a word and silently
		// drops expansions, so a dynamic word anywhere in the call reconstructs
		// to something the shell will never run: `$(printf foo)vim` (runs as
		// `foovim`) collapses to "vim", and `git $(printf foo)rebase -i` (runs
		// as `git foorebase -i`, non-interactive) collapses to `git rebase -i`
		// and fabricates an interactive match. Since the runtime value of an
		// expansion is unknowable here, skip the whole call rather than classify
		// a lossy reconstruction. Skipping is the safe direction: the
		// hand-written passes above already ran, and a missed detection falls
		// through to the normal permission prompt instead of hard-blocking a
		// command the user never wrote.
		for _, word := range call.Args {
			if !isLiteralWord(word) {
				return true
			}
		}
		fields := make([]string, 0, len(call.Args))
		for _, word := range call.Args {
			fields = append(fields, wordText(word))
		}
		commands = append(commands, fields)
		return true
	})
	return commands
}

// isLiteralWord reports whether every part of word is a static literal (bare or
// quoted). A word containing a command substitution, parameter/arithmetic
// expansion, process substitution, etc. is dynamic — its runtime value is
// unknown, so its wordText (a partial literal) must not be trusted as a program
// name.
func isLiteralWord(word *syntax.Word) bool {
	if word == nil {
		return false
	}
	for _, part := range word.Parts {
		switch typed := part.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
		case *syntax.DblQuoted:
			for _, inner := range typed.Parts {
				if _, ok := inner.(*syntax.Lit); !ok {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

// analyzeInto parses script and folds its interactive/destructive/network usage
// into result, sharing seen so program names are de-duplicated across recursion.
func analyzeInto(script string, result *AnalysisResult, seen map[string]bool, depth int) {
	file, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		result.TooComplex = true
		return
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		// Resolve the real program behind wrapper prefixes (sudo, env, nice, ...)
		// so `sudo rm -rf`, `env curl …`, and `bash -c 'vim x'` are classified on
		// the payload, not the launcher — matching DetectInteractiveCommand.
		// GNU env -S/--split-string can consume the complete payload, leaving no
		// ordinary effective program, so resolve its argv before that scan.
		if fields, ok := literalCallFields(call.Args); ok {
			if split := envSplitCommandFields(fields); split.recognized {
				if split.executableEnvironmentDependent || fallbackBodyUsesNetwork(split.command, depth+1) {
					result.Network = true
				}
				return true
			}
		} else if envSplitSourceDynamic(call.Args) {
			// The split string comes from an expansion, so its argv — including the
			// executable — is unknowable here. effectiveProgram would consume -S with
			// its operand and report no executable at all, which reads an
			// uninspectable command as a clean one.
			result.Network = true
			return true
		}
		prog, rest := effectiveProgram(call.Args)
		if prog == "" {
			return true
		}
		if !seen[prog] {
			seen[prog] = true
			result.Programs = append(result.Programs, prog)
		}
		// `sh -c <payload>` runs the payload as a fresh command; recurse into it so
		// a program hidden behind a shell launcher is still classified.
		if shellPrograms[prog] {
			if payloadIndex, found := shellCommandPayloadIndex(prog, wordTexts(rest)); found && payloadIndex < len(rest) {
				if !isLiteralWord(rest[payloadIndex]) {
					result.Network = true
				} else if payload := wordText(rest[payloadIndex]); payload != "" && depth < maxAnalyzerDepth {
					analyzeInto(payload, result, seen, depth+1)
				} else if payload != "" {
					result.Network = true
				}
			}
		}
		// PowerShell's Command flag also carries textual source. Analyze it in an
		// isolated result because PowerShell syntax that the POSIX parser rejects
		// must not make the outer command TooComplex; only fold the network fact.
		if prog == "powershell" || prog == "pwsh" {
			source := fallbackPowerShellPayload(prog, wordTexts(rest))
			switch {
			case source.opaque, powerShellSourceDynamic(source, rest):
				// Source the scan cannot read — an undecodable encoded payload, or a
				// Command operand built from an expansion — must not be reported as a
				// command that makes no network call.
				result.Network = true
			case source.payload != "":
				if depth >= maxAnalyzerDepth || textualPayloadUsesNetwork(source.payload, depth+1) {
					result.Network = true
				}
			}
		}
		if _, interactive := interactivePrograms[prog]; interactive && !replSuppressed(prog, rest) {
			result.Interactive = true
		}
		// BusyBox and strace delegate to a child executable named in their own
		// argv. Both resolvers below (busyboxCommandArgs, straceCommandArgs)
		// operate on wordTexts, which silently drops any expansion — an
		// unresolvable child-program token would otherwise read as a clean,
		// unrecognized token rather than as "unknown, so assume the worst."
		switch {
		case prog == "busybox" && busyboxSourceDynamic(rest):
			result.Network = true
		case prog == "strace" && straceSourceDynamic(rest):
			result.Network = true
		case commandUsesNetwork(prog, rest):
			result.Network = true
		}
		if destructivePrograms[prog] ||
			(prog == "rm" && hasRecursiveForce(rest)) ||
			(powerShellRemoveItemPrograms[prog] && hasPowerShellRecursiveForce(rest)) ||
			(prog == "find" && hasFindDelete(rest)) {
			result.Destructive = true
		}
		return true
	})
}

func commandUsesNetwork(prog string, args []*syntax.Word) bool {
	return commandWordsUseNetwork(prog, wordTexts(args))
}

func commandWordsUseNetwork(prog string, words []string) bool {
	return commandWordsUseNetworkAt(prog, words, 0)
}

func commandWordsUseNetworkAt(prog string, words []string, depth int) bool {
	prog = normalizeProgramToken(prog)
	originalWords := words
	normalized := make([]string, len(words))
	for index := range words {
		normalized[index] = strings.ToLower(strings.TrimSpace(words[index]))
	}
	words = normalized
	if networkPrograms[prog] || localServerPrograms[prog] {
		return true
	}
	switch prog {
	case "python", "python2", "python3", "py":
		return pythonModuleUsesNetwork(words)
	case "npm":
		return packageManagerUsesNetwork(words, map[string]string{
			"run":  "run",
			"exec": "exec",
			"x":    "exec",
		})
	case "pnpm":
		return packageManagerUsesNetwork(words, map[string]string{
			"run":  "run",
			"exec": "exec",
			"dlx":  "exec",
		})
	case "yarn":
		return packageManagerUsesNetwork(words, map[string]string{
			"run":  "run",
			"exec": "exec",
			"dlx":  "exec",
		})
	case "bun":
		return packageManagerUsesNetwork(words, map[string]string{
			"run": "run",
			"x":   "exec",
		})
	case "npx":
		return npxUsesNetwork(words)
	case "pip", "pip2", "pip3":
		return firstSubcommand(words, nil) == "install"
	case "go":
		return firstSubcommand(words, nil) == "get"
	case "git":
		return gitUsesNetwork(words)
	case "gh":
		return ghUsesNetwork(words)
	case "busybox":
		if command := busyboxCommandArgs(originalWords); len(command) > 0 {
			return fallbackBodyUsesNetwork(command, depth+1)
		}
	case "strace":
		if command := straceCommandArgs(originalWords); len(command) > 0 {
			return fallbackBodyUsesNetwork(command, depth+1)
		}
	default:
		return false
	}
	return false
}

func packageManagerUsesNetwork(words []string, aliases map[string]string) bool {
	if packageManagerOffline(words) {
		return false
	}
	first := firstSubcommand(words, aliases)
	switch first {
	case "install", "add", "ci", "create", "dlx", "publish", "unpublish",
		"login", "logout", "adduser", "whoami", "ping", "audit", "outdated",
		"update", "upgrade", "search", "view", "info", "show", "dist-tag",
		"deprecate", "owner", "org", "team", "token", "profile", "access":
		return true
	case "start", "serve", "dev", "preview":
		return true
	case "run":
		second := secondSubcommand(words)
		return second == "start" || second == "serve" || second == "dev" || second == "preview"
	case "exec":
		// Package-manager exec commands may resolve and download a missing
		// package before launching it. An explicit offline flag keeps this path
		// inside the isolated namespace; otherwise request egress up front.
		return true
	}
	return false
}

func packageManagerOffline(words []string) bool {
	for _, word := range words {
		switch word {
		case "--offline", "--no-network":
			return true
		}
	}
	return false
}

func gitUsesNetwork(words []string) bool {
	invocation := parseGitInvocation(words)
	if invocation.kind != gitCommandSubcommand {
		// No subcommand at all, or a global option that makes git print locally
		// and exit before any subcommand runs.
		return false
	}
	switch invocation.subcommand {
	case "clone", "fetch", "pull", "push", "ls-remote":
		return true
	case "archive":
		// `git archive HEAD` streams a tree out of the local object store and needs
		// no egress at all; only `--remote=<repo>` sends the request to another
		// host. Classifying every archive as network cost a proactive network
		// prompt on a purely local command.
		return gitTargetsRemoteArchive(words, invocation.subcommandIndex)
	default:
		return false
	}
}

// gitTargetsRemoteArchive reports whether an archive subcommand has an active
// --remote option. Git accepts archive options after positional operands, so
// this must keep scanning until an unconsumed `--`. Value-taking options are
// consumed first: in `archive -o -- --remote=origin HEAD`, `-o` owns the first
// `--` and the later remote is active; in `archive -o --remote HEAD`, --remote
// is only the output filename.
func gitTargetsRemoteArchive(words []string, subcommandIndex int) bool {
	for index := subcommandIndex + 1; index < len(words); index++ {
		word := strings.ToLower(words[index])
		switch {
		case word == "--":
			return false
		case strings.HasPrefix(word, "--remote="):
			return true
		case word == "--remote":
			return index+1 < len(words)
		case gitArchivePreliminaryOptionConsumesValue(word):
			index++
		}
	}
	return false
}

// gitArchivePreliminaryOptionConsumesValue models the first parse-options pass
// in git archive. That pass consumes only transport/output options and retains
// format/prefix/mtime options for a later parser, so those later options must
// not hide an immediately following --remote.
func gitArchivePreliminaryOptionConsumesValue(option string) bool {
	if strings.Contains(option, "=") {
		return false
	}
	switch option {
	case "-o", "--output", "--exec":
		return true
	default:
		return false
	}
}

// gitCommandKind distinguishes the three outcomes of reading a git command
// line, which callers must treat differently.
type gitCommandKind int

const (
	// gitCommandNone: no subcommand was found (`git`, `git -C repo`).
	gitCommandNone gitCommandKind = iota
	// gitCommandTerminalGlobal: a global option that makes git print locally
	// and exit — no subcommand runs, whatever words follow it.
	gitCommandTerminalGlobal
	// gitCommandSubcommand: a subcommand git will actually execute.
	gitCommandSubcommand
)

type gitInvocation struct {
	kind gitCommandKind
	// subcommand is set only for gitCommandSubcommand.
	subcommand string
	// subcommandIndex is the position in the original argument slice.
	subcommandIndex int
	// terminalOption is set only for gitCommandTerminalGlobal.
	terminalOption string
}

// gitTerminalGlobalOptions are git's global options that print something from
// the local installation and exit. Everything after one of them is help/version
// output text, not a command: `git -C repo --help push` prints git-push's
// manual page without contacting a remote, so a subcommand scan that walked
// past them would classify a purely local command as network.
// Bare `--exec-path` belongs here for the same reason: git documents it as
// `--exec-path[=<path>]`, so without an inline value it prints the compiled-in
// exec path and exits. `git --exec-path /tmp push` neither reads /tmp as the
// option's value nor runs push, so treating it as a value-taking option made a
// local informational command request egress.
var gitTerminalGlobalOptions = map[string]bool{
	"-h": true, "--help": true,
	"-v": true, "--version": true,
	"--html-path": true, "--man-path": true, "--info-path": true,
	"--list-cmds": true, "--exec-path": true,
}

// parseGitInvocation resolves what a git command line actually does, past git's
// GLOBAL options. It is the single reader both classification paths use — the
// AST path through gitUsesNetwork and the unparseable fallback through
// matchesUnparseableGitNetwork — so the two cannot disagree about an option, as
// they did while each carried its own skip list.
//
// The generic firstSubcommand cannot do this job: git's value-taking globals put
// their value in the next token, so scanning for the first non-dash token returns
// that value instead — `git -C repo push origin main` looked like the subcommand
// "repo" and so classified as no-network, dropping the proactive network prompt
// for the most common form of the command. internal/agent/command_prefix.go
// resolves the same option set for its own prefix matching.
func parseGitInvocation(words []string) gitInvocation {
	for index := 0; index < len(words); index++ {
		word := strings.ToLower(words[index])
		if word == "" {
			continue
		}
		if gitTerminalGlobalOptions[word] || strings.HasPrefix(word, "--list-cmds=") {
			return gitInvocation{kind: gitCommandTerminalGlobal, terminalOption: word}
		}
		if strings.HasPrefix(word, "-") {
			// A joined value (--git-dir=/x, -C/x) is one token and needs no skip;
			// a separated one puts its value in the next token.
			if GitGlobalOptionConsumesValue(word) {
				index++
			}
			continue
		}
		if isNumericToken(word) {
			continue
		}
		return gitInvocation{kind: gitCommandSubcommand, subcommand: word, subcommandIndex: index}
	}
	return gitInvocation{kind: gitCommandNone}
}

// GitGlobalOptionConsumesValue lists git's global options whose value is a
// separate token. It is shared with internal/agent's command-prefix parser so
// the two security-sensitive scans cannot drift.
//
// `--exec-path` is deliberately absent: its value is inline-only
// (`--exec-path=<path>`), and the bare spelling is terminal — see
// gitTerminalGlobalOptions.
func GitGlobalOptionConsumesValue(option string) bool {
	switch strings.ToLower(option) {
	case "-c", "--attr-source", "--config-env", "--git-dir", "--namespace", "--super-prefix", "--work-tree":
		return true
	default:
		return false
	}
}

func npxUsesNetwork(_ []string) bool {
	return true
}

func wordTexts(args []*syntax.Word) []string {
	words := make([]string, 0, len(args))
	for _, arg := range args {
		words = append(words, strings.TrimSpace(wordText(arg)))
	}
	return words
}

// powerShellSourceDynamic reports whether the words carrying a PowerShell
// host's command source contain an expansion. wordText silently drops those,
// so `powershell -Command "$PAYLOAD"` otherwise extracts an empty payload and
// classifies as clean while the host runs whatever the shell expanded.
func powerShellSourceDynamic(source powerShellPayload, args []*syntax.Word) bool {
	if source.sourceIndex < 0 {
		return false
	}
	for index := source.sourceIndex; index < len(args); index++ {
		if !isLiteralWord(args[index]) {
			return true
		}
	}
	return false
}

// envSplitSourceDynamic reports whether an env invocation takes its
// -S/--split-string argv from a word this scan cannot resolve statically.
//
// The literal reconstruction used by envSplitCommandFields is an optimization,
// not a proof of safety: `PAYLOAD='curl https://…'; env -S "$PAYLOAD"` runs the
// expanded argv, so an unreadable operand must classify as network-sensitive
// rather than fall through to wrapper handling that reports no executable.
func envSplitSourceDynamic(args []*syntax.Word) bool {
	texts := make([]string, len(args))
	for index, arg := range args {
		texts[index] = wordText(arg)
	}
	start, ok := envArgumentStart(texts)
	if !ok {
		return false
	}
	seenSplit := false
	for index := start; index < len(texts); index++ {
		text := texts[index]
		if text == "--" {
			return false
		}
		if _, _, _, split := envSplitOption([]string{text}, 0); split {
			// A joined operand (-S"$PAYLOAD") reconstructs to the option alone, so
			// the option token itself carries the dynamic source.
			if !isLiteralWord(args[index]) {
				return true
			}
			seenSplit = true
			continue
		}
		if !isLiteralWord(args[index]) {
			if seenSplit {
				return true
			}
			continue
		}
		if seenSplit {
			// The operand of a separated -S is literal; the ordinary literal path
			// already reads it.
			return false
		}
		if strings.Contains(text, "=") && !strings.HasPrefix(text, "=") && !strings.HasPrefix(text, "-") {
			continue
		}
		if strings.HasPrefix(text, "-") {
			if wrapperConsumesValue("env", text) && index+1 < len(texts) {
				index++
			}
			continue
		}
		// A command position was reached without a split string; ordinary wrapper
		// resolution classifies this invocation.
		return false
	}
	return false
}

// busyboxSourceDynamic reports whether a BusyBox invocation's applet-name
// operand — the token busyboxCommandArgs treats as the delegated child
// executable — comes from a word this scan cannot resolve statically.
//
// busyboxCommandArgs runs on wordTexts, which silently drops expansions:
// `APPLET=curl; busybox "$APPLET" https://…` would otherwise resolve the
// applet position to an empty string, which is neither a recognized BusyBox
// flag nor the executable this scan can name, and the invocation reads as an
// ordinary unrecognized command rather than as "unknown, assume network."
func busyboxSourceDynamic(args []*syntax.Word) bool {
	if len(args) == 0 {
		return false
	}
	return !isLiteralWord(args[0])
}

// straceSourceDynamic is busyboxSourceDynamic's counterpart for strace: it
// reports whether the operand straceCommandArgs would treat as the traced
// child command comes from a word this scan cannot resolve statically.
// straceChildIndex is shared with straceCommandArgs so this check walks
// strace's option grammar exactly once, rather than duplicating it and
// risking the two silently drifting apart.
func straceSourceDynamic(args []*syntax.Word) bool {
	index, ok := straceChildIndex(wordTexts(args))
	if !ok || index >= len(args) {
		return false
	}
	return !isLiteralWord(args[index])
}

// envArgumentStart returns the index just past an `env` program token, allowing
// the wrapper prefixes (sudo, nice, ...) that may precede it.
func envArgumentStart(texts []string) (int, bool) {
	wrapper := ""
	for index := 0; index < len(texts); index++ {
		text := texts[index]
		if text == "" {
			if wrapper == "" {
				return 0, false
			}
			continue
		}
		if strings.Contains(text, "=") && !strings.HasPrefix(text, "=") && !strings.HasPrefix(text, "-") {
			continue
		}
		if strings.HasPrefix(text, "-") {
			if wrapperConsumesValue(wrapper, text) && index+1 < len(texts) {
				index++
			}
			continue
		}
		if isNumericToken(text) {
			continue
		}
		token := normalizeProgramToken(text)
		if token == "env" {
			return index + 1, true
		}
		if wrapperPrograms[token] {
			wrapper = token
			continue
		}
		return 0, false
	}
	return 0, false
}

func literalCallFields(args []*syntax.Word) ([]string, bool) {
	fields := make([]string, 0, len(args))
	for _, arg := range args {
		if !isLiteralWord(arg) {
			return nil, false
		}
		fields = append(fields, wordText(arg))
	}
	return fields, true
}

// textualPayloadUsesNetwork classifies source carried by another interpreter
// without leaking the nested parser's TooComplex bit into the outer command.
func textualPayloadUsesNetwork(payload string, depth int) bool {
	if depth > maxAnalyzerDepth {
		return false
	}
	result := AnalysisResult{}
	analyzeInto(payload, &result, map[string]bool{}, depth)
	return result.Network || (result.TooComplex && matchesUnparseableNetworkAt(payload, depth))
}

func pythonModuleUsesNetwork(words []string) bool {
	for index := 0; index < len(words); index++ {
		if words[index] != "-m" || index+1 >= len(words) {
			continue
		}
		module := words[index+1]
		if module == "http.server" {
			return true
		}
		if module == "pip" && firstSubcommand(words[index+2:], nil) == "install" {
			return true
		}
	}
	return false
}

func ghUsesNetwork(words []string) bool {
	first := firstSubcommand(words, nil)
	if first == "api" {
		return true
	}
	second := secondSubcommand(words)
	return (first == "release" && second == "download") ||
		(first == "repo" && second == "clone")
}

func secondSubcommand(words []string) string {
	firstSeen := false
	for _, word := range words {
		if word == "" || strings.HasPrefix(word, "-") {
			continue
		}
		if !firstSeen {
			firstSeen = true
			continue
		}
		return word
	}
	return ""
}

func firstSubcommand(words []string, aliases map[string]string) string {
	for _, word := range words {
		if word == "" || strings.HasPrefix(word, "-") || isNumericToken(word) {
			continue
		}
		if aliases != nil {
			if alias, ok := aliases[word]; ok {
				return alias
			}
		}
		return word
	}
	return ""
}

// wordText returns the literal text of a shell word, concatenating its plain and
// quoted literal parts (so "vim", 'vim', and vim all yield "vim"). Parts that are
// expansions ($x, $(...)) contribute nothing — the program name is taken as-is.
func wordText(word *syntax.Word) string {
	if word == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range word.Parts {
		switch typed := part.(type) {
		case *syntax.Lit:
			builder.WriteString(typed.Value)
		case *syntax.SglQuoted:
			builder.WriteString(typed.Value)
		case *syntax.DblQuoted:
			for _, inner := range typed.Parts {
				if lit, ok := inner.(*syntax.Lit); ok {
					builder.WriteString(lit.Value)
				}
			}
		}
	}
	return builder.String()
}

// effectiveProgram resolves the real command behind wrapper prefixes (sudo, env,
// nice, timeout, ...) and their consumed option values in an AST arg list,
// returning the program token and the args that follow it. It mirrors
// firstProgram in safe_command.go. An expansion-only program word ($x) yields ""
// because it cannot be classified statically.
func effectiveProgram(args []*syntax.Word) (string, []*syntax.Word) {
	wrapper := ""
	for index := 0; index < len(args); index++ {
		text := wordText(args[index])
		if text == "" {
			// A dynamic ($x) token in the PROGRAM position can't be classified, so
			// fail closed. But once we're past a wrapper, a dynamic arg is most
			// likely a wrapper flag/value — keep scanning so the literal payload that
			// follows is still classified (e.g. `env "$opts" curl …`).
			if wrapper == "" {
				return "", nil
			}
			continue
		}
		if strings.Contains(text, "=") && !strings.HasPrefix(text, "=") {
			continue // env-assignment prefix (e.g. `env FOO=bar cmd`)
		}
		if strings.HasPrefix(text, "-") {
			// Only consume the next token as a value when the ACTIVE wrapper says
			// this flag takes one; otherwise a valueless flag (e.g. `sudo -n`) would
			// swallow the real payload command (`rm`/`curl`).
			if wrapperConsumesValue(wrapper, text) && index+1 < len(args) {
				index++
			}
			continue
		}
		if isNumericToken(text) {
			continue
		}
		token := normalizeProgramToken(text)
		if wrapperPrograms[token] {
			wrapper = token
			continue
		}
		return token, args[index+1:]
	}
	return "", nil
}

// replSuppressed reports whether a REPL program (python/node/...) was invoked
// non-interactively — with an inline-eval flag or a script argument — mirroring
// nonInteractiveREPLFlags used by the regex detector. Non-REPL interactive
// programs are never suppressed.
func replSuppressed(prog string, args []*syntax.Word) bool {
	flags, isREPL := nonInteractiveREPLFlags[prog]
	if !isREPL {
		return false
	}
	for _, arg := range args {
		text := wordText(arg)
		if text == "" {
			continue
		}
		for _, flag := range flags {
			if text == flag || strings.HasPrefix(text, flag+"=") {
				return true
			}
		}
		// A bare (non-flag) argument is a script path, e.g. `python app.py`.
		if !strings.HasPrefix(text, "-") {
			return true
		}
	}
	return false
}

// hasRecursiveForce reports whether an rm argument list contains both recursive
// and force flags (-rf, -r -f, --recursive --force, ...), the destructive form.
func hasRecursiveForce(args []*syntax.Word) bool {
	recursive, force := false, false
	for _, arg := range args {
		text := wordText(arg)
		switch {
		case text == "--":
			// End-of-options: every later token is an operand (a filename), so a
			// trailing `-rf`/`--force` is literal. `rm -- -rf` deletes a file named
			// "-rf" and must not be treated as the destructive recursive-force form.
			return recursive && force
		case text == "--recursive":
			recursive = true
		case text == "--force":
			force = true
		case strings.HasPrefix(text, "--"):
			// other long flag — ignore
		case strings.HasPrefix(text, "-"):
			for _, char := range text[1:] {
				switch char {
				case 'r', 'R':
					recursive = true
				case 'f':
					force = true
				}
			}
		}
	}
	return recursive && force
}

func hasPowerShellRecursiveForce(args []*syntax.Word) bool {
	recursive, force := false, false
	for _, arg := range args {
		switch strings.ToLower(wordText(arg)) {
		case "-recurse", "-r":
			recursive = true
		case "-force":
			force = true
		}
	}
	return recursive && force
}

func hasFindDelete(args []*syntax.Word) bool {
	for _, arg := range args {
		if wordText(arg) == "-delete" {
			return true
		}
	}
	return false
}
