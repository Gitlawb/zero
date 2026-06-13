package agenteval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
)

type Suite struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Tasks       []Task `json:"tasks"`
}

type Task struct {
	ID                    string        `json:"id"`
	Name                  string        `json:"name"`
	Description           string        `json:"description,omitempty"`
	Tags                  []string      `json:"tags,omitempty"`
	Difficulty            string        `json:"difficulty,omitempty"`
	Prompt                string        `json:"prompt"`
	WorkspaceFixture      string        `json:"workspaceFixture"`
	VerificationCommands  []Command     `json:"verificationCommands"`
	ExpectedChangedFiles  []string      `json:"expectedChangedFiles"`
	ForbiddenChangedFiles []string      `json:"forbiddenChangedFiles,omitempty"`
	RequiredTraceEvents   []string      `json:"requiredTraceEvents,omitempty"`
	ContextChecks         ContextChecks `json:"contextChecks,omitempty"`
}

type Command struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Command []string `json:"command"`
}

type ValidationError struct {
	Problems []string
}

func (err ValidationError) Error() string {
	if len(err.Problems) == 0 {
		return "agent eval suite is invalid"
	}
	return "agent eval suite is invalid: " + strings.Join(err.Problems, "; ")
}

func LoadSuite(path string) (Suite, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Suite{}, fmt.Errorf("load agent eval suite: %w", err)
	}
	var suite Suite
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suite); err != nil {
		return Suite{}, fmt.Errorf("parse agent eval suite: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return Suite{}, fmt.Errorf("parse agent eval suite: trailing JSON after suite")
	} else if !errors.Is(err, io.EOF) {
		return Suite{}, fmt.Errorf("parse agent eval suite: trailing JSON after suite: %w", err)
	}
	if err := suite.Validate(); err != nil {
		return Suite{}, err
	}
	suite.normalize()
	return suite, nil
}

func (suite Suite) Validate() error {
	problems := []string{}
	if blank(suite.ID) {
		problems = append(problems, "suite id is required")
	}
	if blank(suite.Name) {
		problems = append(problems, "suite name is required")
	}
	if len(suite.Tasks) == 0 {
		problems = append(problems, "tasks must not be empty")
	}
	taskIndexes := map[string]int{}
	for taskIndex, task := range suite.Tasks {
		taskPath := fmt.Sprintf("tasks[%d]", taskIndex)
		if blank(task.ID) {
			problems = append(problems, taskPath+" id is required")
		} else if previous, ok := taskIndexes[task.ID]; ok {
			problems = append(problems, fmt.Sprintf("%s id duplicates tasks[%d]", taskPath, previous))
		} else {
			taskIndexes[task.ID] = taskIndex
		}
		if blank(task.Name) {
			problems = append(problems, taskPath+" name is required")
		}
		if blank(task.Prompt) {
			problems = append(problems, taskPath+" prompt is required")
		}
		if blank(task.WorkspaceFixture) {
			problems = append(problems, taskPath+" workspaceFixture is required")
		}
		if len(task.VerificationCommands) == 0 {
			problems = append(problems, taskPath+" verificationCommands must not be empty")
		}
		problems = append(problems, validateFileList(taskPath, "expectedChangedFiles", task.ExpectedChangedFiles, true)...)
		problems = append(problems, validateFileList(taskPath, "forbiddenChangedFiles", task.ForbiddenChangedFiles, false)...)
		problems = append(problems, validateFileList(taskPath, "contextChecks.requiredFiles", task.ContextChecks.RequiredFiles, false)...)
		problems = append(problems, validateFileList(taskPath, "contextChecks.forbiddenFiles", task.ContextChecks.ForbiddenFiles, false)...)
		problems = append(problems, validateStringList(taskPath, "requiredTraceEvents", task.RequiredTraceEvents)...)
		commandIndexes := map[string]int{}
		for commandIndex, command := range task.VerificationCommands {
			commandPath := fmt.Sprintf("%s verificationCommands[%d]", taskPath, commandIndex)
			if blank(command.ID) {
				problems = append(problems, commandPath+" id is required")
			} else if previous, ok := commandIndexes[command.ID]; ok {
				problems = append(problems, fmt.Sprintf("%s id duplicates verificationCommands[%d]", commandPath, previous))
			} else {
				commandIndexes[command.ID] = commandIndex
			}
			if blank(command.Name) {
				problems = append(problems, commandPath+" name is required")
			}
			if emptyCommand(command.Command) {
				problems = append(problems, commandPath+" command must not be empty")
			}
		}
	}
	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

func (suite *Suite) normalize() {
	for taskIndex := range suite.Tasks {
		suite.Tasks[taskIndex].ExpectedChangedFiles = normalizeFiles(suite.Tasks[taskIndex].ExpectedChangedFiles)
		suite.Tasks[taskIndex].ForbiddenChangedFiles = normalizeFiles(suite.Tasks[taskIndex].ForbiddenChangedFiles)
		suite.Tasks[taskIndex].RequiredTraceEvents = normalizeStrings(suite.Tasks[taskIndex].RequiredTraceEvents)
		suite.Tasks[taskIndex].ContextChecks.RequiredFiles = normalizeFiles(suite.Tasks[taskIndex].ContextChecks.RequiredFiles)
		suite.Tasks[taskIndex].ContextChecks.ForbiddenFiles = normalizeFiles(suite.Tasks[taskIndex].ContextChecks.ForbiddenFiles)
	}
}

func normalizeFiles(files []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(files))
	for _, file := range files {
		file, ok := normalizeEvalPath(file)
		if !ok || file == "" || seen[file] {
			continue
		}
		seen[file] = true
		normalized = append(normalized, file)
	}
	sort.Strings(normalized)
	return normalized
}

func validateFileList(taskPath string, field string, files []string, required bool) []string {
	if required && len(files) == 0 {
		return []string{taskPath + " " + field + " must not be empty"}
	}
	problems := []string{}
	seen := map[string]int{}
	for index, file := range files {
		normalized, ok := normalizeEvalPath(file)
		if !ok {
			problems = append(problems, fmt.Sprintf("%s %s[%d] must be a relative workspace path", taskPath, field, index))
			continue
		}
		if normalized == "" {
			problems = append(problems, fmt.Sprintf("%s %s[%d] must not be empty", taskPath, field, index))
			continue
		}
		if previous, ok := seen[normalized]; ok {
			problems = append(problems, fmt.Sprintf("%s %s[%d] duplicates %s[%d]", taskPath, field, index, field, previous))
			continue
		}
		seen[normalized] = index
	}
	return problems
}

func validateExpectedChangedFiles(taskPath string, files []string) []string {
	return validateFileList(taskPath, "expectedChangedFiles", files, true)
}

func validateStringList(taskPath string, field string, values []string) []string {
	problems := []string{}
	seen := map[string]int{}
	for index, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			problems = append(problems, fmt.Sprintf("%s %s[%d] must not be empty", taskPath, field, index))
			continue
		}
		if previous, ok := seen[value]; ok {
			problems = append(problems, fmt.Sprintf("%s %s[%d] duplicates %s[%d]", taskPath, field, index, field, previous))
			continue
		}
		seen[value] = index
	}
	return problems
}

func normalizeEvalPath(file string) (string, bool) {
	file = strings.TrimSpace(strings.ReplaceAll(file, "\\", "/"))
	if file == "" {
		return "", true
	}
	if strings.HasPrefix(file, "/") || strings.Contains(file, ":") {
		return "", false
	}
	clean := path.Clean(file)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func blank(value string) bool {
	return strings.TrimSpace(value) == ""
}

func normalizeStrings(values []string) []string {
	seen := map[string]bool{}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		normalized = append(normalized, value)
	}
	sort.Strings(normalized)
	return normalized
}

func emptyCommand(command []string) bool {
	if len(command) == 0 {
		return true
	}
	for _, part := range command {
		if !blank(part) {
			return false
		}
	}
	return true
}
