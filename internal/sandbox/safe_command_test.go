package sandbox

import (
	"strings"
	"testing"
)

func TestDetectInteractiveCommandBlocksEditors(t *testing.T) {
	cases := []struct {
		name         string
		command      string
		wantCmd      string
		wantSuggHint string
	}{
		{name: "vim", command: "vim main.go", wantCmd: "vim", wantSuggHint: "non-interactive"},
		{name: "nano", command: "nano notes.txt", wantCmd: "nano"},
		{name: "less pager", command: "less /var/log/syslog", wantCmd: "less", wantSuggHint: "cat"},
		{name: "python repl", command: "python", wantCmd: "python", wantSuggHint: "-c"},
		{name: "node repl", command: "node", wantCmd: "node", wantSuggHint: "-e"},
		// -v is "verbose" for mysql (NOT "version"), so it must still open the
		// prompt — the info-exit allowance is for unambiguous --version/--help only.
		{name: "mysql verbose not version", command: "mysql -v", wantCmd: "mysql", wantSuggHint: "-e"},
		{name: "ssh interactive", command: "ssh host.example.com", wantCmd: "ssh"},
		{name: "top", command: "top", wantCmd: "top"},
		{name: "git rebase interactive", command: "git rebase -i HEAD~3", wantCmd: "git rebase -i"},
		{name: "tail follow", command: "tail -f app.log", wantCmd: "tail -f"},
		{name: "env prefix vim", command: "EDITOR=vim FOO=bar vim file", wantCmd: "vim"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := DetectInteractiveCommand(tc.command, "linux")
			if !result.Interactive {
				t.Fatalf("DetectInteractiveCommand(%q) = not interactive, want interactive", tc.command)
			}
			if result.Command != tc.wantCmd {
				t.Fatalf("matched command = %q, want %q", result.Command, tc.wantCmd)
			}
			if result.Suggestion == "" {
				t.Fatalf("expected an actionable suggestion for %q", tc.command)
			}
			if tc.wantSuggHint != "" && !strings.Contains(strings.ToLower(result.Suggestion), strings.ToLower(tc.wantSuggHint)) {
				t.Fatalf("suggestion %q does not mention %q", result.Suggestion, tc.wantSuggHint)
			}
		})
	}
}

func TestDetectInteractiveCommandAllowsNonInteractive(t *testing.T) {
	cases := []string{
		"",
		"ls -la",
		"go test ./...",
		"python -c 'print(1)'",
		"python3 script.py",
		"node -e 'console.log(1)'",
		"node build.js",
		"node --version",
		"node -v",
		"node --check app.js",
		"node --help",
		"node --version && npm --version",
		"python3 --version",
		"php --version",
		"cat file.txt",
		"git rebase --continue",
		"git status",
		"tail -n 50 app.log",
		"ssh host 'uptime'",
		"grep -r foo .",
	}
	for _, command := range cases {
		t.Run(command, func(t *testing.T) {
			result := DetectInteractiveCommand(command, "linux")
			if result.Interactive {
				t.Fatalf("DetectInteractiveCommand(%q) = interactive (%q), want allowed", command, result.Command)
			}
		})
	}
}

func TestDetectInteractiveCommandHonorsWindows(t *testing.T) {
	// edit and notepad are Windows-only interactive launchers.
	if result := DetectInteractiveCommand("notepad config.ini", "windows"); !result.Interactive {
		t.Fatalf("expected notepad to be interactive on windows")
	}
	if result := DetectInteractiveCommand("notepad config.ini", "linux"); result.Interactive {
		t.Fatalf("notepad should not be treated as interactive on linux")
	}
}

func TestDetectInteractiveCommandFindsAcrossSeparators(t *testing.T) {
	// Interactive commands hidden after a shell operator should still be caught.
	for _, command := range []string{
		"git pull && vim conflict.txt",
		"echo hi; less log.txt",
		"true | nano",
	} {
		result := DetectInteractiveCommand(command, "linux")
		if !result.Interactive {
			t.Fatalf("DetectInteractiveCommand(%q) = not interactive, want interactive", command)
		}
	}
}

// Finding 3: firstProgram must skip additional wrappers (nice/timeout/stdbuf/
// setsid/ionice/xargs), skip leading option tokens for sudo/env, and recurse
// into `sh -c`/`bash -c <payload>`.
func TestDetectInteractiveThroughWrappersAndShellC(t *testing.T) {
	cases := []struct {
		name    string
		command string
		wantCmd string
	}{
		{name: "nice", command: "nice vim file.txt", wantCmd: "vim"},
		{name: "timeout", command: "timeout 5 vim file.txt", wantCmd: "vim"},
		{name: "stdbuf", command: "stdbuf -oL vim file.txt", wantCmd: "vim"},
		{name: "setsid", command: "setsid vim file.txt", wantCmd: "vim"},
		{name: "ionice", command: "ionice -c3 vim file.txt", wantCmd: "vim"},
		{name: "xargs", command: "xargs vim", wantCmd: "vim"},
		{name: "sudo with option", command: "sudo -u root vim file.txt", wantCmd: "vim"},
		{name: "sudo with long option value", command: "sudo --user root vim file.txt", wantCmd: "vim"},
		{name: "sudo with long option joined value", command: "sudo --user=root vim file.txt", wantCmd: "vim"},
		{name: "env with assignment option", command: "env -i EDITOR=x vim file.txt", wantCmd: "vim"},
		{name: "sh -c payload", command: "sh -c 'vim file.txt'", wantCmd: "vim"},
		{name: "bash -c payload", command: `bash -c "less /var/log/syslog"`, wantCmd: "less"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := DetectInteractiveCommand(tc.command, "linux")
			if !result.Interactive {
				t.Fatalf("DetectInteractiveCommand(%q) = not interactive, want interactive", tc.command)
			}
			if result.Command != tc.wantCmd {
				t.Fatalf("matched command = %q, want %q", result.Command, tc.wantCmd)
			}
		})
	}
}

// Audit finding (MED): the interactive-program detector must not be bypassed by
// quote/escape characters embedded INSIDE the program token (e.g. `vi\m`,
// `v"i"m`, `'v'im`), not just surrounding it.
func TestDetectInteractiveStripsEmbeddedQuotingFromToken(t *testing.T) {
	cases := []struct {
		name    string
		command string
		wantCmd string
	}{
		{name: "mid-token backslash", command: `vi\m file.txt`, wantCmd: "vim"},
		{name: "embedded double quotes", command: `v"i"m file.txt`, wantCmd: "vim"},
		{name: "leading single quote split", command: `'v'im file.txt`, wantCmd: "vim"},
		{name: "escaped less", command: `le\ss /var/log/syslog`, wantCmd: "less"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := DetectInteractiveCommand(tc.command, "linux")
			if !result.Interactive {
				t.Fatalf("DetectInteractiveCommand(%q) = not interactive, want interactive", tc.command)
			}
			if result.Command != tc.wantCmd {
				t.Fatalf("matched command = %q, want %q", result.Command, tc.wantCmd)
			}
		})
	}
}

// Audit finding (LOW): interactive SEGMENTS (e.g. "git rebase -i", "tail -f")
// must match only on a real command/segment boundary, not anywhere as a raw
// substring of the whole command. Otherwise the text appearing inside a quoted
// argument produces a false positive.
func TestDetectInteractiveSegmentBoundary(t *testing.T) {
	// False positives: the segment text appears only inside an argument/quotes.
	allowed := []string{
		`echo "git rebase -i is interactive"`,
		`grep "tail -f" notes.txt`,
		`echo run docker attach later`,
		`printf 'kubectl logs -f streams'`,
		// A literal ')' that does not close a $(...) must not split the command
		// into a fake `less` segment (regression for unconditional ')' splitting).
		`echo "a) less"`,
		`echo "(done) vim later"`,
	}
	for _, cmd := range allowed {
		if got := DetectInteractiveCommand(cmd, "linux"); got.Interactive {
			t.Errorf("expected %q NOT to be flagged interactive (got %q)", cmd, got.Command)
		}
	}
	// True positives must still be caught at a real boundary.
	blocked := []struct {
		command string
		wantCmd string
	}{
		{`git rebase -i HEAD~3`, "git rebase -i"},
		{`tail -f app.log`, "tail -f"},
		{`git pull && git rebase -i HEAD~2`, "git rebase -i"},
		{`docker logs -f mycontainer`, "docker logs -f"},
	}
	for _, tc := range blocked {
		got := DetectInteractiveCommand(tc.command, "linux")
		if !got.Interactive || got.Command != tc.wantCmd {
			t.Errorf("DetectInteractiveCommand(%q) = (%v,%q), want interactive %q", tc.command, got.Interactive, got.Command, tc.wantCmd)
		}
	}
}

func TestSplitShellSegmentsParenOnlySplitsInsideSubstitution(t *testing.T) {
	// A literal ')' that does not close a $(...) must not be a segment boundary,
	// otherwise text like "a) less" splits into a fake `less` segment.
	if segs := splitShellSegments(`echo "a) less"`); len(segs) != 1 {
		t.Fatalf(`splitShellSegments(echo "a) less") = %#v, want a single segment`, segs)
	}
	// A real $(...) still isolates the substituted command so it can be analyzed.
	segs := splitShellSegments(`echo $(less foo)`)
	found := false
	for _, s := range segs {
		if s == "less foo" {
			found = true
		}
	}
	if !found {
		t.Fatalf(`splitShellSegments(echo $(less foo)) = %#v, want a "less foo" segment`, segs)
	}
	// Nested substitutions track depth correctly: the inner ')' closes $(b ...),
	// the outer ')' closes $(a ...), and neither leaks an empty/garbage segment.
	nested := splitShellSegments(`a $(b $(c) d) e`)
	for _, s := range nested {
		if s == "" {
			t.Fatalf("nested substitution produced an empty segment: %#v", nested)
		}
	}

	// A ')' inside a double-quoted argument WITHIN a substitution is literal and
	// must not close the $(...) early — otherwise a fake `less` segment appears.
	for _, seg := range splitShellSegments(`echo $(printf "a) less")`) {
		if seg == "less\"" || seg == "less" || strings.HasPrefix(seg, "less") {
			t.Fatalf(`a quoted ')' closed the substitution early, producing %q in %#v`, seg, splitShellSegments(`echo $(printf "a) less")`))
		}
	}

	// A real substitution still spanning double quotes (`"$(vim)"`) isolates the
	// inner command so an interactive program inside it is still caught.
	foundVim := false
	for _, seg := range splitShellSegments(`echo "$(vim x)"`) {
		if seg == "vim x" {
			foundVim = true
		}
	}
	if !foundVim {
		t.Fatalf(`splitShellSegments(echo "$(vim x)") must isolate "vim x", got %#v`, splitShellSegments(`echo "$(vim x)"`))
	}
}

func TestSplitShellSegmentsIsEscapeAware(t *testing.T) {
	// An escaped operator outside quotes is literal and must not split.
	if segs := splitShellSegments(`echo foo\|less`); len(segs) != 1 {
		t.Fatalf(`splitShellSegments(echo foo\|less) = %#v, want a single segment`, segs)
	}
	// An escaped quote inside double quotes must not toggle quoting, so the '|'
	// stays quoted and does not manufacture a fake `less` segment.
	if segs := splitShellSegments(`printf "use \"| less"`); len(segs) != 1 {
		t.Fatalf(`splitShellSegments(printf "use \"| less") = %#v, want a single segment`, segs)
	}
	// A real, unescaped operator still splits.
	if segs := splitShellSegments(`a | less`); len(segs) != 2 {
		t.Fatalf(`splitShellSegments(a | less) = %#v, want two segments`, segs)
	}
}

func TestDetectInteractiveBypasses(t *testing.T) {
	blocked := []string{
		"/usr/bin/vim file.txt",     // absolute path
		"\"vim\" file.txt",          // double-quoted program
		"'vim' file.txt",            // single-quoted program
		"echo $(vim file.txt)",      // command substitution
		"echo `vim file.txt`",       // backtick substitution
		"echo \"`true | less`\"",    // backtick in double quotes hides an inner pager
		"echo \"$(true | less)\"",   // $() in double quotes hides an inner pager
		"/bin/less /var/log/syslog", // absolute pager
	}
	for _, cmd := range blocked {
		if got := DetectInteractiveCommand(cmd, "linux"); !got.Interactive {
			t.Errorf("expected %q to be detected as interactive", cmd)
		}
	}
	// must NOT over-block legitimate non-interactive commands
	allowed := []string{
		"python script.py",   // script, not REPL
		"cat vim.txt",        // file named vim, not the editor
		"grep ssh config.go", // 'ssh' as a search term
		"echo hello",
	}
	for _, cmd := range allowed {
		if got := DetectInteractiveCommand(cmd, "linux"); got.Interactive {
			t.Errorf("expected %q NOT to be flagged interactive (got %q)", cmd, got.Command)
		}
	}
}

// Audit finding (MED): splitShellSegments must be quote-aware. A shell operator
// inside quotes is a literal argument, not a separator, so it must not split the
// command and falsely flag a quoted program name — while real, unquoted operators
// must still split (no new false negatives).
func TestDetectInteractiveQuoteAwareSeparators(t *testing.T) {
	allowed := []string{
		`git commit -m "use top | less"`, // | inside double quotes
		`echo "a; vim b"`,                // ; inside double quotes
		`echo 'pipe it: a | less'`,       // | inside single quotes
		`git commit -m "vim && nano"`,    // && inside double quotes
	}
	for _, cmd := range allowed {
		if got := DetectInteractiveCommand(cmd, "linux"); got.Interactive {
			t.Errorf("expected %q NOT to be flagged (operator is quoted), got %q", cmd, got.Command)
		}
	}

	blocked := []struct {
		command string
		wantCmd string
	}{
		{`echo hi | less`, "less"},            // real unquoted pipe
		{`echo "safe" | vim`, "vim"},          // real pipe after a quoted arg
		{`git commit -m "msg"; vim x`, "vim"}, // real ; after a quoted arg
		{`echo "$(vim x)"`, "vim"},            // substitution still active in double quotes
	}
	for _, tc := range blocked {
		got := DetectInteractiveCommand(tc.command, "linux")
		if !got.Interactive || got.Command != tc.wantCmd {
			t.Errorf("DetectInteractiveCommand(%q) = (%v,%q), want interactive %q", tc.command, got.Interactive, got.Command, tc.wantCmd)
		}
	}
}

func TestDetectInteractiveMongoEvalAndFullPaths(t *testing.T) {
	cases := []struct {
		command     string
		interactive bool
	}{
		{"mongo --eval 'db.test.find()'", false},
		{"mongosh --eval 'db.test.find()'", false},
		{"mongo", true},
		{"/usr/bin/python script.py", false}, // full-path program + script arg -> not a REPL
		{"/bin/bash -c 'vim file'", true},    // full-path shell -c with nested interactive program
	}
	for _, tc := range cases {
		got := DetectInteractiveCommand(tc.command, "linux").Interactive
		if got != tc.interactive {
			t.Errorf("DetectInteractiveCommand(%q).Interactive = %v, want %v", tc.command, got, tc.interactive)
		}
	}
}
