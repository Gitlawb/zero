package agenteval

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

type BenchmarkInput struct {
	TaskID         string
	WorkRoot       string
	Models         []string
	KeepWorkspaces bool
	// Timeout bounds each task's materialization, agent run, and scoring. A
	// non-positive value leaves the task unbounded.
	Timeout time.Duration
}

type BenchmarkReport struct {
	Contract string                `json:"contract"`
	SuiteID  string                `json:"suiteId"`
	OK       bool                  `json:"ok"`
	Summary  BenchmarkSummary      `json:"summary"`
	Tasks    []BenchmarkTaskReport `json:"tasks"`
}

type BenchmarkTaskReport struct {
	TaskID        string         `json:"taskId"`
	Model         string         `json:"model,omitempty"`
	WorkspacePath string         `json:"workspacePath"`
	FixturePath   string         `json:"fixturePath"`
	Agent         AgentRunResult `json:"agent"`
	Report        Report         `json:"report"`
}

type BenchmarkSummary struct {
	TotalTasks   int `json:"totalTasks"`
	PassedTasks  int `json:"passedTasks"`
	FailedTasks  int `json:"failedTasks"`
	BlockedTasks int `json:"blockedTasks"`
	ErrorTasks   int `json:"errorTasks"`
}

type Harness struct {
	Materializer Materializer
	Agent        AgentRunner
	Runner       Runner
}

func (harness Harness) Run(ctx context.Context, suitePath string, suite Suite, input BenchmarkInput) BenchmarkReport {
	if ctx == nil {
		ctx = context.Background()
	}
	report := BenchmarkReport{
		Contract: ReportContractVersion,
		SuiteID:  suite.ID,
	}
	tasks, err := selectBenchmarkTasks(suite, input.TaskID)
	if err != nil {
		taskID := input.TaskID
		report.Tasks = append(report.Tasks, BenchmarkTaskReport{
			TaskID: taskID,
			Agent:  AgentRunResult{ExitCode: -1, Error: err.Error()},
			Report: Report{
				Contract: ReportContractVersion,
				SuiteID:  suite.ID,
				TaskID:   taskID,
				Status:   StatusError,
				OK:       false,
				Summary:  Summary{Total: 1, Errors: 1},
				Error:    err.Error(),
				Results: []Result{{
					ID:      "task",
					Name:    "Task selection",
					Kind:    ResultChangedFiles,
					Status:  StatusError,
					Message: err.Error(),
				}},
			},
		})
		report.finishSummary()
		return report
	}

	for _, task := range tasks {
		for _, model := range benchmarkModels(input.Models) {
			report.Tasks = append(report.Tasks, harness.runTask(ctx, suitePath, suite, task, model, input))
		}
	}
	report.finishSummary()
	return report
}

func (harness Harness) runTask(ctx context.Context, suitePath string, suite Suite, task Task, model string, input BenchmarkInput) BenchmarkTaskReport {
	if input.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, input.Timeout)
		defer cancel()
	}
	taskReport := BenchmarkTaskReport{
		TaskID: task.ID,
		Model:  model,
		Agent:  AgentRunResult{ExitCode: -1},
	}
	if harness.Agent == nil {
		taskReport.Agent = AgentRunResult{ExitCode: -1, Error: "agent command is required"}
		taskReport.Report = Score(suite, ScoreInput{
			TaskID:      task.ID,
			Blocked:     true,
			BlockReason: taskReport.Agent.Error,
		})
		return taskReport
	}

	workspace, err := harness.Materializer.MaterializeTask(ctx, suitePath, task, MaterializeInput{
		WorkRoot: input.WorkRoot,
	})
	if err != nil {
		taskReport.Agent.Error = err.Error()
		taskReport.Report = errorReport(suite.ID, task.ID, fmt.Sprintf("workspace materialization failed: %v", err))
		return taskReport
	}
	taskReport.WorkspacePath = workspace.Path
	taskReport.FixturePath = workspace.FixturePath
	if !input.KeepWorkspaces {
		defer func() { _ = os.RemoveAll(workspace.Path) }()
	}

	agentResult := harness.Agent.Run(ctx, AgentRunInput{
		TaskID:        task.ID,
		Model:         model,
		Prompt:        task.Prompt,
		WorkspacePath: workspace.Path,
	})
	taskReport.Agent = agentResult
	if agentResult.Error != "" || agentResult.ExitCode != 0 {
		reason := firstNonEmpty(agentResult.Error, strings.TrimSpace(agentResult.Stderr), fmt.Sprintf("agent exited with code %d", agentResult.ExitCode))
		taskReport.Report = Score(suite, ScoreInput{
			TaskID:      task.ID,
			Blocked:     true,
			BlockReason: reason,
		})
		return taskReport
	}

	taskReport.Report = harness.Runner.Run(ctx, suite, RunInput{
		TaskID:        task.ID,
		WorkspacePath: workspace.Path,
		TraceStdout:   agentResult.Stdout,
	})
	return taskReport
}

func (report *BenchmarkReport) finishSummary() {
	report.Summary = BenchmarkSummary{TotalTasks: len(report.Tasks)}
	for _, task := range report.Tasks {
		switch {
		case task.Report.OK:
			report.Summary.PassedTasks++
		case task.Report.Status == StatusBlocked:
			report.Summary.BlockedTasks++
		case task.Report.Status == StatusError:
			report.Summary.ErrorTasks++
		default:
			report.Summary.FailedTasks++
		}
	}
	report.OK = report.Summary.TotalTasks > 0 &&
		report.Summary.FailedTasks == 0 &&
		report.Summary.BlockedTasks == 0 &&
		report.Summary.ErrorTasks == 0
}

func selectBenchmarkTasks(suite Suite, taskID string) ([]Task, error) {
	if taskID == "" {
		tasks := make([]Task, 0, len(suite.Tasks))
		for _, task := range suite.Tasks {
			tasks = append(tasks, normalizeTask(task))
		}
		return tasks, nil
	}
	task, err := selectTask(suite, taskID)
	if err != nil {
		return nil, err
	}
	return []Task{task}, nil
}

func benchmarkModels(models []string) []string {
	normalized := make([]string, 0, len(models))
	seen := map[string]bool{}
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			continue
		}
		seen[model] = true
		normalized = append(normalized, model)
	}
	if len(normalized) == 0 {
		return []string{""}
	}
	return normalized
}

func errorReport(suiteID string, taskID string, message string) Report {
	return Report{
		Contract: ReportContractVersion,
		SuiteID:  suiteID,
		TaskID:   taskID,
		Status:   StatusError,
		OK:       false,
		Summary:  Summary{Total: 1, Errors: 1},
		Error:    message,
		Results: []Result{{
			ID:      "benchmark",
			Name:    "Benchmark harness",
			Kind:    ResultChangedFiles,
			Status:  StatusError,
			Message: message,
		}},
	}
}
