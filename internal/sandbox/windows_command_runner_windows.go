//go:build windows

package sandbox

import (
	"fmt"
	"io"
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
	// Broadening with Users/Authenticated Users is only useful on the fully
	// restricted token, where READS also require a restricted-SID match and
	// system paths like Program Files and System32 grant those groups rather
	// than Everyone. A WRITE_RESTRICTED token already performs reads with the
	// normal token identity, so broadening there cannot improve reads at all;
	// it would only let Users/Authenticated Users write grants pass the
	// restricted-SID write check and weaken the default write jail for no
	// benefit. It also needs the elevated tier: only that tier can enforce
	// the shared-directory DenyWrite mitigation BuildWindowsACLPlan adds for
	// the broadened SIDs (it requires Administrator rights); see
	// createWindowsRestrictedTokenFromBase.
	// The broadened identities are also gated on the machine's volume
	// layout: the compensating shared-path DenyWrite mitigation covers only
	// system-drive paths, so on a host with any other fixed volume (whose
	// root typically grants Authenticated Users Modify with volume-wide
	// inheritance) the broadened token could write outside every configured
	// write root. Fail closed there and keep the narrow SID set — reads of
	// Users-granted system paths stay broken on such hosts, but the write
	// jail holds.
	//
	// Before broadening, revalidate/reapply direct denies on any currently
	// Users/AuthUsers-writable descendants of the shared roots. Setup alone is
	// a point-in-time snapshot; non-inheriting denies do not cover children
	// created afterward. If coverage cannot be re-established, keep the narrow
	// SID set rather than widening the write jail.
	//
	// KNOWN LIMITATION (TOCTOU): this scan-then-broaden sequence cannot be made
	// fully atomic at this layer. Another process could create a new
	// Users/AuthUsers-writable child under a shared root in the gap between
	// windowsEnsureSharedDescendantCoverage returning clean and the token
	// actually being created below; that child would carry no compensating
	// deny, yet the freshly broadened token could still write it. Closing this
	// completely would need either a filesystem-level guarantee (e.g. a
	// minifilter or a lock held across the whole window) or re-checking the
	// exact same live directories the kernel itself would consult during the
	// access check, which this process cannot do atomically with token
	// creation from user mode. What IS done: everything unrelated to the scan
	// result (SID resolution, capability-file I/O) is resolved BEFORE the
	// scan, specifically so the scan runs as the LAST thing before
	// createWindowsRestrictedTokenForCapabilitySIDs, keeping the window to the
	// minimum a few Go statements and one syscall can achieve — not zero. This
	// is accepted as a narrow, disclosed residual risk consistent with the
	// rest of this function's documented tradeoffs (Schannel, MSYS2 above):
	// the realistic window is a hostile local process winning a race measured
	// in microseconds against a command that was already about to run inside
	// the write jail, not a passive gap an attacker can wait out.
	broadenReadSIDs := config.SandboxLevel == WindowsSandboxLevelRestrictedToken && !writeRestricted &&
		windowsSystemDriveIsOnlyFixedVolume()
	if broadenReadSIDs {
		// The shared-directory DenyWrite mitigation names the one stable
		// read-only capability SID rather than the per-workspace SIDs (see
		// BuildWindowsACLPlan), so every broadened token must carry it for
		// those deny ACEs to bind. Resolved BEFORE the coverage scan (rather
		// than after, as a prior revision did) so this file I/O cannot widen
		// the TOCTOU window documented above: the scan below is the last thing
		// that happens before token creation.
		caps, err := LoadOrCreateWindowsCapabilitySIDs(config.SandboxHome)
		if err != nil {
			fmt.Fprintln(stderr, WindowsSandboxCommandRunnerName+": "+err.Error())
			return 1
		}
		if err := windowsEnsureSharedDescendantCoverage(config); err != nil {
			fmt.Fprintf(stderr, "%s: shared descendant write coverage incomplete (%v); keeping narrow restricting SIDs\n",
				WindowsSandboxCommandRunnerName, err)
			broadenReadSIDs = false
		} else {
			tokenSIDs = append(tokenSIDs, caps.ReadOnly)
		}
	}
	token, err := createWindowsRestrictedTokenForCapabilitySIDs(tokenSIDs, writeRestricted, broadenReadSIDs)
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
		return fmt.Errorf("apply unelevated workspace ACLs: %w — the workspace may be on a filesystem the current user does not own; "+
			"run `zero sandbox setup` from an elevated (Administrator) terminal, or re-run with `--sandbox forbid` to skip OS sandboxing", err)
	}
	return recordWindowsUnelevatedAppliedPlan(config.SandboxHome, applied)
}
