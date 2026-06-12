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
	return newHookDispatcherWithExtra(workspaceRoot, nil)
}

// newHookDispatcherWithExtra builds the dispatcher like newHookDispatcher but also
// folds plugin-activated hook definitions into the active hook set, so a plugin's
// declared hooks run alongside the user/project hooks.json hooks. Plugin hooks are
// appended after the configured hooks; their ids are plugin-namespaced (plugin
// id + hook name) so they never collide with hooks.json ids. A nil/empty extra
// slice is byte-equivalent to newHookDispatcher.
func newHookDispatcherWithExtra(workspaceRoot string, extra []hooks.Definition) *hooks.Dispatcher {
	loaded, err := hooks.LoadConfig(hooks.LoadOptions{Cwd: workspaceRoot})
	if err != nil {
		return nil
	}
	var audit *hooks.AuditStore
	if store, err := hooks.NewAuditStore(hooks.AuditStoreOptions{}); err == nil {
		audit = store
	}
	config := loaded.Config
	if len(extra) > 0 {
		// Plugin hooks only run when hooks are enabled overall; an explicit
		// `enabled:false` in hooks.json still disables the whole hook surface.
		merged := append([]hooks.Definition{}, config.Hooks...)
		existing := make(map[string]bool, len(merged))
		for _, hook := range merged {
			existing[hook.ID] = true
		}
		for _, hook := range extra {
			// A hooks.json hook with the same (namespaced) id wins, so an operator can
			// still disable a plugin hook by id without the plugin re-enabling it.
			if existing[hook.ID] {
				continue
			}
			merged = append(merged, hook)
		}
		config.Hooks = merged
	}
	return hooks.NewDispatcher(hooks.DispatcherOptions{
		Config: config,
		Audit:  audit,
		Cwd:    workspaceRoot,
	})
}
