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

// networkPrograms are commands that perform network egress/ingress.
var networkPrograms = map[string]bool{
	"curl": true, "wget": true, "ssh": true, "scp": true, "sftp": true,
	"rsync": true, "nc": true, "ncat": true, "netcat": true, "telnet": true,
	"ftp": true,
}

// AnalyzeCommand parses script and reports interactive/destructive/network usage
// from the shell AST. A script that cannot be parsed yields TooComplex (with no
// other flags set) so the caller can decide how to treat an unanalyzable command.
func AnalyzeCommand(script string) AnalysisResult {
	result := AnalysisResult{}
	if strings.TrimSpace(script) == "" {
		return result
	}
	file, err := syntax.NewParser().Parse(strings.NewReader(script), "")
	if err != nil {
		result.TooComplex = true
		return result
	}

	seen := map[string]bool{}
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		prog := normalizeProgramToken(wordText(call.Args[0]))
		if prog == "" {
			return true
		}
		if !seen[prog] {
			seen[prog] = true
			result.Programs = append(result.Programs, prog)
		}
		if _, interactive := interactivePrograms[prog]; interactive && !replSuppressed(prog, call.Args[1:]) {
			result.Interactive = true
		}
		if networkPrograms[prog] {
			result.Network = true
		}
		if destructivePrograms[prog] || (prog == "rm" && hasRecursiveForce(call.Args[1:])) {
			result.Destructive = true
		}
		return true
	})
	return result
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
