//go:build !windows

package sandbox

import "errors"

// errNoRootedStampWriter marks the platforms with no rooted traversal. The
// runtime stamp is a Windows concept; the code that writes it is shared only so
// its tests run everywhere.
var errNoRootedStampWriter = errors.New("no rooted stamp writer on this platform")

func writeRuntimeStampThroughHandle(string, string) error {
	return errNoRootedStampWriter
}
