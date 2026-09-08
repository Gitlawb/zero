//go:build windows

package sandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Gitlawb/zero/internal/execution"
	"golang.org/x/sys/windows"
)

// windowsExecutionReport is the helper's side channel back to the parent.
//
// The only fact it carries is whether the REQUESTED process was created. The
// parent starts this helper, so the parent's own exec.Cmd.Process proves the
// helper ran and nothing more: setup-marker validation, ACL application,
// network-policy validation, capability and offline SID construction and
// restricted-token creation all happen afterwards and can each return with no
// sandboxed child. Only this process observes the transition, so only this
// process may report it.
//
// OPENED BEFORE THE LAUNCH, ON PURPOSE. Publishing is not free of failure, and
// once CreateProcessAsUser has succeeded a running child exists whether or not
// the report can be written. Acquiring the file first moves every failure that
// can be moved to a point where there is still nothing to own; what remains is
// handled by reaping the child rather than returning while it runs.
type windowsExecutionReport struct {
	file *os.File
	path string
}

// openWindowsExecutionReport claims the report path before anything is launched.
//
// O_EXCL, so a file another local user pre-created at this name makes the helper
// fail here, before any child exists, instead of letting them supply the fact
// the parent reads back. An empty path means the caller wants no report, which
// keeps the standalone helper and every existing test working unchanged.
func openWindowsExecutionReport(path string) (*windowsExecutionReport, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return &windowsExecutionReport{}, nil
	}
	file, err := os.OpenFile(trimmed, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open sandbox execution report: %w", err)
	}
	return &windowsExecutionReport{file: file, path: trimmed}, nil
}

// publish records the launch fact. Safe on a report the caller never opened.
func (report *windowsExecutionReport) publish(childLaunched bool) error {
	if report == nil || report.file == nil {
		return nil
	}
	launched := childLaunched
	if err := json.NewEncoder(report.file).Encode(execution.AdapterReport{ChildLaunched: &launched}); err != nil {
		return fmt.Errorf("write sandbox execution report: %w", err)
	}
	return nil
}

// close releases the handle. Discards the file when nothing was published, so a
// truncated or empty report can never be read back as a launch that happened.
func (report *windowsExecutionReport) close(published bool) {
	if report == nil || report.file == nil {
		return
	}
	closeErr := report.file.Close()
	if !published || closeErr != nil {
		_ = os.Remove(report.path)
	}
	report.file = nil
}

// publishThenResume records the launch and only then lets the child run, and
// unwinds the record if the child cannot run after all.
//
// The report says one thing to the parent: a sandboxed child could execute, and
// enforcement was in force for it. That is what AppliedEnforcementNotices gates
// on, and what ResolveChildLaunched treats as authoritative. It is deliberately
// NOT "CreateProcessAsUser returned a handle": a suspended process that is reaped
// before ResumeThread has executed no instruction and applied nothing, and a
// report saying otherwise would disclose a write-jail trade nobody made.
//
// Publish before resume stays, because it closes the inherited-pipe race the
// caller documents. What this adds is the other half: the returned published
// flag is true only when the child actually resumed, so a resume failure hands
// the deferred close a false and the report is removed rather than left saying
// true about a child that never ran.
func publishThenResume(report *windowsExecutionReport, resume func() error) (published bool, err error) {
	if err := report.publish(true); err != nil {
		return false, fmt.Errorf("record sandboxed child launch: %w", err)
	}
	if err := resume(); err != nil {
		return false, fmt.Errorf("resume sandboxed process: %w", err)
	}
	return true, nil
}

// terminateSuspendedWindowsChild takes down a child that was created suspended
// and never resumed, and waits for it to actually leave.
//
// Used on the paths between CreateProcessAsUser and ResumeThread. The process
// exists and holds the inherited pipes, so it has to be closed out rather than
// abandoned, but it has executed no instructions: there is no work to undo and
// nothing for the parent to be told about.
//
// Two of those paths differ in what the report holds. Before publish, nothing was
// written and "no child launched" is simply what happened. After publish but
// before resume, a report saying true is on disk about a child that never ran;
// publishThenResume returns false there so the caller's deferred close removes
// it. Either way the parent reads absence, and returning an error here is honest.
func terminateSuspendedWindowsChild(process windows.Handle) {
	if process == 0 {
		return
	}
	// The exit code is irrelevant: this path is already returning an error, and
	// the point is that the child is gone before the helper is.
	_ = windows.TerminateProcess(process, 1)
	_, _ = windows.WaitForSingleObject(process, windows.INFINITE)
}
