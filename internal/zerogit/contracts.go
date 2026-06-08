package zerogit

const RuntimeGo = "go"
const ChangeContractVersion = "zero.changes.report.v1"

type EventType string

const (
	EventChangeSummary EventType = "change_summary"
	EventFileChange    EventType = "file_change"
)

type ChangeSnapshot struct {
	Contract  string       `json:"contract"`
	Runtime   string       `json:"runtime"`
	Root      string       `json:"root"`
	Branch    string       `json:"branch,omitempty"`
	Base      string       `json:"base,omitempty"`
	Commit    string       `json:"commit,omitempty"`
	Clean     bool         `json:"clean"`
	Files     []FileChange `json:"files"`
	DiffStat  string       `json:"diffStat,omitempty"`
	Diff      string       `json:"diff,omitempty"`
	Truncated bool         `json:"truncated,omitempty"`
	Events    []Event      `json:"events"`
}

type Event struct {
	Type      EventType `json:"type"`
	Runtime   string    `json:"runtime,omitempty"`
	Contract  string    `json:"contract,omitempty"`
	Root      string    `json:"root,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Commit    string    `json:"commit,omitempty"`
	Clean     bool      `json:"clean,omitempty"`
	FileCount int       `json:"fileCount,omitempty"`
	Path      string    `json:"path,omitempty"`
	Status    string    `json:"status,omitempty"`
	Staged    bool      `json:"staged,omitempty"`
	Unstaged  bool      `json:"unstaged,omitempty"`
	Untracked bool      `json:"untracked,omitempty"`
}

func SnapshotFromSummary(summary ChangeSummary) ChangeSnapshot {
	files := redactFiles(summary.Files)
	return ChangeSnapshot{
		Contract:  ChangeContractVersion,
		Runtime:   RuntimeGo,
		Root:      redactText(summary.Root),
		Branch:    redactText(summary.Branch),
		Base:      redactText(summary.Base),
		Commit:    redactText(summary.Commit),
		Clean:     summary.Clean,
		Files:     files,
		DiffStat:  redactText(summary.DiffStat),
		Diff:      redactText(summary.Diff),
		Truncated: summary.Truncated,
		Events:    EventsFromSummary(summary),
	}
}

func EventsFromSummary(summary ChangeSummary) []Event {
	events := []Event{
		{
			Type:      EventChangeSummary,
			Runtime:   RuntimeGo,
			Contract:  ChangeContractVersion,
			Root:      redactText(summary.Root),
			Branch:    redactText(summary.Branch),
			Commit:    redactText(summary.Commit),
			Clean:     summary.Clean,
			FileCount: len(summary.Files),
		},
	}
	for _, file := range summary.Files {
		events = append(events, Event{
			Type:      EventFileChange,
			Runtime:   RuntimeGo,
			Contract:  ChangeContractVersion,
			Root:      redactText(summary.Root),
			Branch:    redactText(summary.Branch),
			Commit:    redactText(summary.Commit),
			Path:      redactText(file.Path),
			Status:    redactText(file.Status),
			Staged:    file.Staged,
			Unstaged:  file.Unstaged,
			Untracked: file.Untracked,
		})
	}
	return events
}

func redactFiles(files []FileChange) []FileChange {
	if len(files) == 0 {
		return []FileChange{}
	}
	next := append([]FileChange{}, files...)
	for index := range next {
		next[index].Path = redactText(next[index].Path)
		next[index].Status = redactText(next[index].Status)
	}
	return next
}
