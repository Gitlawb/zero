package specialist

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gitlawb/zero/internal/sessions"
)

const (
	sessionTagSpecialist     = "specialist"
	promptFileThresholdBytes = 4 * 1024
)

type NewSessionIDFunc func() (string, error)
type WritePromptFileFunc func(prompt string) (string, error)

type Executor struct {
	NewSessionID      NewSessionIDFunc
	WritePromptFile   WritePromptFileFunc
	PromptFileMaxSize int
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
}

type BuildResumeArgsInput struct {
	SessionID    string
	Prompt       string
	CurrentDepth int
}

type BuildArgsResult struct {
	Args      []string
	SessionID string
	// PromptFile is created for large prompts; callers own cleanup after exec finishes.
	PromptFile string
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
	args = append(args, "--auto", "high", "--output-format", "stream-json")
	if len(input.Manifest.ResolvedTools) > 0 {
		args = append(args, "--enabled-tools", strings.Join(input.Manifest.ResolvedTools, ","))
	}
	args = append(args, "--depth", strconv.Itoa(input.CurrentDepth+1), "--tag", sessionTagSpecialist)
	if parentSessionID := strings.TrimSpace(input.ParentSessionID); parentSessionID != "" {
		args = append(args, "--calling-session-id", parentSessionID)
	}
	if parentToolUseID := strings.TrimSpace(input.ParentToolUseID); parentToolUseID != "" {
		args = append(args, "--calling-tool-use-id", parentToolUseID)
	}
	if description := strings.TrimSpace(input.Description); description != "" {
		args = append(args, "--session-title", strings.TrimSpace(input.Manifest.Metadata.Name)+": "+description)
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
	args = append(args, "--auto", "high", "--output-format", "stream-json")
	args = append(args, "--depth", strconv.Itoa(input.CurrentDepth+1), "--tag", sessionTagSpecialist)
	return BuildArgsResult{Args: args, SessionID: sessionID, PromptFile: promptFile}, nil
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
