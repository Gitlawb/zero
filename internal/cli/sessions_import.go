package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/agentsessions"
	"github.com/Gitlawb/zero/internal/redaction"
	"github.com/Gitlawb/zero/internal/sessions"
)

// runSessionsDiscover lists sessions belonging to other coding agents.
//
// Read-only in every sense: it opens transcripts, never credentials, and never
// writes to another agent's store. See internal/agentsessions for why the file
// access is glob-bounded rather than a directory walk.
func runSessionsDiscover(options sessionCommandOptions, stdout io.Writer, stderr io.Writer) int {
	cwd := ""
	if !options.allWorkspaces {
		// Scope to this workspace by default. On a machine with a thousand
		// transcripts, an unscoped list is not a list anyone can use.
		working, err := os.Getwd()
		if err != nil {
			return writeAppError(stderr, "resolve working directory: "+err.Error(), exitCrash)
		}
		cwd = working
	}

	found, problems := agentsessions.DiscoverAll(agentsessions.OSEnv(), cwd)
	found = filterDiscoveredByAgent(found, options.agent)

	if options.json {
		if err := writePrettyJSON(stdout, redaction.RedactValue(discoveredSnapshots(found), redaction.Options{})); err != nil {
			return exitCrash
		}
		return reportDiscoveryProblems(stderr, problems)
	}
	if _, err := fmt.Fprintln(stdout, formatDiscoveredSessions(found, cwd)); err != nil {
		return exitCrash
	}
	return reportDiscoveryProblems(stderr, problems)
}

// reportDiscoveryProblems mentions adapters that failed without failing the
// command. What did work is still worth having.
func reportDiscoveryProblems(stderr io.Writer, problems []error) int {
	for _, problem := range problems {
		fmt.Fprintln(stderr, "warning: "+problem.Error())
	}
	return exitSuccess
}

func filterDiscoveredByAgent(found []agentsessions.ForeignSession, agent string) []agentsessions.ForeignSession {
	wanted := strings.TrimSpace(agent)
	if wanted == "" {
		return found
	}
	filtered := make([]agentsessions.ForeignSession, 0, len(found))
	for _, session := range found {
		if strings.EqualFold(session.Agent, wanted) {
			filtered = append(filtered, session)
		}
	}
	return filtered
}

type discoveredSnapshot struct {
	Agent     string `json:"agent"`
	Ref       string `json:"ref"`
	ID        string `json:"id"`
	Title     string `json:"title"`
	Cwd       string `json:"cwd"`
	GitBranch string `json:"gitBranch,omitempty"`
	ModelID   string `json:"modelId,omitempty"`
	StartedAt string `json:"startedAt,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
	Path      string `json:"path"`
}

func discoveredSnapshots(found []agentsessions.ForeignSession) []discoveredSnapshot {
	out := make([]discoveredSnapshot, 0, len(found))
	for _, session := range found {
		out = append(out, discoveredSnapshot{
			Agent:     session.Agent,
			Ref:       session.Agent + ":" + session.ID,
			ID:        session.ID,
			Title:     session.Title,
			Cwd:       session.Cwd,
			GitBranch: session.GitBranch,
			ModelID:   session.ModelID,
			StartedAt: formatDiscoveredTime(session.StartedAt),
			UpdatedAt: formatDiscoveredTime(session.UpdatedAt),
			Path:      session.Path,
		})
	}
	return out
}

func formatDiscoveredTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

// describeAge renders a last-activity stamp the way a person scanning a list
// wants it: a clock time for today, a date for anything older.
func describeAge(value time.Time, now time.Time) string {
	if value.IsZero() {
		return ""
	}
	local, reference := value.Local(), now.Local()
	switch {
	case local.Year() == reference.Year() && local.YearDay() == reference.YearDay():
		return "today " + local.Format("15:04")
	case local.Year() == reference.Year():
		return local.Format("Jan _2 15:04")
	default:
		return local.Format("2006-01-02")
	}
}

func formatDiscoveredSessions(found []agentsessions.ForeignSession, cwd string) string {
	if len(found) == 0 {
		where := "this workspace"
		if strings.TrimSpace(cwd) == "" {
			where = "any workspace"
		}
		return "No sessions from other coding agents were found for " + where + ".\n" +
			"Agents this build can read: " + strings.Join(agentsessions.AdapterNames(agentsessions.OSEnv()), ", ") + "."
	}

	lines := []string{fmt.Sprintf("%d session(s) from other coding agents:", len(found)), ""}
	for _, session := range found {
		age := ""
		if !session.UpdatedAt.IsZero() {
			age = describeAge(session.UpdatedAt, time.Now())
		}
		header := session.Agent + ":" + session.ID
		lines = append(lines, header)
		detail := "    " + session.Title
		lines = append(lines, detail)
		meta := []string{}
		if session.GitBranch != "" {
			meta = append(meta, "branch "+session.GitBranch)
		}
		if session.ModelID != "" {
			meta = append(meta, session.ModelID)
		}
		if age != "" {
			meta = append(meta, age)
		}
		if len(meta) > 0 {
			lines = append(lines, "    "+strings.Join(meta, "  ·  "))
		}
		lines = append(lines, "")
	}
	lines = append(lines,
		"Import one with:  zero sessions import <agent>:<id>",
		"Then continue it: zero exec --resume <zero-session-id> \"…\"",
	)
	return strings.Join(lines, "\n")
}

// runSessionsImport copies one foreign session into Zero's own store, after
// which every existing session verb — resume, fork, rewind, compact — works on
// it unchanged.
func runSessionsImport(store *sessions.Store, ref string, options sessionCommandOptions, stdout io.Writer, stderr io.Writer) int {
	env := agentsessions.OSEnv()
	adapter, id, err := agentsessions.ParseRef(env, ref)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	result, err := agentsessions.Import(store, adapter, id, agentsessions.ReadOptions{
		MaxEvents:        options.maxEvents,
		IncludeReasoning: options.includeReasoning,
	})
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	if options.json {
		if err := writePrettyJSON(stdout, redaction.RedactValue(map[string]any{
			"sessionId": result.Session.SessionID,
			"title":     result.Session.Title,
			"cwd":       result.Session.Cwd,
			"events":    result.Events,
			"source":    discoveredSnapshots([]agentsessions.ForeignSession{result.Source})[0],
		}, redaction.Options{})); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	lines := []string{
		"Imported " + result.Source.Agent + " session " + result.Source.ID,
		"",
		"  zero session: " + result.Session.SessionID,
		"  title:        " + displayOrNone(result.Session.Title),
		"  cwd:          " + displayOrNone(result.Session.Cwd),
		fmt.Sprintf("  events:       %d", result.Events),
	}
	if warning := importWorkspaceWarning(result.Session.Cwd); warning != "" {
		lines = append(lines, "", warning)
	}
	lines = append(lines, "",
		"Continue it with:",
		"  zero exec --resume "+result.Session.SessionID+" \"Summarise where this left off and what remains\"",
	)
	if _, err := fmt.Fprintln(stdout, strings.Join(lines, "\n")); err != nil {
		return exitCrash
	}
	return exitSuccess
}

// importWorkspaceWarning flags a session that ran somewhere else. Resuming it
// here is allowed — that is a reasonable thing to want — but the file paths in
// its transcript will refer to a different tree, and silence about that is how
// someone spends ten minutes wondering why nothing matches.
func importWorkspaceWarning(sessionCwd string) string {
	recorded := strings.TrimSpace(sessionCwd)
	if recorded == "" {
		return ""
	}
	working, err := os.Getwd()
	if err != nil {
		return ""
	}
	if filepath.Clean(working) == filepath.Clean(recorded) {
		return ""
	}
	return "Note: this session ran in " + recorded + ", not the current directory.\n" +
		"      Paths mentioned in it refer to that tree."
}

func displayOrNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return "(none)"
	}
	return value
}
