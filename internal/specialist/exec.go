package specialist

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/background"
	"github.com/Gitlawb/zero/internal/sessions"
	"github.com/Gitlawb/zero/internal/streamjson"
	"github.com/Gitlawb/zero/internal/tools"
)

const (
	sessionTagSpecialist     = "specialist"
	promptFileThresholdBytes = 4 * 1024
)

const SessionTagSpecialist = sessionTagSpecialist

type NewSessionIDFunc func() (string, error)
type WritePromptFileFunc func(prompt string) (string, error)
type LoadFunc func(LoadOptions) (LoadResult, error)
type RunChildFunc func(ctx context.Context, binaryPath string, args []string) (ChildRunResult, error)
type LaunchBackgroundFunc func(binaryPath string, args []string, outputFile string, onExit func(exitCode int)) (int, error)
type BackgroundManagerFunc func() (*background.Manager, error)

type Executor struct {
	NewSessionID          NewSessionIDFunc
	WritePromptFile       WritePromptFileFunc
	PromptFileMaxSize     int
	Load                  LoadFunc
	RunChild              RunChildFunc
	LaunchBackground      LaunchBackgroundFunc
	BinaryPath            string
	Paths                 Paths
	SessionStore          *sessions.Store
	BackgroundManager     *background.Manager
	BackgroundManagerFunc BackgroundManagerFunc
	BackgroundRuntime     *Runtime
}

type BuildArgsInput struct {
	Manifest              Manifest
	Prompt                string
	ParentSessionID       string
	ParentToolUseID       string
	ParentModel           string
	ParentReasoningEffort string
	CurrentDepth          int
	Description           string
	Cwd                   string
	// PermissionMode is the parent's resolved permission mode. Empty preserves
	// the historical "--auto high" (the Task tool omits it); a caller that
	// passes a non-unsafe mode (e.g. the swarm propagating a non-unsafe
	// orchestrator) gets a non-unsafe child so authority is never widened.
	PermissionMode string
}

type BuildResumeArgsInput struct {
	SessionID      string
	Prompt         string
	CurrentDepth   int
	Manifest       Manifest
	Cwd            string
	PermissionMode string
}

type BuildArgsResult struct {
	Args      []string
	SessionID string
	// PromptFile is created for large prompts; callers own cleanup after exec finishes.
	PromptFile string
}

type TaskParameters struct {
	Name            string
	Prompt          string
	Description     string
	RunInBackground bool
	Resume          string
}

type TaskRunOptions struct {
	ToolCallID            string
	ParentSessionID       string
	ParentModel           string
	ParentReasoningEffort string
	CurrentDepth          int
	Cwd                   string
	// PermissionMode propagates the parent's resolved permission mode to the
	// child. Empty keeps the historical "--auto high"; a non-unsafe value runs
	// the child non-unsafe so it never gains more authority than the parent.
	PermissionMode string
}

// specialistAutonomy maps a parent permission mode to the child's "--auto"
// level. A headless specialist child cannot answer interactive prompts, so it
// runs autonomously — but only at "high" (unsafe) when the parent is itself
// unsafe (or when no mode is provided, preserving the Task tool's behavior).
// Any other parent mode yields the non-unsafe "low" level, so the child's
// authority never exceeds the parent's.
func specialistAutonomy(permissionMode string) string {
	switch strings.TrimSpace(permissionMode) {
	case "", string(permissionModeUnsafe):
		return "high"
	default:
		return "low"
	}
}

// permissionModeUnsafe mirrors agent.PermissionModeUnsafe without importing the
// agent package (which would create an import cycle): exec resolves "--auto high"
// to this mode.
const permissionModeUnsafe = "unsafe"

type ExecResult struct {
	Result    tools.Result
	SessionID string
}

type ChildRunResult struct {
	Events   []streamjson.Event
	Stderr   string
	ExitCode int
	Started  bool
}

func (executor Executor) Run(ctx context.Context, params TaskParameters, options TaskRunOptions) (ExecResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if options.CurrentDepth < 0 {
		return ExecResult{}, fmt.Errorf("current depth cannot be negative")
	}
	if strings.TrimSpace(params.Prompt) == "" {
		return ExecResult{}, fmt.Errorf("specialist prompt is required")
	}
	if strings.TrimSpace(params.Resume) != "" {
		if params.RunInBackground {
			return ExecResult{}, fmt.Errorf("specialist resume cannot run in background")
		}
		return executor.runResume(ctx, params, options)
	}
	return executor.runFresh(ctx, params, options)
}

func (executor Executor) BuildArgs(input BuildArgsInput) (BuildArgsResult, error) {
	if input.CurrentDepth < 0 {
		return BuildArgsResult{}, fmt.Errorf("current depth cannot be negative")
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return BuildArgsResult{}, fmt.Errorf("specialist prompt is required")
	}
	sessionID, err := executor.newSessionID()
	if err != nil {
		return BuildArgsResult{}, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if !sessions.ValidSessionID(sessionID) {
		return BuildArgsResult{}, fmt.Errorf("invalid specialist session id %q", sessionID)
	}
	wrappedPrompt := WrapSystemPrompt(input.Manifest.Metadata.Name, input.Manifest.SystemPrompt, input.Prompt, input.Description)
	promptArgs, promptFile, err := executor.buildPromptArgs(wrappedPrompt)
	if err != nil {
		return BuildArgsResult{}, err
	}

	args := []string{"exec", "--init-session-id", sessionID}
	args = append(args, promptArgs...)
	args = appendModelArgs(args, input.Manifest, input.ParentModel, input.ParentReasoningEffort)
	args = append(args, "--auto", specialistAutonomy(input.PermissionMode), "--output-format", "stream-json")
	toolAllowlist, err := resolvedToolAllowlist(input.Manifest)
	if err != nil {
		return BuildArgsResult{}, err
	}
	if len(toolAllowlist) == 0 {
		return BuildArgsResult{}, fmt.Errorf("specialist %q resolved no enabled tools", input.Manifest.Metadata.Name)
	}
	args = append(args, "--enabled-tools", strings.Join(toolAllowlist, ","))
	args = append(args, "--depth", strconv.Itoa(input.CurrentDepth+1), "--tag", sessionTagSpecialist)
	if parentSessionID := strings.TrimSpace(input.ParentSessionID); parentSessionID != "" {
		args = append(args, "--calling-session-id", parentSessionID)
	}
	if parentToolUseID := strings.TrimSpace(input.ParentToolUseID); parentToolUseID != "" {
		args = append(args, "--calling-tool-use-id", parentToolUseID)
	}
	// Always record the specialist name in the session title. AgentName is
	// derived from the title's "name:" prefix, and resume refuses a session whose
	// AgentName is empty — so a description-less run (description is optional)
	// must still carry the name or it can never be resumed.
	name := strings.TrimSpace(input.Manifest.Metadata.Name)
	if description := strings.TrimSpace(input.Description); description != "" {
		args = append(args, "--session-title", name+": "+description)
	} else {
		args = append(args, "--session-title", name)
	}
	if cwd := strings.TrimSpace(input.Cwd); cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	return BuildArgsResult{Args: args, SessionID: sessionID, PromptFile: promptFile}, nil
}

func (executor Executor) BuildResumeArgs(input BuildResumeArgsInput) (BuildArgsResult, error) {
	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		return BuildArgsResult{}, fmt.Errorf("resume session id is required")
	}
	if !sessions.ValidSessionID(sessionID) {
		return BuildArgsResult{}, fmt.Errorf("invalid resume session id %q", sessionID)
	}
	if input.CurrentDepth < 0 {
		return BuildArgsResult{}, fmt.Errorf("current depth cannot be negative")
	}
	if strings.TrimSpace(input.Prompt) == "" {
		return BuildArgsResult{}, fmt.Errorf("specialist prompt is required")
	}
	promptArgs, promptFile, err := executor.buildPromptArgs(WrapResumePrompt(input.Prompt))
	if err != nil {
		return BuildArgsResult{}, err
	}
	args := []string{"exec", "--resume", sessionID}
	args = append(args, promptArgs...)
	args = append(args, "--auto", specialistAutonomy(input.PermissionMode), "--output-format", "stream-json")
	toolAllowlist, err := resolvedToolAllowlist(input.Manifest)
	if err != nil {
		return BuildArgsResult{}, err
	}
	if len(toolAllowlist) == 0 {
		return BuildArgsResult{}, fmt.Errorf("specialist %q resolved no enabled tools", input.Manifest.Metadata.Name)
	}
	args = append(args, "--enabled-tools", strings.Join(toolAllowlist, ","))
	args = append(args, "--depth", strconv.Itoa(input.CurrentDepth+1), "--tag", sessionTagSpecialist)
	if cwd := strings.TrimSpace(input.Cwd); cwd != "" {
		args = append(args, "--cwd", cwd)
	}
	return BuildArgsResult{Args: args, SessionID: sessionID, PromptFile: promptFile}, nil
}

func (executor Executor) runFresh(ctx context.Context, params TaskParameters, options TaskRunOptions) (ExecResult, error) {
	manifest, err := executor.loadManifest(params.Name)
	if err != nil {
		return ExecResult{}, err
	}
	built, err := executor.BuildArgs(BuildArgsInput{
		Manifest:              manifest,
		Prompt:                params.Prompt,
		ParentSessionID:       options.ParentSessionID,
		ParentToolUseID:       options.ToolCallID,
		ParentModel:           options.ParentModel,
		ParentReasoningEffort: options.ParentReasoningEffort,
		CurrentDepth:          options.CurrentDepth,
		Description:           params.Description,
		Cwd:                   options.Cwd,
		PermissionMode:        options.PermissionMode,
	})
	if err != nil {
		return ExecResult{}, err
	}
	if params.RunInBackground {
		return executor.runBackground(ctx, built, manifest, params, options)
	}
	return executor.runBuiltArgs(ctx, built, manifest, params, options, "foreground")
}

func (executor Executor) runResume(ctx context.Context, params TaskParameters, options TaskRunOptions) (ExecResult, error) {
	session, err := executor.resumeSession(params.Resume)
	if err != nil {
		return ExecResult{}, err
	}
	specialistName := strings.TrimSpace(session.AgentName)
	if specialistName == "" {
		return ExecResult{}, fmt.Errorf("resume session %q does not identify a specialist", session.SessionID)
	}
	if requestedName := strings.TrimSpace(params.Name); requestedName != "" && requestedName != specialistName {
		return ExecResult{}, fmt.Errorf("resume session %q belongs to specialist %q, not %q", session.SessionID, specialistName, requestedName)
	}
	manifest, err := executor.loadManifest(specialistName)
	if err != nil {
		return ExecResult{}, err
	}
	built, err := executor.BuildResumeArgs(BuildResumeArgsInput{
		SessionID:      params.Resume,
		Prompt:         params.Prompt,
		CurrentDepth:   options.CurrentDepth,
		Manifest:       manifest,
		Cwd:            options.Cwd,
		PermissionMode: options.PermissionMode,
	})
	if err != nil {
		return ExecResult{}, err
	}
	return executor.runBuiltArgs(ctx, built, manifest, params, options, "resume")
}

func (executor Executor) runBackground(ctx context.Context, built BuildArgsResult, manifest Manifest, params TaskParameters, options TaskRunOptions) (ExecResult, error) {
	if err := ctx.Err(); err != nil {
		if built.PromptFile != "" {
			cleanupPromptFile(built.PromptFile)
		}
		return ExecResult{}, err
	}
	manager, err := executor.backgroundManager()
	if err != nil {
		if built.PromptFile != "" {
			cleanupPromptFile(built.PromptFile)
		}
		return ExecResult{}, err
	}
	if err := ctx.Err(); err != nil {
		if built.PromptFile != "" {
			cleanupPromptFile(built.PromptFile)
		}
		return ExecResult{}, err
	}
	outputFile, err := manager.Register(background.RegisterInput{
		TaskID:         built.SessionID,
		Type:           "specialist",
		SpecialistName: manifest.Metadata.Name,
		Description:    params.Description,
		ParentID:       options.ParentSessionID,
	})
	if err != nil {
		if built.PromptFile != "" {
			cleanupPromptFile(built.PromptFile)
		}
		return ExecResult{}, err
	}
	binaryPath, err := executor.binaryPath()
	if err != nil {
		_ = manager.UpdateStatus(built.SessionID, background.StatusError, -1)
		if built.PromptFile != "" {
			cleanupPromptFile(built.PromptFile)
		}
		return ExecResult{}, err
	}
	if err := ctx.Err(); err != nil {
		_ = manager.UpdateStatus(built.SessionID, background.StatusError, -1)
		if built.PromptFile != "" {
			cleanupPromptFile(built.PromptFile)
		}
		return ExecResult{}, err
	}
	executor.trackBackgroundPromptFile(built.SessionID, built.PromptFile)

	accounting := specialistAccountingInput{
		ParentSessionID: options.ParentSessionID,
		ChildSessionID:  built.SessionID,
		SpecialistName:  manifest.Metadata.Name,
		Description:     params.Description,
		ToolCallID:      options.ToolCallID,
		Mode:            "background",
		Background:      true,
	}
	executor.recordSpecialistStart(accounting)
	pid, err := executor.launchBackground(binaryPath, built.Args, outputFile, func(exitCode int) {
		status := background.StatusCompleted
		if exitCode != 0 {
			status = background.StatusError
		}
		_ = manager.MarkExited(built.SessionID, status, exitCode)
		if task, ok := manager.Get(built.SessionID); ok {
			summary := StreamResult{ExitCode: task.ExitCode}
			if data, err := os.ReadFile(task.OutputFile); err == nil {
				summary, _ = summarizeTaskData(string(data), task.ExitCode)
			}
			executor.recordBackgroundTaskAccounting(task, summary)
		}
		executor.cleanupBackgroundPromptFile(built.SessionID, built.PromptFile)
	})
	if err != nil {
		_ = manager.UpdateStatus(built.SessionID, background.StatusError, -1)
		executor.cleanupBackgroundPromptFile(built.SessionID, built.PromptFile)
		executor.recordSpecialistStop(accounting, StreamResult{ExitCode: -1}, "error", -1, err, false)
		return ExecResult{}, err
	}
	if pid > 0 {
		if err := manager.SetPID(built.SessionID, pid); err != nil {
			// The child is running but its PID was never recorded, so nothing can
			// track or stop it later. Kill it (and its process group) now, mark the
			// task errored, and record the stop so it cannot become an untracked
			// orphan. The dedup in recordSpecialistStop makes the later onExit
			// accounting a no-op.
			if killErr := background.TerminateProcess(pid); killErr != nil {
				// Don't mask a failed kill: a surviving orphan is worse than the
				// SetPID error alone, so surface both to the caller.
				err = errors.Join(err, fmt.Errorf("terminate orphaned pid %d: %w", pid, killErr))
			}
			_ = manager.UpdateStatus(built.SessionID, background.StatusError, -1)
			executor.cleanupBackgroundPromptFile(built.SessionID, built.PromptFile)
			executor.recordSpecialistStop(accounting, StreamResult{ExitCode: -1}, "error", -1, err, false)
			return ExecResult{}, err
		}
	}

	output := fmt.Sprintf(`Task launched in background.
task_id: %s
pid: %d
specialist: %s
description: %s

Use TaskOutput with task_id "%s" to check progress.
Use TaskStop with task_id "%s" to stop it.`,
		built.SessionID,
		pid,
		manifest.Metadata.Name,
		strings.TrimSpace(params.Description),
		built.SessionID,
		built.SessionID,
	)
	return ExecResult{
		Result: tools.Result{
			Status: tools.StatusOK,
			Output: strings.TrimSpace(output),
			Meta: map[string]string{
				"task_id":    built.SessionID,
				"session_id": built.SessionID,
			},
		},
		SessionID: built.SessionID,
	}, nil
}

func (executor Executor) resumeSession(sessionID string) (*sessions.Metadata, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, fmt.Errorf("resume session id is required")
	}
	if !sessions.ValidSessionID(sessionID) {
		return nil, fmt.Errorf("invalid resume session id %q", sessionID)
	}
	store := executor.SessionStore
	if store == nil {
		store = sessions.NewStore(sessions.StoreOptions{})
	}
	session, err := store.Get(sessionID)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, fmt.Errorf("resume session not found: %s", sessionID)
	}
	if session.SessionKind != sessions.SessionKindChild || strings.TrimSpace(session.Tag) != sessionTagSpecialist {
		return nil, fmt.Errorf("resume session %q is not a specialist child session", sessionID)
	}
	return session, nil
}

func (executor Executor) runBuiltArgs(ctx context.Context, built BuildArgsResult, manifest Manifest, params TaskParameters, options TaskRunOptions, mode string) (ExecResult, error) {
	if built.PromptFile != "" {
		defer cleanupPromptFile(built.PromptFile)
	}
	binaryPath, err := executor.binaryPath()
	if err != nil {
		return ExecResult{}, err
	}
	accounting := specialistAccountingInput{
		ParentSessionID: options.ParentSessionID,
		ChildSessionID:  built.SessionID,
		SpecialistName:  manifest.Metadata.Name,
		Description:     params.Description,
		ToolCallID:      options.ToolCallID,
		Mode:            mode,
		Background:      false,
	}
	executor.recordSpecialistStart(accounting)
	run, err := executor.runChild(ctx, binaryPath, built.Args)
	if err != nil {
		exitCode := run.exitCodeOr(-1)
		summary := SummarizeStream(run.Events, exitCode)
		executor.recordSpecialistStop(accounting, summary, "error", summary.ExitCode, err, false)
		return ExecResult{}, err
	}
	summary := SummarizeStream(run.Events, run.ExitCode)
	rolledUp := executor.rollUpSpecialistUsage(accounting, summary)
	executor.recordSpecialistStop(accounting, summary, summary.Status, summary.ExitCode, nil, rolledUp)
	return ExecResult{
		Result:    BuildFinalResult(run.Events, run.Stderr, run.ExitCode),
		SessionID: built.SessionID,
	}, nil
}

func (executor Executor) loadManifest(name string) (Manifest, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Manifest{}, fmt.Errorf("specialist name is required")
	}
	load := executor.Load
	if load == nil {
		load = Load
	}
	result, err := load(LoadOptions{Paths: executor.Paths})
	if err != nil {
		return Manifest{}, err
	}
	manifest, ok := Find(result, name)
	if !ok {
		return Manifest{}, fmt.Errorf("specialist %q not found", name)
	}
	return manifest, nil
}

func (executor Executor) binaryPath() (string, error) {
	if path := strings.TrimSpace(executor.BinaryPath); path != "" {
		return path, nil
	}
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve zero executable: %w", err)
	}
	return path, nil
}

func (executor Executor) runChild(ctx context.Context, binaryPath string, args []string) (ChildRunResult, error) {
	if executor.RunChild != nil {
		return executor.RunChild(ctx, binaryPath, append([]string(nil), args...))
	}
	return runChildProcess(ctx, binaryPath, args)
}

func (executor Executor) launchBackground(binaryPath string, args []string, outputFile string, onExit func(exitCode int)) (int, error) {
	if executor.LaunchBackground != nil {
		return executor.LaunchBackground(binaryPath, append([]string(nil), args...), outputFile, onExit)
	}
	return launchBackgroundProcess(binaryPath, args, outputFile, onExit)
}

func (executor Executor) backgroundManager() (*background.Manager, error) {
	if executor.BackgroundRuntime != nil {
		return executor.BackgroundRuntime.Manager()
	}
	if executor.BackgroundManager != nil {
		return executor.BackgroundManager, nil
	}
	if executor.BackgroundManagerFunc != nil {
		return executor.BackgroundManagerFunc()
	}
	return background.NewManager("")
}

func (executor Executor) trackBackgroundPromptFile(taskID string, promptFile string) {
	if promptFile == "" {
		return
	}
	if executor.BackgroundRuntime != nil {
		executor.BackgroundRuntime.TrackPromptFile(taskID, promptFile)
	}
}

func (executor Executor) cleanupBackgroundPromptFile(taskID string, promptFile string) {
	if promptFile == "" {
		return
	}
	if executor.BackgroundRuntime != nil {
		executor.BackgroundRuntime.UntrackPromptFile(taskID)
		return
	}
	cleanupPromptFile(promptFile)
}

func appendModelArgs(args []string, manifest Manifest, parentModel string, parentReasoningEffort string) []string {
	resolvedModel := strings.TrimSpace(manifest.Metadata.Model)
	if resolvedModel == "" {
		resolvedModel = strings.TrimSpace(parentModel)
	}
	if resolvedModel != "" {
		args = append(args, "--model", resolvedModel)
	}

	reasoningEffort := strings.TrimSpace(manifest.Metadata.ReasoningEffort)
	if reasoningEffort == "" && strings.TrimSpace(manifest.Metadata.Model) == "" {
		reasoningEffort = strings.TrimSpace(parentReasoningEffort)
	}
	if reasoningEffort != "" {
		args = append(args, "--reasoning-effort", reasoningEffort)
	}
	return args
}

func resolvedToolAllowlist(manifest Manifest) ([]string, error) {
	if len(manifest.ResolvedTools) > 0 {
		return append([]string(nil), manifest.ResolvedTools...), nil
	}
	return ResolveTools(manifest.Metadata.Tools)
}

func (executor Executor) buildPromptArgs(prompt string) ([]string, string, error) {
	threshold := executor.PromptFileMaxSize
	if threshold <= 0 {
		threshold = promptFileThresholdBytes
	}
	if len([]byte(prompt)) <= threshold {
		return []string{prompt}, "", nil
	}
	path, err := executor.writePromptFile(prompt)
	if err != nil {
		return nil, "", err
	}
	return []string{"--file", path}, path, nil
}

func (executor Executor) newSessionID() (string, error) {
	if executor.NewSessionID != nil {
		return executor.NewSessionID()
	}
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", fmt.Errorf("create specialist session id: %w", err)
	}
	return "specialist_" + hex.EncodeToString(random), nil
}

func (executor Executor) writePromptFile(prompt string) (string, error) {
	if executor.WritePromptFile != nil {
		return executor.WritePromptFile(prompt)
	}
	return writePromptFile(prompt)
}

func writePromptFile(prompt string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "zero-specialist-")
	if err != nil {
		return "", fmt.Errorf("create specialist prompt temp dir: %w", err)
	}
	if err := os.Chmod(tmpDir, 0o700); err != nil {
		return "", fmt.Errorf("secure specialist prompt temp dir: %w", err)
	}
	promptPath := filepath.Join(tmpDir, "prompt.md")
	if err := os.WriteFile(promptPath, []byte(prompt), 0o600); err != nil {
		return "", fmt.Errorf("write specialist prompt file: %w", err)
	}
	return promptPath, nil
}

func cleanupPromptFile(promptFile string) {
	if promptFile == "" {
		return
	}
	dir := filepath.Dir(promptFile)
	if strings.HasPrefix(filepath.Base(dir), "zero-specialist-") {
		_ = os.RemoveAll(dir)
		return
	}
	_ = os.Remove(promptFile)
}

func runChildProcess(ctx context.Context, binaryPath string, args []string) (ChildRunResult, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := osexec.CommandContext(ctx, binaryPath, args...)
	command.Stdout = &stdout
	command.Stderr = &stderr

	exitCode := 0
	started := false
	if err := command.Run(); err != nil {
		var exitErr *osexec.ExitError
		if errors.As(err, &exitErr) {
			started = true
			exitCode = exitErr.ExitCode()
		} else {
			return ChildRunResult{Stderr: stderr.String(), ExitCode: exitCode, Started: started}, fmt.Errorf("run specialist child: %w", err)
		}
	} else {
		started = true
	}
	events, err := ParseStream(bytes.NewReader(stdout.Bytes()))
	if err != nil {
		return ChildRunResult{Stderr: stderr.String(), ExitCode: exitCode, Started: started}, err
	}
	return ChildRunResult{Events: events, Stderr: stderr.String(), ExitCode: exitCode, Started: started}, nil
}

func (run ChildRunResult) exitCodeOr(defaultExitCode int) int {
	if run.Started || run.ExitCode != 0 {
		return run.ExitCode
	}
	return defaultExitCode
}

func launchBackgroundProcess(binaryPath string, args []string, outputFile string, onExit func(exitCode int)) (int, error) {
	file, err := os.OpenFile(outputFile, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open specialist background output: %w", err)
	}
	command := osexec.Command(binaryPath, args...)
	command.Stdout = file
	command.Stderr = file
	command.Stdin = nil
	// Put the child in its own process group so terminating the task later kills
	// the whole group (any grandchildren it forks) instead of orphaning them.
	background.ConfigureChildProcessGroup(command)
	if err := command.Start(); err != nil {
		_ = file.Close()
		return 0, fmt.Errorf("launch specialist background child: %w", err)
	}
	pid := command.Process.Pid
	go func() {
		exitCode := 0
		if err := command.Wait(); err != nil {
			var exitErr *osexec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = 1
			}
		}
		_ = file.Close()
		if onExit != nil {
			onExit(exitCode)
		}
	}()
	return pid, nil
}
