// Package privatedir creates and safely hardens current-user-owned state
// directories without applying permission changes through a path-only check.
package privatedir

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Ensure creates path when needed and enforces owner-only access. Existing
// directories are hardened only after ownership is verified through a bound
// directory handle; foreign-owned paths fail closed.
func Ensure(path string) (returnErr error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve private directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return fmt.Errorf("create private directory: %w", err)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return fmt.Errorf("open private directory: %w", err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close private directory: %w", err))
		}
	}()
	if err := harden(root); err != nil {
		return err
	}
	return nil
}
