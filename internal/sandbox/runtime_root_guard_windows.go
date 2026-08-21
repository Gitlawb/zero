//go:build windows

package sandbox

import (
	"os"
	"strings"
)

// refuseForeignRuntimeComponent has no ownership check on Windows.
//
// The derived root lives under the per-user cache directory or the per-session
// TEMP, both of which are already user-private, and the elevated setup path
// applies its own capability ACL. The link refusal in the shared guard is the
// part that matters here.
func refuseForeignRuntimeComponent(string, os.FileInfo) error {
	return nil
}

// sandboxRuntimeUserScope names the account the tree belongs to.
//
// Windows TEMP is already per-user, so this is belt and braces rather than the
// load-bearing separation it is on Unix. Kept so the derived path has the same
// shape on every platform and one code path builds it.
func sandboxRuntimeUserScope() string {
	name := strings.TrimSpace(os.Getenv("USERNAME"))
	if name == "" {
		return "u"
	}
	return "u" + strings.ToLower(name)
}
