package tools

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Gitlawb/zero/internal/pathjail"
	"github.com/Gitlawb/zero/internal/sandbox"
)

// This file is the mandatory, engine-independent half of the daemon-token
// boundary for direct file tools. Registry.Run and RunWithOptions without a
// sandbox engine never reach Engine.Evaluate, so the tools must enforce the
// process-owned credential set themselves.
//
// Reads open through an os.Root tied to the selected workspace/scope root, then
// compare metadata from that same handle with the protected credential set.
// Writes build a complete temporary file under the same root and atomically
// publish it by rename (or an exclusive no-replace operation for creates).
// Consequently neither path resolution nor a later truncating open can be
// redirected to the token between authorization and use.

// protectedReadOpen opens path through the workspace root and verifies the
// identity obtained from that same handle before any content is consumed.
func protectedReadOpen(path, workspaceRoot string) (*os.File, os.FileInfo, error) {
	rootPath, relative, err := rootedPathWithin([]string{workspaceRoot}, path)
	if err != nil {
		// Explicitly granted extra roots and spill files have already passed their
		// own canonical containment checks. Bind their final lookup to the
		// canonical parent rather than falling back to an unrestricted os.Open.
		rootPath = filepath.Dir(path)
		relative = filepath.Base(path)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, nil, err
	}
	file, err := root.Open(relative)
	closeErr := root.Close()
	if err != nil {
		return nil, nil, err
	}
	if closeErr != nil {
		file.Close()
		return nil, nil, closeErr
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

// protectedRootRead is the equivalent for callers that already hold the root,
// notably structured and staged unified patches.
func protectedRootRead(root *os.Root, relative, absolute, workspaceRoot string) (*os.File, os.FileInfo, error) {
	file, err := root.Open(relative)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	exclusions := sandbox.ProtectedCredentialExclusions(workspaceRoot)
	if exclusions.FileExcluded(absolute, info) {
		file.Close()
		return nil, nil, protectedCredentialErr(absolute, "readable")
	}
	return file, info, nil
}

// protectedMutationDenied is the early, lexical refusal. The actual mutation
// must still use writeRootedFile so a raced hard-link or symlink replacement is
// replaced atomically rather than opened and modified.
func protectedMutationDenied(path, workspaceRoot string) error {
	exclusions := sandbox.ProtectedCredentialExclusions(workspaceRoot)
	if exclusions.PathExcluded(path) {
		return protectedCredentialErr(path, "writable")
	}
	return nil
}

func protectedCredentialsActive(workspaceRoot string) bool {
	exclusions := sandbox.ProtectedCredentialExclusions(workspaceRoot)
	return exclusions.Active()
}

func writeRootedFile(root *os.Root, relative string, content []byte, mode os.FileMode, createOnly bool) (bool, error) {
	parent := filepath.Dir(relative)
	if err := root.MkdirAll(parent, 0o755); err != nil {
		return false, err
	}
	temp, tempName, err := pathjail.CreateTemp(root, parent, "zero-write", filepath.Ext(relative)+".tmp")
	if err != nil {
		return false, err
	}
	defer func() { _ = removeStructuredPatchTemp(root, tempName) }()
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return false, err
	}
	if err := temp.Chmod(mode.Perm()); err != nil {
		_ = temp.Close()
		return false, err
	}
	if err := temp.Close(); err != nil {
		return false, err
	}
	if createOnly {
		return publishStructuredPatchNoReplace(root, tempName, relative, mode)
	}
	if err := root.Rename(tempName, relative); err != nil {
		return false, err
	}
	return true, nil
}

func protectedCredentialErr(path, verb string) error {
	return fmt.Errorf("%s holds the remote bridge token and is never %s", path, verb)
}
