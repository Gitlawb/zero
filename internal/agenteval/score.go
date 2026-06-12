package agenteval

import (
	"fmt"
	"sort"
)

const ReportContractVersion = "zero.agenteval.report.v1"

type Status string

const (
	StatusPass    Status = "pass"
	StatusFail    Status = "fail"
	StatusBlocked Status = "blocked"
	StatusError   Status = "error"
)

type ResultKind string

const (
	ResultCommand      ResultKind = "command"
	ResultChangedFiles ResultKind = "changed_files"
)

type ScoreInput struct {
	TaskID         string
	CommandResults []CommandResult
	ChangedFiles   []string
	Blocked        bool
	BlockReason    string
}

type CommandResult struct {
	ID       string
	ExitCode int
	Stdout   string
	Stderr   string
	Error    string
}

type Summary struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Blocked int `json:"blocked"`
	Errors  int `json:"errors"`
}

type Report struct {
	Contract     string   `json:"contract"`
	SuiteID      string   `json:"suiteId"`
	TaskID       string   `json:"taskId"`
	Status       Status   `json:"status"`
	OK           bool     `json:"ok"`
	Summary      Summary  `json:"summary"`
	ChangedFiles []string `json:"changedFiles"`
	Results      []Result `json:"results"`
	Error        string   `json:"error,omitempty"`
}

type Result struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Kind            ResultKind `json:"kind"`
	Status          Status     `json:"status"`
	Command         []string   `json:"command,omitempty"`
	ExitCode        *int       `json:"exitCode,omitempty"`
	Stdout          string     `json:"stdout,omitempty"`
	Stderr          string     `json:"stderr,omitempty"`
	Message         string     `json:"message,omitempty"`
	ExpectedFiles   []string   `json:"expectedFiles,omitempty"`
	ActualFiles     []string   `json:"actualFiles,omitempty"`
	MissingFiles    []string   `json:"missingFiles,omitempty"`
	UnexpectedFiles []string   `json:"unexpectedFiles,omitempty"`
}

func Score(suite Suite, input ScoreInput) Report {
	task, err := selectTask(suite, input.TaskID)
	report := Report{
		Contract:     ReportContractVersion,
		SuiteID:      suite.ID,
		TaskID:       input.TaskID,
		Status:       StatusPass,
		OK:           true,
		ChangedFiles: normalizeFiles(input.ChangedFiles),
	}
	if err != nil {
		report.Status = StatusError
		report.OK = false
		report.Error = err.Error()
		report.Results = []Result{{
			ID:      "task",
			Name:    "Task selection",
			Kind:    ResultChangedFiles,
			Status:  StatusError,
			Message: err.Error(),
		}}
		report.finishSummary()
		return report
	}
	report.TaskID = task.ID
	commandResults := commandResultsByID(input.CommandResults)
	seenCommands := map[string]bool{}
	for _, command := range task.VerificationCommands {
		seenCommands[command.ID] = true
		result, found := commandResults[command.ID]
		report.Results = append(report.Results, scoreCommand(command, result, found, input))
	}
	report.Results = append(report.Results, scoreChangedFiles(task.ExpectedChangedFiles, report.ChangedFiles, input))
	for _, result := range unknownCommandResults(input.CommandResults, seenCommands, input) {
		report.Results = append(report.Results, result)
	}
	report.finishSummary()
	return report
}

func scoreCommand(command Command, result CommandResult, found bool, input ScoreInput) Result {
	scored := Result{
		ID:      command.ID,
		Name:    command.Name,
		Kind:    ResultCommand,
		Command: append([]string{}, command.Command...),
		Stdout:  result.Stdout,
		Stderr:  result.Stderr,
	}
	if input.Blocked {
		scored.Status = StatusBlocked
		scored.Message = blockMessage(input.BlockReason)
		return scored
	}
	if !found {
		scored.Status = StatusError
		scored.Message = "missing command result"
		return scored
	}
	exitCode := result.ExitCode
	scored.ExitCode = &exitCode
	if result.Error != "" {
		scored.Status = StatusError
		scored.Message = result.Error
		return scored
	}
	if result.ExitCode == 0 {
		scored.Status = StatusPass
		return scored
	}
	scored.Status = StatusFail
	return scored
}

func scoreChangedFiles(expected []string, actual []string, input ScoreInput) Result {
	result := Result{
		ID:            "changed_files",
		Name:          "Expected changed files",
		Kind:          ResultChangedFiles,
		ExpectedFiles: append([]string{}, expected...),
		ActualFiles:   append([]string{}, actual...),
	}
	if input.Blocked {
		result.Status = StatusBlocked
		result.Message = blockMessage(input.BlockReason)
		return result
	}
	result.MissingFiles = diffFiles(expected, actual)
	result.UnexpectedFiles = diffFiles(actual, expected)
	if len(result.MissingFiles) > 0 || len(result.UnexpectedFiles) > 0 {
		result.Status = StatusFail
		return result
	}
	result.Status = StatusPass
	return result
}

func unknownCommandResults(results []CommandResult, seen map[string]bool, input ScoreInput) []Result {
	unknownByID := map[string]CommandResult{}
	for _, result := range results {
		if result.ID != "" && !seen[result.ID] {
			unknownByID[result.ID] = result
		}
	}
	ids := make([]string, 0, len(unknownByID))
	for id := range unknownByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	scored := make([]Result, 0, len(ids))
	for _, id := range ids {
		result := unknownByID[id]
		exitCode := result.ExitCode
		status := StatusError
		message := firstNonEmpty(result.Error, fmt.Sprintf("unexpected command result %q", result.ID))
		if input.Blocked {
			status = StatusBlocked
			message = blockMessage(input.BlockReason)
		} else if result.Error == "" && result.ExitCode != 0 {
			status = StatusFail
		}
		scored = append(scored, Result{
			ID:       "unknown_command." + result.ID,
			Name:     "Unknown command result",
			Kind:     ResultCommand,
			Status:   status,
			ExitCode: &exitCode,
			Stdout:   result.Stdout,
			Stderr:   result.Stderr,
			Message:  message,
		})
	}
	return scored
}

func (report *Report) finishSummary() {
	report.Summary = Summary{}
	for _, result := range report.Results {
		switch result.Status {
		case StatusPass:
			report.Summary.Passed++
		case StatusFail:
			report.Summary.Failed++
		case StatusBlocked:
			report.Summary.Blocked++
		case StatusError:
			report.Summary.Errors++
		}
	}
	report.Summary.Total = len(report.Results)
	report.OK = report.Summary.Failed == 0 && report.Summary.Blocked == 0 && report.Summary.Errors == 0
	switch {
	case report.Summary.Blocked > 0:
		report.Status = StatusBlocked
	case report.Summary.Errors > 0:
		report.Status = StatusError
	case report.Summary.Failed > 0:
		report.Status = StatusFail
	default:
		report.Status = StatusPass
	}
}

func commandResultsByID(results []CommandResult) map[string]CommandResult {
	byID := map[string]CommandResult{}
	for _, result := range results {
		if result.ID != "" {
			byID[result.ID] = result
		}
	}
	return byID
}

func selectTask(suite Suite, taskID string) (Task, error) {
	if taskID == "" && len(suite.Tasks) == 1 {
		return normalizeTask(suite.Tasks[0]), nil
	}
	for _, task := range suite.Tasks {
		if task.ID == taskID {
			return normalizeTask(task), nil
		}
	}
	if taskID == "" {
		return Task{}, fmt.Errorf("taskId is required when suite has %d tasks", len(suite.Tasks))
	}
	return Task{}, fmt.Errorf("task %q not found", taskID)
}

func normalizeTask(task Task) Task {
	task.ExpectedChangedFiles = normalizeFiles(task.ExpectedChangedFiles)
	return task
}

func diffFiles(left []string, right []string) []string {
	rightSet := map[string]bool{}
	for _, file := range right {
		rightSet[file] = true
	}
	diff := []string{}
	for _, file := range left {
		if !rightSet[file] {
			diff = append(diff, file)
		}
	}
	sort.Strings(diff)
	return diff
}

func blockMessage(reason string) string {
	if reason == "" {
		return "run blocked"
	}
	return "run blocked: " + reason
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
