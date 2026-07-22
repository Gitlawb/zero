package agent

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/Gitlawb/zero/internal/sandbox"
	"github.com/Gitlawb/zero/internal/tools"
	"mvdan.cc/sh/v3/syntax"
)

func proposedCommandPrefix(toolName string, args map[string]any) []string {
	if !isShellCommandTool(toolName) {
		return nil
	}
	command, ok := firstStringArg(args, "command", "cmd", "script", "shell")
	if !ok {
		return nil
	}
	segments, ok := safeShellCommandSegments(command)
	if !ok {
		return nil
	}
	if requested, ok := requestedPrefixRule(args); ok {
		if safeRequestedPrefixForSegments(requested, segments) {
			return requested
		}
		return nil
	}
	// Only propose approving a prefix of one segment when every other segment
	// in the command is independently known-safe. Once a prefix is approved,
	// shellExecutionArgsForApproval escalates the whole command (every
	// segment, not just the approved one) to bypass the sandbox, so offering a
	// prefix that leaves an MSYS-prone (or otherwise unsafe) segment uncovered
	// would let that segment run unsandboxed without ever being reviewed.
	for index, tokens := range segments {
		if knownSafeCommandSegment(tokens) {
			continue
		}
		if !sandbox.ValidCommandPrefix(tokens) || !otherSegmentsKnownSafe(segments, index) {
			return nil
		}
		return append([]string(nil), tokens...)
	}
	if len(segments) == 0 || !sandbox.ValidCommandPrefix(segments[0]) {
		return nil
	}
	return append([]string(nil), segments[0]...)
}

// commandPrefixLadder returns the breadth choices offered for a shell command's
// prefix grant, ordered broadest → most specific. The most specific entry equals
// proposedCommandPrefix (today's default). Broader entries are shorter token
// prefixes of at least two tokens; a lone launcher/command token (e.g. "yarn")
// is never offered because a one-token grant is too broad — it would approve every
// later subcommand of that program (e.g. "yarn add", "yarn publish", arbitrary
// scripts), exactly the package-manager class that must stay non-grantable. When
// the final token carries a namespace separator (e.g. "test:unit") an intra-token
// wildcard level ("test:*") is inserted just before the exact one. Returns nil when
// there is nothing to choose between (zero or one safe level), so callers keep the
// single-prefix behavior.
func commandPrefixLadder(toolName string, args map[string]any) [][]string {
	base := proposedCommandPrefix(toolName, args)
	if len(base) == 0 {
		return nil
	}
	ladder := make([][]string, 0, len(base)+1)
	seen := map[string]bool{}
	add := func(candidate []string) {
		if len(candidate) == 0 || !sandbox.ValidCommandPrefix(candidate) {
			return
		}
		key := strings.Join(candidate, "\x00")
		if seen[key] {
			return
		}
		seen[key] = true
		ladder = append(ladder, append([]string(nil), candidate...))
	}
	// Start at two tokens: a one-token prefix (base[:1]) is a bare launcher/command
	// name, and granting it would approve every subcommand of that program, so it is
	// never offered as a reusable breadth.
	for length := 2; length < len(base); length++ {
		add(base[:length])
	}
	if wildcard, ok := intraTokenWildcardPrefix(base); ok {
		add(wildcard)
	}
	add(base)
	if len(ladder) <= 1 {
		return nil
	}
	return ladder
}

// intraTokenWildcardPrefix turns a base prefix whose final token has a namespace
// separator into a trailing-wildcard variant, e.g. ["npm","run","test:unit"] ->
// ["npm","run","test:*"]. It never wildcards a lone launcher token (a single-token
// base). ok is false when the final token has no usable separator.
func intraTokenWildcardPrefix(base []string) ([]string, bool) {
	if len(base) < 2 {
		return nil, false
	}
	last := base[len(base)-1]
	// Use the LAST separator so a nested name keeps its deepest namespace segment
	// (e.g. "test:unit:fast" -> "test:unit:*", not the broader "test:*").
	index := strings.LastIndexAny(last, ":-/.@")
	if index <= 0 || index >= len(last)-1 {
		// No separator, a leading separator, or a trailing separator — nothing
		// meaningful to widen into a namespace wildcard.
		return nil, false
	}
	wildcard := append([]string(nil), base[:len(base)-1]...)
	wildcard = append(wildcard, last[:index+1]+"*")
	return wildcard, true
}

// otherSegmentsKnownSafe reports whether every segment other than the one at
// skip is known-safe on its own.
func otherSegmentsKnownSafe(segments [][]string, skip int) bool {
	for index, tokens := range segments {
		if index == skip {
			continue
		}
		if !knownSafeCommandSegment(tokens) {
			return false
		}
	}
	return true
}

// grantPrefixForDecision resolves which command prefix to grant. It honors the
// approver's breadth choice (decision.CommandPrefix) only when that choice is a
// valid prefix AND was one of the options the request offered; otherwise it falls
// back to the request's default prefix. Because the offered options were derived
// from this exact command, an offered choice already matches it — so a stale or
// overbroad selection can never widen the grant beyond what was presented.
func grantPrefixForDecision(request PermissionRequest, decision PermissionDecision) []string {
	fallback := append([]string(nil), request.CommandPrefix...)
	if len(decision.CommandPrefix) == 0 {
		return fallback
	}
	if !sandbox.ValidCommandPrefix(decision.CommandPrefix) {
		return fallback
	}
	if !commandPrefixOffered(request.CommandPrefixOptions, decision.CommandPrefix) {
		return fallback
	}
	return append([]string(nil), decision.CommandPrefix...)
}

func commandPrefixOffered(options [][]string, prefix []string) bool {
	for _, option := range options {
		if equalStringSlices(option, prefix) {
			return true
		}
	}
	return false
}

func matchCommandPrefix(toolName string, args map[string]any, options Options) (sandbox.CommandPrefixGrant, bool, bool) {
	if !isShellCommandTool(toolName) || options.Sandbox == nil {
		return sandbox.CommandPrefixGrant{}, false, false
	}
	if shellCommandAdditionalPermissionsRequested(args) {
		return sandbox.CommandPrefixGrant{}, false, false
	}
	command, ok := firstStringArg(args, "command", "cmd", "script", "shell")
	if !ok {
		return sandbox.CommandPrefixGrant{}, false, false
	}
	segments, ok := safeShellCommandSegments(command)
	if !ok {
		return sandbox.CommandPrefixGrant{}, false, false
	}
	// A prefix grant that matches lets the command run unsandboxed (see
	// shellExecutionArgsForApproval). `cd` is a known-safe segment, so a composite
	// like `cd /other && go test` would otherwise honor a grant saved for THIS
	// project yet execute in another directory outside it. Bind the grant to the
	// effective directory: if a `cd` moves execution outside the workspace root (or
	// to a target we cannot prove stays inside it), refuse the match so the command
	// falls back to the normal sandboxed prompt instead of an out-of-scope bypass.
	if !commandDirStaysWithinProject(segments, options.Sandbox.WorkspaceRoot()) {
		return sandbox.CommandPrefixGrant{}, false, false
	}
	var matched sandbox.CommandPrefixGrant
	matchedAny := false
	matchedSession := false
	for _, tokens := range segments {
		if grant, ok := options.Sandbox.LookupCommandPrefix(toolName, tokens); ok {
			if !matchedAny {
				matched = grant
			}
			matchedAny = true
			continue
		}
		if grant, ok := options.Sandbox.LookupCommandPrefixForSession(toolName, tokens); ok {
			if !matchedAny {
				matched = grant
			}
			matchedAny = true
			matchedSession = true
			continue
		}
		if knownSafeCommandSegment(tokens) {
			continue
		}
		return sandbox.CommandPrefixGrant{}, false, false
	}
	if matchedAny {
		return matched, true, matchedSession
	}
	return sandbox.CommandPrefixGrant{}, false, false
}

// commandDirStaysWithinProject reports whether a composite command's `cd`
// segments keep execution inside root. It starts at root and follows each `cd`;
// a target that resolves outside root, or one that cannot be resolved statically
// (no argument, `-`, `~`/home, an environment variable, a glob, or extra args),
// is treated as leaving the project so the caller refuses the unsandboxed grant.
// With no root there is no project to bind to, so any `cd` is rejected.
func commandDirStaysWithinProject(segments [][]string, root string) bool {
	root = strings.TrimSpace(root)
	if root == "" {
		// No project to bind to: only safe if the command never changes directory.
		return !commandChangesDirectory(segments)
	}
	// Resolve the root's real path once. Containment is checked against real paths
	// (EvalSymlinks) so a symlink inside the workspace pointing outside it cannot
	// satisfy a lexical prefix check and smuggle an unsandboxed grant out of scope.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	effective := realRoot
	for _, tokens := range segments {
		if len(tokens) == 0 || commandName(tokens[0]) != "cd" {
			continue
		}
		target, ok := resolveCdTarget(tokens[1:], effective)
		if !ok {
			return false
		}
		// The target must exist and, once symlinks are resolved, stay within the
		// real workspace root. A non-existent target or a broken/escaping symlink
		// cannot be proven in-project, so it fails closed.
		realTarget, err := filepath.EvalSymlinks(target)
		if err != nil || !pathWithinRoot(realTarget, realRoot) {
			return false
		}
		effective = realTarget
	}
	return true
}

// commandChangesDirectory reports whether any segment is a `cd`.
func commandChangesDirectory(segments [][]string) bool {
	for _, tokens := range segments {
		if len(tokens) > 0 && commandName(tokens[0]) == "cd" {
			return true
		}
	}
	return false
}

// resolveCdTarget resolves a `cd` argument list to an absolute directory relative
// to cwd. ok is false for forms whose destination cannot be known statically.
func resolveCdTarget(args []string, cwd string) (string, bool) {
	if len(args) != 1 {
		return "", false // bare `cd` (home) or too many args
	}
	arg := args[0]
	if arg == "" || arg == "-" || arg == "~" || strings.HasPrefix(arg, "~") {
		return "", false // previous dir or home-relative: not statically knowable
	}
	if strings.ContainsAny(arg, "$*?[") {
		return "", false // variable expansion or glob
	}
	if filepath.IsAbs(arg) {
		return filepath.Clean(arg), true
	}
	return filepath.Clean(filepath.Join(cwd, arg)), true
}

// pathWithinRoot reports whether target is root or a descendant of it.
func pathWithinRoot(target, root string) bool {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func shellExecutionArgsForApproval(toolName string, args map[string]any, action PermissionDecisionAction, options Options) map[string]any {
	if !isShellCommandTool(toolName) || !shellPrefixApprovalBypassesSandbox(action) {
		return args
	}
	if options.Sandbox == nil || !options.Sandbox.UnsandboxedExecutionAllowed() {
		return args
	}
	if shellCommandAdditionalPermissionsRequested(args) || shellCommandRequiresEscalated(args) {
		return args
	}
	planned := cloneArgs(args)
	if planned == nil {
		planned = map[string]any{}
	}
	planned["sandbox_permissions"] = string(tools.SandboxPermissionsRequireEscalated)
	return planned
}

func shellPrefixApprovalBypassesSandbox(action PermissionDecisionAction) bool {
	return isCommandPrefixDecision(action)
}

// isCommandPrefixDecision reports whether action is one of the command-prefix
// grant tiers (session, project, or global).
func isCommandPrefixDecision(action PermissionDecisionAction) bool {
	switch action {
	case PermissionDecisionAllowPrefix, PermissionDecisionAllowPrefixProject, PermissionDecisionAlwaysAllowPrefix:
		return true
	default:
		return false
	}
}

func shellCommandRequiresEscalated(args map[string]any) bool {
	raw, ok := args["sandbox_permissions"]
	if !ok || raw == nil {
		return false
	}
	value, ok := raw.(string)
	if !ok {
		value = fmt.Sprint(raw)
	}
	return strings.TrimSpace(value) == string(tools.SandboxPermissionsRequireEscalated)
}

func isShellCommandTool(toolName string) bool {
	return toolName == "bash" || toolName == "exec_command"
}

func firstStringArg(args map[string]any, names ...string) (string, bool) {
	for _, name := range names {
		if raw, ok := args[name].(string); ok {
			value := strings.TrimSpace(raw)
			if value != "" {
				return value, true
			}
		}
	}
	return "", false
}

func requestedPrefixRule(args map[string]any) ([]string, bool) {
	raw, ok := args["prefix_rule"]
	if !ok {
		return nil, false
	}
	switch value := raw.(type) {
	case []string:
		return cleanPrefixRule(value), true
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			part, ok := item.(string)
			if !ok {
				return nil, true
			}
			out = append(out, part)
		}
		return cleanPrefixRule(out), true
	default:
		return nil, true
	}
}

func cleanPrefixRule(prefix []string) []string {
	cleaned := make([]string, 0, len(prefix))
	for _, part := range prefix {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil
		}
		cleaned = append(cleaned, part)
	}
	return cleaned
}

func safeRequestedPrefixForSegments(prefix []string, segments [][]string) bool {
	if len(prefix) == 0 || !sandbox.ValidCommandPrefix(prefix) {
		return false
	}
	matched := false
	for _, command := range segments {
		if len(prefix) > len(command) {
			if knownSafeCommandSegment(command) {
				continue
			}
			return false
		}
		if hasStringPrefix(command, prefix) {
			matched = true
			continue
		}
		if knownSafeCommandSegment(command) {
			continue
		}
		return false
	}
	return matched
}

func safeShellCommandSegments(command string) ([][]string, bool) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, false
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) == 0 {
		return nil, false
	}
	segments := make([][]string, 0, len(file.Stmts))
	for _, stmt := range file.Stmts {
		if !collectSafeShellStatement(stmt, &segments) {
			return nil, false
		}
	}
	if len(segments) == 0 {
		return nil, false
	}
	return segments, true
}

func collectSafeShellStatement(stmt *syntax.Stmt, segments *[][]string) bool {
	if stmt == nil || stmt.Negated || stmt.Background || stmt.Coprocess || stmt.Disown || len(stmt.Redirs) > 0 {
		return false
	}
	return collectSafeShellCommand(stmt.Cmd, segments)
}

func collectSafeShellCommand(cmd syntax.Command, segments *[][]string) bool {
	switch node := cmd.(type) {
	case *syntax.CallExpr:
		tokens, ok := literalCallTokens(node)
		if !ok {
			return false
		}
		*segments = append(*segments, tokens)
		return true
	case *syntax.BinaryCmd:
		switch node.Op {
		case syntax.AndStmt, syntax.OrStmt, syntax.Pipe:
		default:
			return false
		}
		return collectSafeShellStatement(node.X, segments) && collectSafeShellStatement(node.Y, segments)
	default:
		return false
	}
}

func literalCallTokens(call *syntax.CallExpr) ([]string, bool) {
	if call == nil || len(call.Assigns) > 0 || len(call.Args) == 0 {
		return nil, false
	}
	tokens := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		if len(word.Parts) != 1 {
			return nil, false
		}
		lit, ok := word.Parts[0].(*syntax.Lit)
		if !ok || strings.ContainsAny(lit.Value, "*?[]{}") {
			return nil, false
		}
		tokens = append(tokens, lit.Value)
	}
	return tokens, true
}

func knownSafeCommandSegment(command []string) bool {
	if len(command) == 0 {
		return false
	}
	name := commandName(command[0])
	if runtime.GOOS == "windows" && tools.MsysProneCommandName(name) {
		return false
	}
	switch name {
	case "cat", "cd", "cut", "echo", "expr", "false", "grep", "head", "id",
		"ls", "nl", "paste", "pwd", "rev", "seq", "stat", "tail", "tr",
		"true", "uname", "uniq", "wc", "which", "whoami":
		return true
	case "base64":
		return safeBase64Command(command[1:])
	case "find":
		return safeFindCommand(command[1:])
	case "rg":
		return safeRipgrepCommand(command[1:])
	case "sed":
		return safeSedCommand(command[1:])
	case "git":
		return safeGitCommand(command)
	default:
		return false
	}
}

func safeBase64Command(args []string) bool {
	for _, arg := range args {
		if arg == "-o" || arg == "--output" || strings.HasPrefix(arg, "--output=") || (strings.HasPrefix(arg, "-o") && arg != "-o") {
			return false
		}
	}
	return true
}

func safeFindCommand(args []string) bool {
	unsafe := map[string]bool{
		"-exec": true, "-execdir": true, "-ok": true, "-okdir": true,
		"-delete": true,
		"-fls":    true, "-fprint": true, "-fprint0": true, "-fprintf": true,
	}
	for _, arg := range args {
		if unsafe[arg] {
			return false
		}
	}
	return true
}

func safeRipgrepCommand(args []string) bool {
	for _, arg := range args {
		if arg == "--search-zip" || arg == "-z" || arg == "--pre" || arg == "--hostname-bin" ||
			strings.HasPrefix(arg, "--pre=") || strings.HasPrefix(arg, "--hostname-bin=") {
			return false
		}
	}
	return true
}

func safeSedCommand(args []string) bool {
	if len(args) < 2 || len(args) > 3 || args[0] != "-n" {
		return false
	}
	return validSedPrintArg(args[1])
}

func validSedPrintArg(arg string) bool {
	if !strings.HasSuffix(arg, "p") {
		return false
	}
	body := strings.TrimSuffix(arg, "p")
	if body == "" {
		return false
	}
	for _, part := range strings.Split(body, ",") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}

func safeGitCommand(command []string) bool {
	subIndex, subcommand, ok := gitSubcommand(command)
	if !ok {
		return false
	}
	if gitHasUnsafeGlobalOption(command[1:subIndex]) {
		return false
	}
	args := command[subIndex+1:]
	switch subcommand {
	case "status", "log", "diff", "show":
		return gitArgsReadOnly(args)
	case "branch":
		return gitArgsReadOnly(args) && gitBranchReadOnly(args)
	default:
		return false
	}
}

func gitSubcommand(command []string) (int, string, bool) {
	for index := 1; index < len(command); index++ {
		arg := command[index]
		if gitOptionConsumesValue(arg) {
			index++
			continue
		}
		if gitOptionHasInlineValue(arg) || arg == "--" || strings.HasPrefix(arg, "-") {
			continue
		}
		switch arg {
		case "status", "log", "diff", "show", "branch":
			return index, arg, true
		default:
			return 0, "", false
		}
	}
	return 0, "", false
}

func gitOptionConsumesValue(arg string) bool {
	switch arg {
	case "-C", "-c", "--config-env", "--exec-path", "--git-dir", "--namespace", "--super-prefix", "--work-tree":
		return true
	default:
		return false
	}
}

func gitOptionHasInlineValue(arg string) bool {
	return strings.HasPrefix(arg, "--config-env=") ||
		strings.HasPrefix(arg, "--exec-path=") ||
		strings.HasPrefix(arg, "--git-dir=") ||
		strings.HasPrefix(arg, "--namespace=") ||
		strings.HasPrefix(arg, "--super-prefix=") ||
		strings.HasPrefix(arg, "--work-tree=") ||
		((strings.HasPrefix(arg, "-C") || strings.HasPrefix(arg, "-c")) && len(arg) > 2)
}

func gitHasUnsafeGlobalOption(args []string) bool {
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case strings.HasPrefix(arg, "--upload-pack"):
			return true
		case arg == "-C" || strings.HasPrefix(arg, "-C"):
			return true
		case gitOptionConsumesValue(arg):
			index++
		}
	}
	return false
}

func gitArgsReadOnly(args []string) bool {
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--output="), strings.HasPrefix(arg, "--exec="):
			return false
		case arg == "--output", arg == "--exec":
			return false
		}
	}
	return true
}

func gitBranchReadOnly(args []string) bool {
	if len(args) == 0 {
		return true
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--format=") {
			continue
		}
		switch arg {
		case "--list", "-l", "--show-current", "-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose":
			continue
		default:
			return false
		}
	}
	return true
}

func commandName(raw string) string {
	name := strings.TrimSpace(raw)
	if index := strings.LastIndexAny(name, `/\`); index >= 0 {
		name = name[index+1:]
	}
	name = strings.ToLower(name)
	for _, suffix := range []string{".exe", ".cmd", ".bat", ".com"} {
		if strings.HasSuffix(name, suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return name
}

func hasStringPrefix(values []string, prefix []string) bool {
	if len(prefix) == 0 || len(prefix) > len(values) {
		return false
	}
	for index := range prefix {
		if values[index] != prefix[index] {
			return false
		}
	}
	return true
}

func equalStringSlices(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
