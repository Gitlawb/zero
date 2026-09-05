//go:build !windows

package sandbox

import (
	"fmt"
	"os"
)

// runtimeCompensationSwapSeam exists so the shared compensation code compiles
// everywhere. Only the Windows build closes a check-then-mutate window, because
// only there does setup run elevated against a tree an unelevated process can
// rename.
var runtimeCompensationSwapSeam func()

func compensateRuntimeStampBound(root string, identity string, prior []byte, existed bool) error {
	current, ok := runtimeDirIdentity(root)
	if !ok {
		return fmt.Errorf("identify the sandbox runtime root %s for stamp compensation", root)
	}
	if current != identity {
		return fmt.Errorf("sandbox runtime root %s is no longer the directory this setup stamped; "+
			"leaving the replacement untouched, and the original still carries this run's stamp", root)
	}
	if runtimeCompensationSwapSeam != nil {
		runtimeCompensationSwapSeam()
	}
	path := windowsSandboxRuntimeStampPath(root)
	if !existed {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove sandbox runtime setup stamp written by this run: %w", err)
		}
		return nil
	}
	if err := os.WriteFile(path, prior, 0o600); err != nil {
		return fmt.Errorf("restore the previous sandbox runtime setup stamp: %w", err)
	}
	return nil
}

func removeCreatedRuntimeDirBound(path string, identity string) error {
	current, ok := runtimeDirIdentity(path)
	if !ok {
		return nil
	}
	if current != identity {
		return fmt.Errorf("sandbox runtime root %s is no longer the directory this run created; "+
			"leaving the replacement in place", path)
	}
	if runtimeCompensationSwapSeam != nil {
		runtimeCompensationSwapSeam()
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove sandbox runtime root %s created by this run: %w", path, err)
	}
	return nil
}
