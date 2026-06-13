package background

// TerminateProcess stops a background process by PID — its whole process group on
// POSIX, or its process tree on Windows. Exported for callers that hold a raw PID
// and cannot route through the manager, e.g. cleaning up a just-launched child
// whose PID could not be recorded.
func TerminateProcess(pid int) error {
	return terminateProcess(pid)
}
