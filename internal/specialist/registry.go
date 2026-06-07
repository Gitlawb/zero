package specialist

import "github.com/Gitlawb/zero/internal/tools"

func RegisterTools(registry *tools.Registry, executor Executor) {
	registry.Register(NewTaskTool(executor))
}
