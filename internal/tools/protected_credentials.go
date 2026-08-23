package tools

import (
	"fmt"
	"os"

	"github.com/Gitlawb/zero/internal/sandbox"
)

// This file is the mandatory, engine-INDEPENDENT half of the daemon-token
// boundary for tools that name a single file directly, rather than walking a
// tree (grep/glob/list_directory use sandboxReadExcluderWithin instead).
//
// Two problems motivate it, not one:
//
//  1. Registry.RunWithOptions only asks the sandbox engine about a protected
//     path when options.Sandbox is non-nil. A caller of the plain registry API
//     — Registry.Run, or RunWithOptions with no engine — bypasses that check
//     entirely, so read_file/read_minified_file/write_file/edit_file/apply_patch
//     had no protection at all on that path. sandbox.ProtectedCredentialExclusions
//     is engine-independent (it reads the protected set from this process's own
//     environment, not from a policy), so it works whether or not an engine was
//     supplied.
//  2. Even WITH an engine, the sandbox's check runs once, before the tool's own
//     Run/RunWithOptions is called, against a resolved pathname — and the tool
//     then opens that path completely independently. A concurrent writer with
//     workspace access can replace an ordinary file with a symlink to the token
//     between those two steps. protectedReadOpen closes that window for reads by
//     deciding from the SAME handle the content is read through, exactly like
//     internal/mcp/resources.go's readResource already does.
//
// Mutation tools (write_file, edit_file) get the pathname-level check only:
// protectedMutationDenied. A fully handle-bound write — open without truncating,
// check identity, THEN truncate and write through that handle — is possible and
// would close the same window for mutations; it is not done here because it
// would mean restructuring how those tools perform their write (currently a
// single os.WriteFile call each) without disturbing their existing staleness/
// conflict-detection logic. The pathname check still closes the P1 gap (no
// protection at all without an engine) and keeps mutations at the SAME
// protection level the engine-present path already had — no regression, and
// still narrower coverage than reads. Left as a known gap; see the PR for
// which finding this corresponds to.

// protectedReadOpen opens path for reading and verifies — from the SAME handle
// any content is read through — that it does not target the automatic daemon
// bridge-token exclusion. Checking a resolved pathname and then opening it
// separately would leave a window where a concurrent writer repoints a symlink
// between the two; deciding from the just-opened handle's own os.FileInfo
// removes that window, because the identity comparison and every subsequent
// read describe the identical object.
func protectedReadOpen(path, workspaceRoot string) (*os.File, os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	exclusions := sandbox.ProtectedCredentialExclusions(workspaceRoot)
	if exclusions.FileExcluded(path, info) {
		file.Close()
		return nil, nil, protectedCredentialErr(path, "readable")
	}
	return file, info, nil
}

// protectedMutationDenied reports whether path names the automatic daemon
// bridge-token exclusion, for a mutation tool to check immediately before it
// writes. Engine-independent — see the file doc for what this does and does
// not close.
func protectedMutationDenied(path, workspaceRoot string) error {
	exclusions := sandbox.ProtectedCredentialExclusions(workspaceRoot)
	if exclusions.PathExcluded(path) {
		return protectedCredentialErr(path, "writable")
	}
	return nil
}

func protectedCredentialErr(path, verb string) error {
	return fmt.Errorf("%s holds the remote bridge token and is never %s", path, verb)
}
