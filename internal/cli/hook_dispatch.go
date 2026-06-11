package cli

import (
	"github.com/Gitlawb/zero/internal/hooks"
)

// newHookDispatcher builds the per-session hooks dispatcher for a workspace,
// merging user + project hooks.json and wiring the audit store. It fails OPEN:
// any load or setup error yields a nil dispatcher, which Dispatch treats as a
// no-op, so a malformed hooks config can never wedge tool execution. With no
// hooks configured the dispatcher selects nothing and runs no commands, so the
// hot path stays free of overhead until a user opts in via hooks.json.
func newHookDispatcher(workspaceRoot string) *hooks.Dispatcher {
	loaded, err := hooks.LoadConfig(hooks.LoadOptions{Cwd: workspaceRoot})
	if err != nil {
		return nil
	}
	var audit *hooks.AuditStore
	if store, err := hooks.NewAuditStore(hooks.AuditStoreOptions{}); err == nil {
		audit = store
	}
	return hooks.NewDispatcher(hooks.DispatcherOptions{
		Config: loaded.Config,
		Audit:  audit,
		Cwd:    workspaceRoot,
	})
}
