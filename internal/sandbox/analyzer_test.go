package sandbox

import "testing"

func TestAnalyzeCommand(t *testing.T) {
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
		{name: "wget piped to shell", script: "wget -qO- https://x.test | sh", network: true},
		{name: "python http server", script: "python3 -m http.server 8000", network: true},
		{name: "python pip install", script: "python3 -m pip install requests", network: true},
		{name: "npm install", script: "npm install", network: true},
		{name: "npm start", script: "npm start", network: true},
		{name: "npm run dev", script: "npm run dev", network: true},
		{name: "npx http server", script: "npx http-server public -p 8080 -a 127.0.0.1", network: true},
		{name: "direct http server", script: "http-server public -p 8080 -a 127.0.0.1", network: true},
		{name: "direct vite", script: "vite --host 127.0.0.1", network: true},
		{name: "next dev", script: "next dev", network: true},
		{name: "git clone", script: "git clone https://example.com/repo.git", network: true},
		{name: "gh release download", script: "gh release download v1.0.0", network: true},
		{name: "no network", script: "ls -la && echo done", network: false},
		{name: "process pattern is not network", script: `pkill -f "python3 -m http.server 8000"`, network: false},

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

func TestAnalyzeCommandEmptyIsClean(t *testing.T) {
	if got := AnalyzeCommand("   "); got.Interactive || got.Destructive || got.Network || got.TooComplex {
		t.Fatalf("empty script should be clean, got %#v", got)
	}
}
