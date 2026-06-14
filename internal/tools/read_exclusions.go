package tools

import "github.com/Gitlawb/zero/internal/sandbox"

// readExcluder skips read-denied paths (the sandbox DenyRead policy) during a
// search walk. The zero value (both funcs nil) excludes nothing, so a
// non-sandboxed search behaves exactly as before — the exclusions are opt-in and
// only ever REMOVE results, never add them.
type readExcluder struct {
	file func(string) bool
	dir  func(string) bool
}

func (e readExcluder) fileExcluded(path string) bool { return e.file != nil && e.file(path) }
func (e readExcluder) dirExcluded(path string) bool  { return e.dir != nil && e.dir(path) }

// sandboxReadExcluder builds a readExcluder from a sandbox engine's DenyRead
// policy. A nil engine yields the no-op excluder, so the search tools keep their
// pre-sandbox behavior when no sandbox is active.
func sandboxReadExcluder(engine *sandbox.Engine) readExcluder {
	if engine == nil {
		return readExcluder{}
	}
	return readExcluder{file: engine.ReadPathExcluded, dir: engine.ReadDirExcluded}
}
