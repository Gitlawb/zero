//go:build linux

// Command zero-seccomp is a tiny exec wrapper for the Linux sandbox: it installs
// the Unix-socket-blocking seccomp filter on itself and then execs the real
// command, so a bubblewrap-sandboxed process cannot create AF_UNIX sockets (a gap
// bubblewrap's filesystem/network isolation leaves open). Wire it by prefixing the
// sandboxed command with this binary, e.g. `bwrap ... -- zero-seccomp <command>`.
//
// It degrades gracefully: if the filter cannot be installed (e.g. the kernel lacks
// seccomp), it warns and runs the command anyway, since bubblewrap still provides
// the primary isolation.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/Gitlawb/zero/internal/sandbox"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: zero-seccomp <command> [args...]")
		os.Exit(2)
	}
	if err := sandbox.ApplyUnixSocketBlock(); err != nil {
		fmt.Fprintln(os.Stderr, "zero-seccomp: warning: "+err.Error()+"; running without the Unix-socket filter")
	}
	binary, err := exec.LookPath(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "zero-seccomp: "+err.Error())
		os.Exit(127)
	}
	if err := syscall.Exec(binary, os.Args[1:], os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "zero-seccomp: exec failed: "+err.Error())
		os.Exit(126)
	}
}
