// Package browser opens a URL in the user's default web browser. It is used by
// interactive flows (e.g. the provider setup wizard's OAuth login) that need to
// hand the user off to a browser for authorization.
package browser

import (
	"fmt"
	"os/exec"
	"runtime"
)

// OpenURL launches url in the default browser. It returns once the launcher
// process has started (it does not wait for the browser to close). A failure to
// start the launcher is returned so callers can fall back to printing the URL.
func OpenURL(url string) error {
	name, args := openCommand(runtime.GOOS, url)
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("browser: open url: %w", err)
	}
	// Reap the short-lived launcher (open/xdg-open/rundll32 exit promptly) so it
	// does not linger as a zombie; the browser window itself is independent.
	go func() { _ = cmd.Wait() }()
	return nil
}

// openCommand returns the launcher command + args for a platform. Split out as a
// pure function so the per-OS choice is unit-testable without spawning anything.
func openCommand(goos, url string) (string, []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default: // linux and other unixes
		return "xdg-open", []string{url}
	}
}
