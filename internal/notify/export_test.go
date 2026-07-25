// Test seams: helpers only test code uses, kept out of the production binary.
package notify

// Enabled reports whether mode will ever emit a notification.
func Enabled(mode Mode) bool {
	return mode != "" && mode != ModeOff
}
