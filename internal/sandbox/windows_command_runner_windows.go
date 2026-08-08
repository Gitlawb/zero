//go:build windows

package sandbox

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func runWindowsSandboxCommand(config WindowsSandboxCommandConfig, stderr io.Writer) int {
	switch config.SandboxLevel {
	case WindowsSandboxLevelRestrictedToken:
		if err := ValidateWindowsSandboxSetupMarker(WindowsSandboxSetupConfigFromCommand(config)); err != nil {
			fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
			return 1
		}
	case WindowsSandboxLevelUnelevated:
		if err := ensureWindowsUnelevatedSetup(config); err != nil {
			fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
			return 1
		}
	default:
		fmt.Fprintf(stderr, "%s: unsupported Windows sandbox level %q\n", WindowsSandboxCommandRunnerName, config.SandboxLevel)
		return 1
	}
	if err := ValidateWindowsNetworkPolicy(config.PermissionProfile.Network); err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	capabilitySIDs, err := WindowsCapabilitySIDsForConfig(config)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	offlineSID, err := WindowsOfflineMarkerSID(config.SandboxHome)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	// Compose the restricting-SID set: both modes keep the write-capability SIDs
	// (workspace write-jail); deny additionally carries the offline-marker SID
	// that the persistent WFP block filter matches — so a deny command has no
	// network while an approved allow command reaches it, both write-jailed.
	//
	// KNOWN LIMITATION: an approved online command reaches the network, but HTTPS
	// via Windows Schannel (e.g. a Schannel-backed curl.exe) fails inside this
	// restricted token with SEC_E_NO_CREDENTIALS — Schannel can't acquire its
	// per-user TLS credential under a WRITE_RESTRICTED/LUA token. This is a
	// fundamental restricted-token vs Schannel incompatibility (the standard
	// mitigation is to run TLS in a broker process, not the sandboxed one) and
	// has no clean in-token fix. Workarounds: the degraded path (no restricted
	// token) or the in-process web_fetch tool.
	//
	// KNOWN LIMITATION: MSYS2/Cygwin binaries (Git for Windows bash.exe,
	// sh.exe, and the usr\bin coreutils) cannot initialize under this token at
	// all, whether invoked directly or spawned internally by an otherwise
	// native command (git hooks, git/gh credential helpers). The MSYS runtime
	// secures its signal pipe and shared-memory sections with explicit DACLs
	// granting only the user, Administrators, and SYSTEM (msys2-runtime
	// sigproc.cc sigproc_init -> sec_user_nih -> __sec_user), and a
	// WRITE_RESTRICTED write check must ALSO match one of the token's
	// restricted SIDs (logon SID, Everyone, capability SIDs). None of the
	// granted SIDs can be added to the restricted list without collapsing the
	// write jail (each has write access nearly everywhere), so MSYS startup
	// dies with "couldn't create signal pipe" or "CreateFileMapping <SID>.1",
	// Win32 error 5, and exit status 0xC0000142. The System32 WSL bash
	// launcher fails equivalently (the restricted token cannot connect to the
	// WSL service: Bash/Service/CreateInstance/E_ACCESSDENIED). Like Schannel,
	// this has no in-token fix; preflight blocking and output hints live in
	// internal/tools/shell_runtime.go.
	tokenSIDs := windowsRuntimeTokenSIDs(capabilitySIDs, offlineSID, config.PermissionProfile.Network.Mode)
	// A WRITE_RESTRICTED token keeps reads unrestricted so sandboxed commands
	// can actually launch executables; it is only unsafe when DenyRead paths
	// are configured, because the kernel skips restricted-SID deny ACEs for
	// reads under that flag (#612). Profiles with DenyRead keep the fully
	// restricted token, trading spawn capability for read-deny enforcement.
	writeRestricted := len(config.PermissionProfile.FileSystem.DenyRead) == 0

	// A provisioned sandbox principal replaces the restricted token entirely: it
	// is a separate account, so reads outside its granted roots are denied by the
	// filesystem rather than left open the way a same-user restricted token has
	// to leave them (#662). Absent, unprovisioned or opted-out, ok is false and
	// the restricted-token backend below runs exactly as before.
	principalToken, ok, err := windowsSandboxPrincipalToken(config)
	if err != nil {
		// The one path here that does not fall back, because a provisioned but
		// unusable principal means the sandbox is broken rather than absent. Say
		// how to get out of it, since the whole backend is opt-in.
		fmt.Fprintf(stderr, "%s: sandbox principal is provisioned but unusable: %v. Re-run `zero sandbox setup` from an elevated terminal, or unset %s to fall back to the restricted-token sandbox.\n",
			WindowsSandboxCommandRunnerName, err, windowsSandboxIdentityEnv)
		return 1
	}
	if ok {
		defer principalToken.Close()
		// The principal gets its own identity AND the write jail, not one or the
		// other. Its ACEs confine reads; without the restricted token it would
		// still hold every write its ambient memberships grant, so a profile
		// permitting writes only to the workspace could still write anywhere
		// BATCH or BUILTIN\Users may — C:\Users\Public\Documents, for one.
		//
		// The principal's own SID joins the capability SIDs because the ACL plan
		// grants the workspace to that SID; leaving it out jails the principal
		// out of the tree it is supposed to own.
		principalUser, err := principalToken.GetTokenUser()
		if err != nil {
			fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": read sandbox principal SID: "+err.Error())
			return 1
		}
		jailSIDs := append(append([]string{}, tokenSIDs...), principalUser.User.Sid.String())
		jailedToken, err := restrictWindowsTokenForCapabilitySIDs(principalToken, jailSIDs, writeRestricted)
		if err != nil {
			fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
			return 1
		}
		defer jailedToken.Close()
		exitCode, err := runWindowsCommandAsUser(jailedToken, config)
		if err != nil {
			fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
			return 1
		}
		return exitCode
	}

	token, err := createWindowsRestrictedTokenForCapabilitySIDs(tokenSIDs, writeRestricted)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	defer token.Close()
	exitCode, err := runWindowsCommandAsUser(token, config)
	if err != nil {
		fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
		return 1
	}
	return exitCode
}

// ensureWindowsUnelevatedSetup applies the workspace ACL plan from the current
// (non-elevated) process so the write-restricted token has somewhere its
// capability SIDs are granted. DACL edits on user-owned workspace and temp
// roots need no Administrator rights; the WFP network filters DO, so this tier
// provisions no network enforcement — the offline-marker SID composed into the
// token stays inert until an elevated `zero sandbox setup` installs the block
// filters. Applied plans are recorded by hash so repeat commands skip the
// re-apply; like the elevated setup, grants are left in place (the rollback is
// deliberately discarded) because they only name synthetic capability SIDs
// that no other token carries.
func ensureWindowsUnelevatedSetup(config WindowsSandboxCommandConfig) error {
	applied, plan, err := buildWindowsUnelevatedAppliedPlan(config)
	if err != nil {
		return err
	}
	marker, err := loadWindowsUnelevatedSetupMarker(config.SandboxHome)
	if err != nil {
		return err
	}
	if marker.contains(applied) {
		return nil
	}
	if _, err := applyWindowsACLPlan(plan); err != nil {
		// Refusing to run is right: without these ACEs the write jail does not
		// exist, so continuing would run the command believing it is sandboxed
		// when it is not. What was wrong was the diagnosis. Every failure got the
		// same "the workspace may be on a filesystem you do not own" guess, and
		// the suggested remedy was elevated setup, which does not help at all
		// when the real problem is one root in the plan that nobody can ACL.
		//
		// Being precise matters because this failure repeats: the success marker
		// is only recorded on success, so the same plan fails identically on
		// every later command until the offending root leaves it. A reader who
		// cannot tell which root is at fault has no way out of that.
		if denied := windowsACLPlanDeniedPath(err); denied != "" {
			return fmt.Errorf("apply unelevated workspace ACLs: %w; %s cannot have its permissions changed by this user, "+
				"so the sandbox cannot enforce a write boundary there and will not run the command. "+
				"That path is one of this workspace's sandbox roots, usually a system directory that arrived via TEMP or TMP. "+
				"Check those, or re-run with `--sandbox forbid` to skip OS sandboxing. "+
				"Running `zero sandbox setup` elevated will NOT fix this", err, denied)
		}
		return fmt.Errorf("apply unelevated workspace ACLs: %w — the workspace may be on a filesystem the current user does not own; "+
			"run `zero sandbox setup` from an elevated (Administrator) terminal, or re-run with `--sandbox forbid` to skip OS sandboxing", err)
	}
	return recordWindowsUnelevatedAppliedPlan(config.SandboxHome, applied)
}

// windowsACLPlanDeniedPath pulls the target path out of an apply failure that
// was an access denial, and returns "" for anything else.
//
// applyWindowsACLPathGroup already wraps the path into its error, so this reads
// the message rather than threading a typed error through four layers for one
// diagnostic. The string it matches is produced in the same package by
// openWindowsACLTarget, and a test pins the pairing so the two cannot drift
// apart silently.
func windowsACLPlanDeniedPath(err error) string {
	if err == nil || !errors.Is(err, os.ErrPermission) {
		return ""
	}
	const marker = "open windows ACL target "
	message := err.Error()
	start := strings.Index(message, marker)
	if start < 0 {
		return ""
	}
	// Colon-SPACE, not colon. The wrapper is "...target %s: %w", and on Windows
	// the path itself starts with a drive colon, so splitting on the first colon
	// returns "C". A drive colon is always followed by a separator, never a
	// space, which makes ": " the only unambiguous boundary here.
	rest := message[start+len(marker):]
	end := strings.Index(rest, ": ")
	if end <= 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}
