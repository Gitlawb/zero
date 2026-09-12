//go:build !unix

package sandbox

// Automatic SSH credential discovery is disabled on Windows.
const sshInspectionNonblock = 0
