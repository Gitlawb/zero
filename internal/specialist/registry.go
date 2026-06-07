package specialist

import "github.com/Gitlawb/zero/internal/tools"

func RegisterTools(registry *tools.Registry, executor Executor) (*Runtime, error) {
	runtime := executor.BackgroundRuntime
	if runtime == nil {
		runtime = NewRuntime(RuntimeOptions{
			Manager:     executor.BackgroundManager,
			ManagerFunc: executor.BackgroundManagerFunc,
		})
	}
	executor.BackgroundRuntime = runtime
	executor.BackgroundManagerFunc = runtime.Manager
	registry.Register(NewTaskTool(executor))
	registry.Register(newOutputToolWithManagerFunc(runtime.Manager))
	registry.Register(newStopToolWithManagerFunc(runtime.Manager))
	return runtime, nil
}
