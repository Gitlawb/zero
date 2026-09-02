//go:build windows

package sandbox

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/Gitlawb/zero/internal/execution"
)

// writeWindowsExecutionReport publishes the helper's structured report to the
// adapter-owned side channel.
//
// The only fact it currently carries is whether the REQUESTED process was
// created. The parent starts this helper, so the parent's own exec.Cmd.Process
// proves the helper ran and nothing more: setup-marker validation, ACL
// application, network-policy validation, capability and offline SID
// construction and restricted-token creation all happen afterwards and can each
// return with no sandboxed child. Only this process observes the transition, so
// only this process may report it.
//
// O_EXCL, so a file another local user pre-created at this name makes the write
// fail instead of letting them supply the fact the parent reads back. An empty
// path means the caller asked for no report, which keeps the standalone helper
// and every existing test working unchanged.
func writeWindowsExecutionReport(path string, childLaunched bool) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	launched := childLaunched
	encodeErr := json.NewEncoder(file).Encode(execution.AdapterReport{ChildLaunched: &launched})
	closeErr := file.Close()
	if encodeErr != nil {
		_ = os.Remove(path)
		return encodeErr
	}
	return closeErr
}
