package background

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusError     Status = "error"
	StatusKilled    Status = "killed"
)

type Task struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	SpecialistName string    `json:"specialistName,omitempty"`
	Description    string    `json:"description,omitempty"`
	ParentID       string    `json:"parentId,omitempty"`
	PID            int       `json:"pid,omitempty"`
	Status         Status    `json:"status"`
	OutputFile     string    `json:"outputFile"`
	StartedAt      time.Time `json:"startedAt"`
	CompletedAt    time.Time `json:"completedAt,omitempty"`
	ExitCode       int       `json:"exitCode,omitempty"`
}

type RegisterInput struct {
	TaskID         string
	Type           string
	SpecialistName string
	Description    string
	ParentID       string
	PID            int
	OutputFile     string
}

type ManagerOptions struct {
	RootDir     string
	Env         map[string]string
	Now         func() time.Time
	KillProcess func(pid int) error
}

type Manager struct {
	mu          sync.Mutex
	tasks       map[string]Task
	rootDir     string
	now         func() time.Time
	killProcess func(pid int) error
}

var taskIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func NewManager(rootDir string) (*Manager, error) {
	return NewManagerWithOptions(ManagerOptions{RootDir: rootDir})
}

func NewManagerWithOptions(options ManagerOptions) (*Manager, error) {
	rootDir := strings.TrimSpace(options.RootDir)
	if rootDir == "" {
		rootDir = DefaultRoot(options.Env)
	}
	rootDir = filepath.Clean(rootDir)
	if err := os.MkdirAll(rootDir, 0o700); err != nil {
		return nil, fmt.Errorf("create background task directory: %w", err)
	}

	now := options.Now
	if now == nil {
		now = time.Now
	}
	killProcess := options.KillProcess
	if killProcess == nil {
		killProcess = terminateProcess
	}
	return &Manager{tasks: map[string]Task{}, rootDir: rootDir, now: now, killProcess: killProcess}, nil
}

func DefaultRoot(env map[string]string) string {
	dataHome := strings.TrimSpace(envValue(env, "XDG_DATA_HOME"))
	home := strings.TrimSpace(envValue(env, "HOME"))
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = userHome
		}
	}
	base := dataHome
	if base == "" {
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "zero", "background")
}

func (manager *Manager) RootDir() string {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.rootDir
}

func (manager *Manager) Register(input RegisterInput) (string, error) {
	taskID := strings.TrimSpace(input.TaskID)
	if !validTaskID(taskID) {
		return "", fmt.Errorf("invalid background task id %q", input.TaskID)
	}
	taskType := strings.TrimSpace(input.Type)
	if taskType == "" {
		return "", fmt.Errorf("background task %s requires a type", taskID)
	}
	if input.PID < 0 {
		return "", fmt.Errorf("invalid background task pid %d", input.PID)
	}
	outputFile, err := manager.outputFile(taskID, input.OutputFile)
	if err != nil {
		return "", err
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if _, exists := manager.tasks[taskID]; exists {
		return "", fmt.Errorf("background task already registered: %s", taskID)
	}
	if err := os.MkdirAll(filepath.Dir(outputFile), 0o700); err != nil {
		return "", fmt.Errorf("create background task output directory: %w", err)
	}
	file, err := os.OpenFile(outputFile, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("background task output already exists: %s", outputFile)
		}
		return "", fmt.Errorf("create background task output file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close background task output file: %w", err)
	}

	manager.tasks[taskID] = Task{
		ID:             taskID,
		Type:           taskType,
		SpecialistName: strings.TrimSpace(input.SpecialistName),
		Description:    strings.TrimSpace(input.Description),
		ParentID:       strings.TrimSpace(input.ParentID),
		PID:            input.PID,
		Status:         StatusRunning,
		OutputFile:     outputFile,
		StartedAt:      manager.now(),
	}
	return outputFile, nil
}

func (manager *Manager) SetPID(taskID string, pid int) error {
	taskID = strings.TrimSpace(taskID)
	if pid <= 0 {
		return fmt.Errorf("invalid background task pid %d", pid)
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	task, ok := manager.tasks[taskID]
	if !ok {
		return fmt.Errorf("background task not found: %s", taskID)
	}
	task.PID = pid
	manager.tasks[taskID] = task
	return nil
}

func (manager *Manager) UpdateStatus(taskID string, status Status, exitCode int) error {
	taskID = strings.TrimSpace(taskID)
	if !validStatus(status) {
		return fmt.Errorf("invalid background task status %q", status)
	}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	task, ok := manager.tasks[taskID]
	if !ok {
		return fmt.Errorf("background task not found: %s", taskID)
	}
	task.Status = status
	task.ExitCode = exitCode
	if status == StatusRunning {
		task.CompletedAt = time.Time{}
	} else if task.CompletedAt.IsZero() {
		task.CompletedAt = manager.now()
	}
	manager.tasks[taskID] = task
	return nil
}

func (manager *Manager) Get(taskID string) (Task, bool) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	task, ok := manager.tasks[strings.TrimSpace(taskID)]
	return task, ok
}

func (manager *Manager) Kill(taskID string) error {
	taskID = strings.TrimSpace(taskID)

	pid, err := manager.killTarget(taskID)
	if err != nil {
		return err
	}
	if !manager.isRunningPID(taskID, pid) {
		return nil
	}
	if err := manager.killProcess(pid); err != nil {
		return fmt.Errorf("kill background task %s: %w", taskID, err)
	}
	return manager.markKilledIfStillRunning(taskID, pid)
}

func (manager *Manager) killTarget(taskID string) (int, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	task, ok := manager.tasks[taskID]
	if !ok {
		return 0, fmt.Errorf("background task not found: %s", taskID)
	}
	if task.Status != StatusRunning {
		return 0, fmt.Errorf("background task %s is %s", taskID, task.Status)
	}
	if task.PID <= 0 {
		return 0, fmt.Errorf("background task %s has no pid", taskID)
	}
	return task.PID, nil
}

func (manager *Manager) isRunningPID(taskID string, pid int) bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	task, ok := manager.tasks[taskID]
	return ok && task.Status == StatusRunning && task.PID == pid
}

func (manager *Manager) markKilledIfStillRunning(taskID string, pid int) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	task, ok := manager.tasks[taskID]
	if !ok {
		return fmt.Errorf("background task not found: %s", taskID)
	}
	if task.Status != StatusRunning {
		return nil
	}
	if task.PID != pid {
		return fmt.Errorf("background task %s pid changed before kill completed", taskID)
	}
	task.Status = StatusKilled
	task.ExitCode = -1
	if task.CompletedAt.IsZero() {
		task.CompletedAt = manager.now()
	}
	manager.tasks[taskID] = task
	return nil
}

func (manager *Manager) OutputPath(taskID string) string {
	task, ok := manager.Get(taskID)
	if !ok {
		return ""
	}
	return task.OutputFile
}

func (manager *Manager) ListByParent(parentID string) []Task {
	parentID = strings.TrimSpace(parentID)
	manager.mu.Lock()
	defer manager.mu.Unlock()
	tasks := []Task{}
	for _, task := range manager.tasks {
		if task.ParentID == parentID {
			tasks = append(tasks, task)
		}
	}
	sortTasks(tasks)
	return tasks
}

func (manager *Manager) List() []Task {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	tasks := make([]Task, 0, len(manager.tasks))
	for _, task := range manager.tasks {
		tasks = append(tasks, task)
	}
	sortTasks(tasks)
	return tasks
}

func (manager *Manager) outputFile(taskID string, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return filepath.Join(manager.rootDir, taskID+".ndjson"), nil
	}
	path := requested
	if !filepath.IsAbs(path) {
		path = filepath.Join(manager.rootDir, path)
	}
	path = filepath.Clean(path)
	rel, err := filepath.Rel(manager.rootDir, path)
	if err != nil {
		return "", fmt.Errorf("resolve background task output file: %w", err)
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", fmt.Errorf("background task output file must be inside %s", manager.rootDir)
	}
	return path, nil
}

func validTaskID(taskID string) bool {
	return taskIDPattern.MatchString(strings.TrimSpace(taskID))
}

func validStatus(status Status) bool {
	switch status {
	case StatusRunning, StatusCompleted, StatusError, StatusKilled:
		return true
	default:
		return false
	}
}

func sortTasks(tasks []Task) {
	sort.SliceStable(tasks, func(left int, right int) bool {
		if tasks[left].StartedAt.Equal(tasks[right].StartedAt) {
			return tasks[left].ID < tasks[right].ID
		}
		return tasks[left].StartedAt.After(tasks[right].StartedAt)
	})
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}
