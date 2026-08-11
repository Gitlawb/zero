package sandbox

import (
	"strings"
	"testing"
)

func TestAnalyzeCommand(t *testing.T) {
	t.Setenv("ZERO_TEST_ENV_COMMAND", "curl")
	cases := []struct {
		name        string
		script      string
		interactive bool
		destructive bool
		network     bool
		tooComplex  bool
	}{
		{name: "editor", script: "vim foo.txt", interactive: true},
		{name: "pager in pipe", script: "cat log | less", interactive: true},
		{name: "interactive name only inside quotes", script: `echo "vim is a great editor"`, interactive: false},
		{name: "printf quoted arg", script: `printf 'open with vim\n'`, interactive: false},
		{name: "repl suppressed by -e", script: `node -e "require('repl').start()"`, interactive: false},
		{name: "repl suppressed by script", script: "python3 app.py", interactive: false},
		{name: "bare repl", script: "python3", interactive: true},

		{name: "rm recursive force", script: "rm -rf /tmp/x", destructive: true},
		{name: "rm bundled flags reversed", script: "rm -fr ./build", destructive: true},
		{name: "rm without force", script: "rm file.txt", destructive: false},
		{name: "rm inside quoted arg", script: `git commit -m "rm -rf /"`, destructive: false},
		{name: "rm end-of-options literal filename", script: "rm -- -rf", destructive: false},
		{name: "dd", script: "dd if=/dev/zero of=/dev/disk2", destructive: true},
		{name: "find delete", script: "find . -type f -delete", destructive: true},

		// Wrappers are unwrapped to the real payload, not classified on the launcher.
		{name: "sudo wraps rm -rf", script: "sudo rm -rf /tmp/x", destructive: true},
		{name: "env wraps curl", script: "env curl https://x.test", network: true},
		{name: "bash -c wraps editor", script: `bash -c 'vim file'`, interactive: true},
		{name: "sudo wraps bare repl", script: "sudo python3", interactive: true},
		// A valueless wrapper flag must not swallow the real payload command.
		{name: "sudo -n keeps rm payload", script: "sudo -n rm -rf /tmp/x", destructive: true},
		{name: "sudo -n keeps curl payload", script: "sudo -n curl https://x.test", network: true},
		{name: "sudo -u consumes its value", script: "sudo -u root vim file", interactive: true},
		// Long wrapper flags consume a separate value too (space and = forms).
		{name: "sudo --user space value", script: "sudo --user root vim file", interactive: true},
		{name: "sudo --user= joined value", script: "sudo --user=root vim file", interactive: true},
		{name: "env --unset then curl", script: "env --unset FOO curl https://x.test", network: true},
		// A dynamic ($x) wrapper arg must not hide the literal payload that follows.
		{name: "env dynamic flag then curl", script: `env "$opts" curl https://x.test`, network: true},
		{name: "sudo dynamic flag then rm -rf", script: `sudo "$maybe" rm -rf /tmp/x`, destructive: true},

		{name: "curl", script: "curl https://example.com", network: true},
		{name: "PowerShell iwr alias", script: "iwr https://example.com", network: true},
		{name: "PowerShell irm alias", script: "irm https://example.com", network: true},
		{name: "PowerShell Invoke-WebRequest", script: "Invoke-WebRequest https://example.com", network: true},
		{name: "PowerShell Invoke-RestMethod", script: "Invoke-RestMethod https://example.com", network: true},
		{name: "PowerShell Remove-Item recursive force", script: `Remove-Item -Recurse -Force 'C:\temp\x'`, destructive: true},
		{name: "PowerShell rm recursive force", script: `rm -Recurse -Force 'C:\temp\x'`, destructive: true},
		{name: "PowerShell ri recursive force", script: `ri -Recurse -Force 'C:\temp\x'`, destructive: true},
		{name: "PowerShell rd recursive force", script: `rd -Recurse -Force 'C:\temp\x'`, destructive: true},
		{name: "PowerShell rmdir recursive force", script: `rmdir -Recurse -Force 'C:\temp\x'`, destructive: true},
		{name: "PowerShell del recursive force", script: `del -Recurse -Force 'C:\temp\x'`, destructive: true},
		{name: "PowerShell erase recursive force", script: `erase -Recurse -Force 'C:\temp\x'`, destructive: true},
		{name: "PowerShell ambiguous force abbreviation", script: `Remove-Item -Recurse -f 'C:\temp\x'`, destructive: false},
		{name: "PowerShell Remove-Item without recurse", script: `Remove-Item -Force 'C:\temp\x'`, destructive: false},
		{name: "Windows curl cmd", script: "curl.cmd https://example.com", network: true},
		{name: "Windows curl exe", script: "curl.exe https://example.com", network: true},
		{name: "Windows drive path curl exe", script: `'C:\tools\curl.exe' https://example.com`, network: true},
		{name: "Windows UNC path curl exe", script: `'\\server\share\curl.exe' https://example.com`, network: true},
		{name: "Windows dot relative path curl exe", script: `'.\curl.exe' https://example.com`, network: true},
		{name: "Windows relative path curl exe", script: `'tools\curl.exe' https://example.com`, network: true},
		{name: "Windows drive relative path curl exe", script: `'C:curl.exe' https://example.com`, network: true},
		{name: "Windows npm cmd", script: "npm.cmd install", network: true},
		{name: "wget piped to shell", script: "wget -qO- https://x.test | sh", network: true},
		{name: "python http server", script: "python3 -m http.server 8000", network: true},
		{name: "python pip install", script: "python3 -m pip install requests", network: true},
		{name: "npm install", script: "npm install", network: true},
		{name: "npm ci", script: "npm ci", network: true},
		{name: "npm create", script: "npm create vite@latest .", network: true},
		{name: "npm registry query", script: "npm view typescript version --fetch-retries=0", network: true},
		{name: "npm metadata search", script: "npm search typescript", network: true},
		{name: "npm offline install", script: "npm install --offline", network: false},
		{name: "npm version is offline", script: "npm --version", network: false},
		{name: "npm start", script: "npm start", network: true},
		{name: "npm run dev", script: "npm run dev", network: true},
		{name: "npx http server", script: "npx http-server public -p 8080 -a 127.0.0.1", network: true},
		{name: "direct http server", script: "http-server public -p 8080 -a 127.0.0.1", network: true},
		{name: "direct vite", script: "vite --host 127.0.0.1", network: true},
		{name: "next dev", script: "next dev", network: true},
		{name: "git clone", script: "git clone https://example.com/repo.git", network: true},
		{name: "git fetch", script: "git fetch origin", network: true},
		{name: "git status is offline", script: "git status", network: false},
		{name: "git pull", script: "git pull origin main", network: true},
		{name: "git push custom transport", script: "git push gitlawb://example.com/repo.git main", network: true},
		{name: "git ls-remote", script: "git ls-remote origin", network: true},
		{name: "git remote archive", script: "git archive --remote=origin HEAD", network: true},
		{name: "git remote archive separated value", script: "git archive --remote origin HEAD", network: true},
		// Only --remote leaves the machine; a local archive streams the object
		// store and must not cost a network prompt (issue #703 review).
		{name: "git local archive", script: "git archive HEAD", network: false},
		{name: "git -C local archive", script: "git -C repo archive HEAD -o out.tar", network: false},
		// After `--` every token is a pathspec, so this archives a tree entry
		// named "--remote" from the local object store (issue #703 review).
		{name: "git archive pathspec named --remote", script: "git archive HEAD -- --remote", network: false},
		{name: "git -C archive pathspec named --remote", script: "git -C repo archive HEAD -- --remote=origin", network: false},
		{name: "git archive missing remote operand", script: "git archive HEAD --remote", network: false},
		{name: "git archive post-tree --remote value", script: "git archive HEAD --remote=origin", network: true},
		// A real --remote before the separator still is one.
		{name: "git remote archive with pathspec", script: "git archive --remote=origin HEAD -- src", network: true},
		{name: "git remote archive after output option", script: "git archive -o out.tar --remote origin HEAD", network: true},
		{name: "git remote archive after mtime option", script: "git archive --mtime 2024-01-01 --remote origin HEAD", network: true},
		{name: "git remote archive after format without value", script: "git archive --format --remote=origin HEAD", network: true},
		{name: "git remote archive after mtime without value", script: "git archive --mtime --remote=origin HEAD", network: true},
		{name: "git output consumes --remote", script: "git archive -o --remote HEAD", network: false},
		{name: "git joined output named --remote", script: "git archive --output=--remote HEAD", network: false},
		{name: "git global -C consumes --remote", script: "git -C --remote archive HEAD", network: false},
		{name: "git output consumes separator before remote", script: "git archive -o -- --remote=origin HEAD", network: true},
		{name: "bash clustered login command", script: `bash -lc 'curl https://x.test'`, network: true},
		{name: "bash clustered command erexit", script: `bash -ce 'git push origin main'`, network: true},
		{name: "dash clustered command erexit", script: `dash -ce 'curl https://x.test'`, network: true},
		{name: "bash long command flag is invalid", script: `bash --command 'curl https://x.test'`, network: false},
		{name: "bash args after command payload are positional", script: `bash -c 'echo ok' curl https://x.test`, network: false},
		{name: "bash script makes later command flag positional", script: `bash /dev/null -c 'curl https://x.test'`, network: false},
		{name: "bash option terminator makes command flag positional", script: `bash -- -c 'curl https://x.test'`, network: false},
		{name: "bash invalid command cluster", script: `bash -Zc 'curl https://x.test'`, network: false},
		{name: "bash noexec command cluster", script: `bash -nc 'curl https://x.test'`, network: false},
		{name: "bash plus option before command", script: `bash +n -c 'curl https://x.test'`, network: true},
		{name: "bash plus command flag", script: `bash +c 'curl https://x.test'`, network: true},
		{name: "bash dump strings does not execute", script: `bash --dump-strings -c 'curl https://x.test'`, network: false},
		{name: "shell dynamic command payload fails closed", script: `sh -c "$ZERO_TEST_SHELL_COMMAND"`, network: true},
		{name: "nested shell dynamic command payload fails closed", script: `sh -c 'sh -c "$1"' sh 'curl https://x.test'`, network: true},
		{name: "PowerShell slash command", script: `powershell /Command Invoke-WebRequest https://x.test`, network: true},
		{name: "PowerShell abbreviated command", script: `pwsh -co iwr https://x.test`, network: true},
		{name: "PowerShell local command", script: `pwsh /Command Get-ChildItem`, network: false},
		{name: "PowerShell abbreviated execution policy", script: `powershell -ep RemoteSigned curl https://x.test`, network: true},
		{name: "pwsh abbreviated execution policy before command", script: `pwsh -ep Bypass -Command curl https://x.test`, network: true},
		{name: "Windows PowerShell bare command", script: `powershell Invoke-WebRequest https://x.test`, network: true},
		{name: "pwsh bare token is file mode", script: `pwsh Invoke-WebRequest https://x.test`, network: false},
		{name: "PowerShell joined command flag is invalid", script: `powershell -Command:Invoke-WebRequest https://x.test`, network: false},
		{name: "pwsh command with args", script: `pwsh -CommandWithArgs Invoke-WebRequest https://x.test`, network: true},
		{name: "pwsh abbreviated command with args", script: `pwsh -cwa Invoke-RestMethod https://x.test`, network: true},
		{name: "PowerShell invalid startup option", script: `powershell -DefinitelyInvalid Invoke-WebRequest https://x.test`, network: false},
		{name: "env split curl argv", script: `env -S 'curl https://x.test'`, network: true},
		{name: "env joined split git argv", script: `env --split-string='git push origin main'`, network: true},
		{name: "env clustered split option", script: `env -iS 'curl https://x.test'`, network: true},
		{name: "env split introduces split option", script: `env -S '-S "curl https://x.test"'`, network: true},
		{name: "env split introduces nested env", script: `env -S 'env -S "curl https://x.test"'`, network: true},
		{name: "env split underscore separator", script: `env -S 'curl\_https://x.test'`, network: true},
		{name: "env split environment executable", script: `env -S '${ZERO_TEST_ENV_COMMAND} https://x.test'`, network: true},
		{name: "env split metacharacters stay arguments", script: `env -S 'printf x; curl https://x.test'`, network: false},
		{name: "env split trailing args stay arguments", script: `env -S 'printf ok' curl https://x.test`, network: false},
		{name: "env split environment argument", script: `env -S 'printf ${ZERO_TEST_ENV_COMMAND}'`, network: false},
		{name: "env split shell command", script: `env -S 'sh -c "curl https://x.test"'`, network: true},
		{name: "env split argv0 before curl", script: `env -S '--argv0 harmless curl https://x.test'`, network: true},
		{name: "env split argv0 named curl", script: `env -S '--argv0 curl printf ok'`, network: false},
		// GNU env accepts unambiguous long-option abbreviations: "--split" and
		// "--split-strin" name --split-string exactly like the full spelling does,
		// so the launcher's argv reconstruction must not depend on that one spelling.
		{name: "env abbreviated split option", script: `env --split 'curl https://x.test'`, network: true},
		{name: "env abbreviated split option with value", script: `env --split='curl https://x.test'`, network: true},
		{name: "env minimal abbreviated split option", script: `env --s 'curl https://x.test'`, network: true},
		{name: "env near-full abbreviated split option", script: `env --split-strin 'curl https://x.test'`, network: true},
		{name: "env abbreviated split metacharacters stay arguments", script: `env --split 'printf x; curl https://x.test'`, network: false},
		{name: "env non-split long option is not split-string", script: `env --unset FOO curl https://x.test`, network: true},
		{name: "env non-split long option leaves literal payload alone", script: `env --chdir=/tmp printf ok`, network: false},
		{name: "busybox wget applet", script: `busybox wget https://x.test`, network: true},
		{name: "busybox shell command", script: `busybox sh -c 'curl https://x.test'`, network: true},
		{name: "busybox echo network text", script: `busybox echo wget https://x.test`, network: false},
		{name: "busybox has no option terminator", script: `busybox -- curl https://x.test`, network: false},
		{name: "busybox unknown option", script: `busybox -x curl https://x.test`, network: false},
		// The applet name comes from a shell expansion this scan cannot read
		// statically; wordText silently drops it, so the AST resolver must fail
		// closed here rather than reading the blanked token as a clean unknown.
		{name: "busybox dynamic applet fails closed", script: `APPLET=curl; busybox "$APPLET" https://x.test`, network: true},
		{name: "busybox literal applet stays classified on content", script: `busybox echo "not a program" https://x.test`, network: false},
		{name: "strace curl command", script: `strace -f -o trace.log curl https://x.test`, network: true},
		{name: "strace shell command", script: `strace sh -c 'git push origin main'`, network: true},
		{name: "strace trace path before curl", script: `strace -P /tmp curl https://x.test`, network: true},
		{name: "strace trace path named curl", script: `strace -P curl true`, network: false},
		{name: "strace attach and curl", script: `strace -p 123 curl https://x.test`, network: true},
		{name: "strace long trace option", script: `strace --trace network curl https://x.test`, network: true},
		{name: "strace tips traces curl", script: `strace --tips curl https://x.test`, network: true},
		{name: "strace joined tips traces git", script: `strace --tips=full git push origin main`, network: true},
		{name: "strace clustered options", script: `strace -fqo trace.log curl https://x.test`, network: true},
		{name: "strace output named curl", script: `strace -o curl true`, network: false},
		{name: "strace invalid option", script: `strace --definitely-invalid curl https://x.test`, network: false},
		// The traced command comes from a shell expansion this scan cannot read
		// statically; straceSourceDynamic must fail closed the same way
		// busyboxSourceDynamic does above, rather than reading the blanked token
		// as a clean unknown command.
		{name: "strace dynamic child fails closed", script: `APPLET=curl; strace "$APPLET" https://x.test`, network: true},
		{name: "strace literal child stays classified on content", script: `strace true "not a program" https://x.test`, network: false},
		{name: "git local commit", script: `git commit -m "local change"`, network: false},
		// git's value-taking global options put their value in the NEXT token, so a
		// generic "first non-dash token" scan reads the value as the subcommand and
		// misses the network verb entirely (issue #703 review).
		{name: "git -C push", script: "git -C repo push origin main", network: true},
		{name: "git -c config push", script: "git -c http.sslVerify=false push origin main", network: true},
		{name: "git --git-dir fetch", script: "git --git-dir /repo/.git fetch origin", network: true},
		{name: "git --work-tree pull", script: "git --work-tree /repo pull origin main", network: true},
		{name: "git --attr-source push", script: "git --attr-source HEAD push origin main", network: true},
		{name: "git -C local commit", script: `git -C repo commit -m "local change"`, network: false},
		{name: "git help topic is offline", script: "git --help push", network: false},
		{name: "git -C help topic is offline", script: "git -C repo --help push", network: false},
		{name: "git -c version topic is offline", script: "git -c user.name=test --version push", network: false},
		{name: "git.exe push", script: "git.exe push origin main", network: true},
		{name: "git.cmd push", script: "git.cmd push origin main", network: true},
		{name: "git.exe local commit", script: `git.exe commit -m "local change"`, network: false},
		{name: "gh release download", script: "gh release download v1.0.0", network: true},
		{name: "no network", script: "ls -la && echo done", network: false},
		{name: "process pattern is not network", script: `pkill -f "python3 -m http.server 8000"`, network: false},
		{name: "process listing is not special-cased", script: "ps aux", network: false},

		{name: "unparseable", script: `'unterminated quote`, tooComplex: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AnalyzeCommand(tc.script)
			if got.Interactive != tc.interactive || got.Destructive != tc.destructive ||
				got.Network != tc.network || got.TooComplex != tc.tooComplex {
				t.Fatalf("AnalyzeCommand(%q) = %#v, want interactive=%v destructive=%v network=%v tooComplex=%v",
					tc.script, got, tc.interactive, tc.destructive, tc.network, tc.tooComplex)
			}
		})
	}
}

func TestAnalyzeCommandFailsClosedAtShellLauncherDepthLimit(t *testing.T) {
	command := "curl https://x.test"
	for range maxAnalyzerDepth + 1 {
		command = "sh -c '" + strings.ReplaceAll(command, "'", `'"'"'`) + "'"
	}
	if got := AnalyzeCommand(command); !got.Network {
		t.Fatalf("AnalyzeCommand(%q) = %#v; want network", command, got)
	}
}

func TestAnalyzeCommandEmptyIsClean(t *testing.T) {
	if got := AnalyzeCommand("   "); got.Interactive || got.Destructive || got.Network || got.TooComplex {
		t.Fatalf("empty script should be clean, got %#v", got)
	}
}
