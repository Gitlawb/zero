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
func Ensure(path string) error {
	root, err := Open(path)
	if err != nil {
		return err
	}
	if err := root.Close(); err != nil {
		return fmt.Errorf("close private directory: %w", err)
	}
	return nil
}

// Open creates path when needed, enforces owner-only access, and returns a
// traversal-resistant handle that callers can retain across subsequent I/O.
func Open(path string) (*os.Root, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve private directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create private directory: %w", err)
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("open private directory: %w", err)
	}
	if err := harden(root); err != nil {
		closeErr := root.Close()
		if closeErr != nil {
			return nil, errors.Join(err, fmt.Errorf("close private directory: %w", closeErr))
		}
		return nil, err
	}
	return root, nil
}
