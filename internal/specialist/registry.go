package specialist

import (
	"sync"

	"github.com/Gitlawb/zero/internal/background"
	"github.com/Gitlawb/zero/internal/tools"
)

func RegisterTools(registry *tools.Registry, executor Executor) error {
	managerFunc := executor.BackgroundManagerFunc
	if executor.BackgroundManager != nil {
		manager := executor.BackgroundManager
		managerFunc = func() (*background.Manager, error) {
			return manager, nil
		}
	}
	if managerFunc == nil {
		managerFunc = lazyBackgroundManager()
	}
	executor.BackgroundManagerFunc = managerFunc
	registry.Register(NewTaskTool(executor))
	registry.Register(newOutputToolWithManagerFunc(managerFunc))
	registry.Register(newStopToolWithManagerFunc(managerFunc))
	return nil
}

func lazyBackgroundManager() BackgroundManagerFunc {
	var mu sync.Mutex
	var manager *background.Manager
	return func() (*background.Manager, error) {
		mu.Lock()
		defer mu.Unlock()
		if manager != nil {
			return manager, nil
		}
		created, err := background.NewManager("")
		if err != nil {
			return nil, err
		}
		manager = created
		return manager, nil
	}
}
