// Test seams: helpers only test code uses, kept out of the production binary.
package lsp

import "os/exec"

// Available reports whether a configured server for the path exists on PATH.
func Available(path string) bool {
	cmd, ok := ServerFor(path)
	if !ok {
		return false
	}
	_, err := exec.LookPath(cmd[0])
	return err == nil
}
