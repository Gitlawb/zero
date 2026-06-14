package tui

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/Gitlawb/zero/internal/agent"
	"github.com/Gitlawb/zero/internal/tools"
)

func displayValue(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// pickerBusyText explains that a settings picker (/model, /mode, /effort)
// can't be opened while a run is in flight. Opening it then would silently refuse
// the selection once the run lands, so the no-arg command no-ops into this notice.
func pickerBusyText(name string) string {
	label := strings.TrimPrefix(name, "/")
	return renderCommandOutput(commandOutput{
		Title:  label,
		Status: commandStatusWarning,
		Sections: []commandSection{{
			Title: "Busy",
			Lines: []string{"Can't change " + label + " while a run is in progress."},
		}},
		Hints: []string{"press Esc to cancel the run, then try again"},
	})
}

func shellOnlyCommandText(name string) string {
	return renderCommandOutput(commandOutput{
		Title:  strings.TrimPrefix(name, "/"),
		Status: commandStatusWarning,
		Sections: []commandSection{{
			Title: "State",
			Lines: []string{"This control is available in the TUI but does not have a backend setting yet."},
		}},
		Hints: []string{"use /help to inspect active commands"},
	})
}

func helpText() string {
	return formatGroupedCommandHelp()
}

// rowContext carries the cross-row knowledge renderRow needs: which tool
// calls already have results (their call rows collapse into the result card),
// each call's argument hints for the card head, and which calls were
// auto-approved (by permission mode or a stored grant). All maps are keyed by
// run-scoped ids (rcKey): some providers synthesize ToolCallIDs that repeat
// across turns (e.g. Gemini's gemini_tool_N), so a bare id could attribute a
// decision or a result to a different run's call.
type rowContext struct {
	resolved map[string]bool
	hints    map[string]string
	args     map[string]string
	auto     map[string]bool
	decided  map[string]bool
}

func rcKey(runID int, id string) string {
	return strconv.Itoa(runID) + ":" + id
}

func buildRowContext(rows []transcriptRow) rowContext {
	rc := rowContext{
		resolved: map[string]bool{},
		hints:    map[string]string{},
		args:     map[string]string{},
		auto:     map[string]bool{},
		decided:  map[string]bool{},
	}
	prompted := map[string]bool{}
	for _, row := range rows {
		switch row.kind {
		case rowToolCall:
			if row.id != "" {
				rc.hints[rcKey(row.runID, row.id)] = strings.TrimSpace(row.detail)
				rc.args[rcKey(row.runID, row.id)] = strings.TrimSpace(row.arg)
			}
		case rowToolResult:
			if row.id != "" {
				rc.resolved[rcKey(row.runID, row.id)] = true
			}
		case rowPermission:
			event := row.permission
			if event == nil || event.ToolCallID == "" {
				continue
			}
			key := rcKey(row.runID, event.ToolCallID)
			switch event.Action {
			case agent.PermissionActionPrompt:
				prompted[key] = true
				delete(rc.auto, key)
			case agent.PermissionActionAllow:
				rc.decided[key] = true
				// "auto" means approved without asking: a mode/policy allow or a
				// stored grant match. Any allow that followed a prompt — including a
				// first-time "always" — was a manual decision, not auto.
				if !prompted[key] {
					rc.auto[key] = true
				}
			case agent.PermissionActionDeny:
				rc.decided[key] = true
			}
		}
	}
	return rc
}

// skip reports whether a row renders nothing itself: a tool call whose result
// arrived collapses into the result's card; a permission prompt that has been
// decided collapses into its decision line; an unprompted allow is already
// surfaced as the card's [auto] tag.
func (rc rowContext) skip(row transcriptRow) bool {
	switch row.kind {
	case rowToolCall:
		return row.id != "" && rc.resolved[rcKey(row.runID, row.id)]
	case rowPermission:
		event := row.permission
		if event == nil || event.ToolCallID == "" {
			return false
		}
		key := rcKey(row.runID, event.ToolCallID)
		switch event.Action {
		case agent.PermissionActionPrompt:
			return rc.decided[key]
		case agent.PermissionActionAllow:
			return rc.auto[key]
		}
	}
	return false
}

// cardRenderOptions carries per-render knobs for tool cards: the body-line cap
// (small for the live region, generous for the permanent scrollback flush) and
// the workspace root used to absolutize paths for OSC 8 file hyperlinks.
type cardRenderOptions struct {
	bodyCap int
	cwd     string
}

// flushCardBodyMaxLines is the body cap for cards flushed to scrollback. The
// small live cap exists only to keep the managed region tidy; history can hold
// full output — most importantly the complete diffs of edited code, which the
// user reviews by scrolling up.
const flushCardBodyMaxLines = 400

func (m model) renderRow(row transcriptRow, width int, rc rowContext) string {
	return m.renderRowMode(row, width, rc, false)
}

func (m model) renderRowDetailed(row transcriptRow, width int, rc rowContext) string {
	opts := cardRenderOptions{bodyCap: 0, cwd: m.cwd}
	if defaultRenderCache != nil {
		if key, stable := m.renderRowCacheKey(row, width, rc, opts, false); key != "" {
			return defaultRenderCache.render(key, stable, func() string {
				return m.renderRowModeUncached(row, width, rc, opts)
			})
		}
	}
	return m.renderRowModeUncached(row, width, rc, opts)
}

// renderRowMode renders a transcript row either for the live region (flush ==
// false: tight body caps, spinner-capable) or for its one-time scrollback
// flush (flush == true: deep body caps so edited code stays reviewable).
func (m model) renderRowMode(row transcriptRow, width int, rc rowContext, flush bool) string {
	opts := cardRenderOptions{bodyCap: cardBodyMaxLines, cwd: m.cwd}
	if flush {
		opts.bodyCap = flushCardBodyMaxLines
	}
	if defaultRenderCache != nil {
		if key, stable := m.renderRowCacheKey(row, width, rc, opts, flush); key != "" {
			return defaultRenderCache.render(key, stable, func() string {
				return m.renderRowModeUncached(row, width, rc, opts)
			})
		}
	}
	return m.renderRowModeUncached(row, width, rc, opts)
}

func (m model) renderRowModeUncached(row transcriptRow, width int, rc rowContext, opts cardRenderOptions) string {
	switch row.kind {
	case rowUser:
		return renderUserRow(row, width)
	case rowAssistant:
		return renderAssistantRow(row, width)
	case rowSystem:
		if row.tool == "sessions" {
			return renderSessionsCards(row.text, width)
		}
		if row.tool == "mcp" {
			return renderMCPManagerCard(row.text, width)
		}
		if row.id == compactStatusRowID && strings.HasPrefix(strings.TrimSpace(row.text), "Compressing session") {
			return renderCompactRunningCard(row.text, width)
		}
		if row.id == compactStatusRowID && strings.HasPrefix(strings.TrimSpace(row.text), "Compression complete") {
			return renderCompactCompleteCard(row.text, width)
		}
		if row.id == doctorStatusRowID && strings.HasPrefix(strings.TrimSpace(row.text), "Checking provider") {
			return renderDoctorRunningCard(row.text, width)
		}
		if row.id == doctorStatusRowID {
			return renderDoctorResultCard(row.text, width)
		}
		return renderSystemNote(row.text, width)
	case rowError:
		return renderErrorRow(row, width)
	case rowToolCall:
		return m.renderRunningToolCard(row, width, rc, opts)
	case rowToolResult:
		if isInternalToolArgumentError(row) {
			return ""
		}
		return renderToolResultCard(row, width, rc, opts)
	case rowPermission:
		return renderPermissionRow(row, width)
	case rowAskUser:
		return renderAskUserRow(row, width)
	default:
		return row.text
	}
}

func isInternalToolArgumentError(row transcriptRow) bool {
	if row.status != tools.StatusError {
		return false
	}
	detail := strings.TrimSpace(row.detail)
	return strings.HasPrefix(detail, "Error: Failed to parse arguments for ") ||
		row.tool == "ask_user" && strings.HasPrefix(detail, "Error: Invalid arguments for ask_user:")
}

// hyperlink wraps already-styled text in an OSC 8 terminal hyperlink so
// supporting terminals (iTerm2, WezTerm, kitty, Ghostty, …) make it clickable
// — cmd/ctrl+click on an edited file opens it. The sequences are zero-width
// for lipgloss/x-ansi width math, and truncateStyledLine skips and re-closes
// them via ansiSequenceEnd.
func hyperlink(url string, text string) string {
	if url == "" || text == "" {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

// fileURL builds the file:// link target for a workspace path. The path is
// percent-encoded (spaces especially) — a raw space inside an OSC 8 URI makes
// some terminals terminate the sequence early and print the remainder.
func fileURL(cwd string, path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	link := url.URL{Scheme: "file", Path: path}
	return link.String()
}

// looksLikePath reports whether a tool-card target plausibly names a file —
// the only targets worth turning into hyperlinks (bash commands and grep
// patterns also flow through the target column).
func looksLikePath(value string) bool {
	if value == "" || strings.ContainsAny(value, " \t") {
		return false
	}
	return strings.Contains(value, "/") || filepath.Ext(value) != ""
}

// sayMeasure is the prose wrap width for user/assistant text: min(width-4, 74).
func sayMeasure(width int) int {
	measure := width - 4
	if measure > 74 {
		measure = 74
	}
	if measure < 16 {
		measure = 16
	}
	return measure
}

// wrapPlainText word-wraps unstyled text to the measure, preserving explicit
// newlines AND each line's leading indentation (wrapped continuations keep the
// same indent), so code blocks and aligned lists in assistant answers survive.
// Words longer than the available measure are hard-split so no emitted line
// can exceed it.
func wrapPlainText(text string, measure int) []string {
	if measure < 1 {
		measure = 1
	}
	out := []string{}
	for _, paragraph := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(paragraph) == "" {
			out = append(out, "")
			continue
		}
		body := strings.TrimLeft(paragraph, " \t")
		// Tabs render unpredictably across terminals; a fixed 4-cell indent
		// keeps the width math exact (same policy as the tool cards).
		indent := strings.ReplaceAll(paragraph[:len(paragraph)-len(body)], "\t", "    ")
		if len(indent) >= measure {
			indent = strings.Repeat(" ", measure/2)
		}
		available := measure - len(indent)
		line := ""
		for _, word := range strings.Fields(body) {
			for lipgloss.Width(word) > available {
				if line != "" {
					out = append(out, indent+line)
					line = ""
				}
				head, tail := splitAtWidth(word, available)
				out = append(out, indent+head)
				word = tail
			}
			switch {
			case line == "":
				line = word
			case lipgloss.Width(line)+1+lipgloss.Width(word) <= available:
				line += " " + word
			default:
				out = append(out, indent+line)
				line = word
			}
		}
		if line != "" {
			out = append(out, indent+line)
		}
	}
	return out
}

// splitAtWidth cuts text at the largest rune boundary whose display width
// fits the measure. CJK and emoji runes are double-width, so slicing by rune
// count would either panic or emit lines up to twice the measure.
func splitAtWidth(text string, measure int) (string, string) {
	used := 0
	for index, glyph := range text {
		glyphWidth := lipgloss.Width(string(glyph))
		if used+glyphWidth > measure {
			if index == 0 {
				// A single glyph wider than the measure still has to go somewhere.
				_, size := utf8.DecodeRuneInString(text)
				return text[:size], text[size:]
			}
			return text[:index], text[index:]
		}
		used += glyphWidth
	}
	return text, ""
}

func renderUserRow(row transcriptRow, width int) string {
	lines := wrapPlainText(row.text, sayMeasure(width))
	for index, line := range lines {
		if index == 0 {
			lines[index] = zeroTheme.userPrompt.Render("❯ ") + zeroTheme.ink.Render(line)
		} else {
			lines[index] = "  " + zeroTheme.ink.Render(line)
		}
	}
	return strings.Join(lines, "\n")
}

// renderAssistantRow draws the turn's final answer with the accent rail
// gutter plus its done line; a non-final assistant row (e.g. a rehydrated
// inline notice) renders as plain interim-style prose.
func renderAssistantRow(row transcriptRow, width int) string {
	tableMeasure := width
	if row.final {
		tableMeasure = maxInt(16, width-2)
	}
	lines := renderAssistantMarkdownText(row.text, sayMeasure(width), tableMeasure)
	if !row.final {
		for index := range lines {
			lines[index] = styleAssistantMarkdownLine(lines[index], zeroTheme.sayText)
		}
		return strings.Join(lines, "\n")
	}
	for index := range lines {
		lines[index] = zeroTheme.finalRail.Render("│ ") + styleAssistantMarkdownLine(lines[index], zeroTheme.ink)
	}
	lines = append(lines, doneLine(row, false))
	return strings.Join(lines, "\n")
}

// doneLine renders the turn terminator: ● green (red on error) plus faint
// counters. Segments the turn has no data for are omitted, never invented.
func doneLine(row transcriptRow, failed bool) string {
	glyph := zeroTheme.green.Render("●")
	label := "done"
	if failed {
		glyph = zeroTheme.red.Render("●")
		label = "error"
	}
	segments := []string{zeroTheme.faint.Render(label)}
	if row.turnTools > 0 {
		noun := "tools"
		if row.turnTools == 1 {
			noun = "tool"
		}
		segments = append(segments, zeroTheme.faint.Render(fmt.Sprintf("%d %s", row.turnTools, noun)))
	}
	if row.turnElapsed > 0 {
		segments = append(segments, zeroTheme.faint.Render(fmt.Sprintf("%.1fs", row.turnElapsed.Seconds())))
	}
	return glyph + " " + strings.Join(segments, zeroTheme.faintest.Render(" · "))
}

// renderSystemNote draws a system notice as a bordered note: faint text on
// the panel surface inside a line border. Content is passed through unchanged.
func renderSystemNote(text string, width int) string {
	return noteBox(text, width, zeroTheme.line, zeroTheme.onPanel(zeroTheme.faint))
}

func renderMCPManagerCard(text string, width int) string {
	raw := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for index, line := range raw {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			lines = append(lines, "")
		case index == 0:
			lines = append(lines, zeroTheme.accent.Bold(true).Render(line))
		case index == 1:
			lines = append(lines, zeroTheme.ink.Bold(true).Render(line))
		case isMCPManagerHeading(trimmed):
			lines = append(lines, zeroTheme.accent.Bold(true).Render(line))
		case strings.Contains(trimmed, "zero mcp "):
			lines = append(lines, zeroTheme.ink.Render(line))
		case strings.HasPrefix(trimmed, "›") || strings.HasPrefix(trimmed, "- "):
			lines = append(lines, zeroTheme.ink.Render(line))
		default:
			lines = append(lines, zeroTheme.muted.Render(line))
		}
	}
	return styledBlock(width, lines, zeroTheme.accent)
}

func isMCPManagerHeading(value string) bool {
	switch value {
	case "User MCPs", "Built-in MCPs", "Tools", "Permissions", "OAuth", "Actions":
		return true
	default:
		return false
	}
}

func renderCompactRunningCard(text string, width int) string {
	raw := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	lines := make([]string, 0, len(raw)+1)
	for index, line := range raw {
		switch index {
		case 0:
			lines = append(lines, zeroTheme.amber.Bold(true).Render(line))
		case 1:
			lines = append(lines, zeroTheme.muted.Render(line))
		case 2:
			lines = append(lines, zeroTheme.amber.Bold(true).Render(line))
		default:
			lines = append(lines, zeroTheme.faint.Render(line))
		}
		if index == 0 {
			lines = append(lines, "")
		}
	}
	return styledBlock(width, lines, zeroTheme.amber)
}

func renderCompactCompleteCard(text string, width int) string {
	raw := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	lines := make([]string, 0, len(raw)+1)
	for index, line := range raw {
		switch index {
		case 0:
			lines = append(lines, zeroTheme.green.Bold(true).Render(line))
		case 1:
			lines = append(lines, zeroTheme.ink.Render(line))
		default:
			lines = append(lines, zeroTheme.muted.Render(line))
		}
		if index == 0 {
			lines = append(lines, "")
		}
	}
	return styledBlock(width, lines, zeroTheme.green)
}

func renderDoctorRunningCard(text string, width int) string {
	raw := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	lines := make([]string, 0, len(raw)+1)
	for index, line := range raw {
		switch index {
		case 0:
			lines = append(lines, zeroTheme.accent.Bold(true).Render(line))
		case 1:
			lines = append(lines, zeroTheme.muted.Render(line))
		case 2:
			lines = append(lines, zeroTheme.accent.Bold(true).Render(line))
		default:
			lines = append(lines, zeroTheme.faint.Render(line))
		}
		if index == 0 {
			lines = append(lines, "")
		}
	}
	return styledBlock(width, lines, zeroTheme.accent)
}

func renderDoctorResultCard(text string, width int) string {
	raw := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	border := doctorResultBorderStyle(text)
	lines := make([]string, 0, len(raw))
	for index, line := range raw {
		trimmed := strings.TrimSpace(line)
		switch {
		case index == 0:
			lines = append(lines, border.Bold(true).Render(line))
		case strings.HasPrefix(trimmed, "status:"):
			lines = append(lines, border.Render(line))
		case isDoctorResultHeading(trimmed):
			lines = append(lines, zeroTheme.accent.Bold(true).Render(line))
		case strings.HasPrefix(trimmed, "- [pass]"):
			lines = append(lines, zeroTheme.green.Render(line))
		case strings.HasPrefix(trimmed, "- [warn]"):
			lines = append(lines, zeroTheme.amber.Render(line))
		case strings.HasPrefix(trimmed, "- [fail]"):
			lines = append(lines, zeroTheme.red.Render(line))
		case strings.HasPrefix(trimmed, "hint:"):
			lines = append(lines, zeroTheme.faint.Render(line))
		case strings.HasPrefix(trimmed, "/") ||
			strings.HasPrefix(trimmed, "WSL2") ||
			strings.HasPrefix(trimmed, "install "):
			lines = append(lines, zeroTheme.ink.Render(line))
		default:
			lines = append(lines, zeroTheme.muted.Render(line))
		}
		if index == 0 {
			lines = append(lines, "")
		}
	}
	return styledBlock(width, lines, border)
}

func doctorResultBorderStyle(text string) lipgloss.Style {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		switch strings.TrimSpace(line) {
		case "status: ok":
			return zeroTheme.green
		case "status: blocked":
			return zeroTheme.red
		case "status: warning":
			return zeroTheme.amber
		}
	}
	return zeroTheme.accent
}

func isDoctorResultHeading(value string) bool {
	switch value {
	case "Summary", "Provider", "Platform", "Other", "Backend", "Actions":
		return true
	default:
		return false
	}
}

func renderErrorRow(row transcriptRow, width int) string {
	note := noteBox(row.text, width, zeroTheme.cardErr, zeroTheme.red)
	if row.final {
		note += "\n" + doneLine(row, true)
	}
	return note
}

// noteBox is the bordered one-note container behind system and error rows.
func noteBox(text string, width int, borderStyle lipgloss.Style, textStyle lipgloss.Style) string {
	raw := strings.Split(strings.TrimRight(strings.ReplaceAll(text, "\r\n", "\n"), "\n"), "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, textStyle.Render(line))
	}
	return styledBlock(width, lines, borderStyle)
}

func renderAskUserRow(row transcriptRow, width int) string {
	line := fitStyledLine(zeroTheme.accent.Render("ask zero")+"  "+zeroTheme.ink.Render(strings.TrimPrefix(row.text, "ask_user: ")), width)
	if detail := strings.TrimSpace(row.detail); detail != "" {
		line += "\n" + wrapDetailBlock(detail, width)
	}
	return line
}

// renderPermissionRow draws the transcript record of a permission event. A
// decided prompt and an auto-approved allow are skipped upstream, so this
// sees: undecided prompts (one amber line + detail), manual decisions (the
// spec's collapsed one-liner), and denials.
func renderPermissionRow(row transcriptRow, width int) string {
	event := row.permission
	if event == nil {
		return zeroTheme.amber.Render("permission") + "  " + zeroTheme.ink.Render(row.text)
	}

	name := event.ToolName
	if name == "" {
		name = row.tool
	}
	dot := zeroTheme.faintest.Render(" · ")

	switch event.Action {
	case agent.PermissionActionAllow:
		label := "allowed once"
		// Rehydrated events lose the grant object but keep GrantMatched, which
		// for a prompted allow is set exactly by the always path.
		if event.Grant != nil || event.GrantMatched {
			label = "always"
		}
		line := zeroTheme.green.Render(label) + dot + zeroTheme.green.Render(name)
		if scope := strings.TrimSpace(event.Scope); scope != "" {
			line += dot + zeroTheme.muted.Render("scope:"+scope)
		}
		return fitStyledLine(line, width)
	case agent.PermissionActionDeny:
		line := zeroTheme.red.Render("denied") + dot + zeroTheme.red.Render(name)
		if scope := strings.TrimSpace(event.Scope); scope != "" {
			line += dot + zeroTheme.muted.Render("scope:"+scope)
		}
		if event.Risk.Level != "" {
			line += dot + zeroTheme.muted.Render("risk:"+string(event.Risk.Level))
		}
		if reason := strings.TrimSpace(event.Reason); reason != "" {
			line += zeroTheme.faint.Render(" — " + truncateRunes(reason, maxInt(16, width-lipgloss.Width(name)-16)))
		}
		out := fitStyledLine(line, width)
		if detail := strings.TrimSpace(row.detail); detail != "" {
			out += "\n" + wrapDetailBlock(detail, width)
		}
		return out
	default:
		line := zeroTheme.amber.Render("permission") + "  " + zeroTheme.ink.Render(name) + "  " + zeroTheme.amber.Render("prompt")
		if scope := strings.TrimSpace(event.Scope); scope != "" {
			line += "  " + zeroTheme.muted.Render("scope:"+scope)
		}
		if event.Risk.Level != "" {
			line += "  " + zeroTheme.muted.Render("risk:"+string(event.Risk.Level))
		}
		out := fitStyledLine(line, width)
		if detail := strings.TrimSpace(row.detail); detail != "" {
			out += "\n" + wrapDetailBlock(detail, width)
		}
		return out
	}
}

// wrapDetailBlock wraps a metadata detail blob to the terminal and indents it
// two cells, so no permission/ask row can emit a line wider than the frame.
func wrapDetailBlock(detail string, width int) string {
	lines := wrapPlainText(detail, maxInt(16, width-2))
	for index := range lines {
		lines[index] = "  " + zeroTheme.muted.Render(lines[index])
	}
	return strings.Join(lines, "\n")
}

// renderFocusedPermissionPrompt draws the modal permission card: PERMISSION
// badge + risk on top, tool + reason body, then the key-chip action row. The
// keys themselves are handled in handlePermissionKey, unchanged.
func renderFocusedPermissionPrompt(request agent.PermissionRequest, width int) string {
	name := strings.TrimSpace(request.ToolName)
	if name == "" {
		name = "tool"
	}
	fill := zeroTheme.onPerm

	top := zeroTheme.permBadge.Render(" PERMISSION ")
	if request.Risk.Level != "" {
		top += fill(zeroTheme.permRisk).Render("  risk: " + string(request.Risk.Level))
	}

	body := fill(zeroTheme.amber).Bold(true).Render(name)
	if request.SideEffect != "" {
		body += fill(zeroTheme.ink).Render("  " + request.SideEffect)
	}
	lines := []string{top, body}
	if reason := strings.TrimSpace(request.Reason); reason != "" {
		lines = append(lines, fill(zeroTheme.muted).Render(reason))
	}
	// Surface exactly what the grant covers (the file/dir the call touches) so
	// "always" is a clear, bounded choice rather than a blind tool-wide yes.
	if scope := strings.TrimSpace(request.Scope); scope != "" {
		lines = append(lines, fill(zeroTheme.muted).Render("scope: "+scope))
	}

	actions := zeroTheme.badge.Render(" [a] allow once ") +
		fill(zeroTheme.ink).Render(" ") +
		fill(zeroTheme.accent).Render("[y]") + fill(zeroTheme.ink).Render(" always ") +
		fill(zeroTheme.red).Render("[d]") + fill(zeroTheme.ink).Render(" deny ") +
		fill(zeroTheme.faint).Render("[esc] cancel run")
	lines = append(lines, actions)

	return styledBlockFill(width, lines, zeroTheme.permBorder, zeroTheme.permBg)
}

// renderFocusedAskUserPrompt draws the ask-user questionnaire in the same
// card language as the permission card, with line borders.
func renderFocusedAskUserPrompt(prompt pendingAskUserPrompt, input string, width int) string {
	questions := prompt.request.Questions
	total := len(questions)
	index := prompt.index
	if index >= total {
		index = total - 1
	}
	if index < 0 {
		index = 0
	}
	fill := zeroTheme.onPanel

	heading := zeroTheme.badge.Render(" ASK ")
	if header := strings.TrimSpace(prompt.request.Header); header != "" {
		heading += fill(zeroTheme.ink).Render(" " + header)
	}
	lines := []string{heading}

	if total > 0 {
		question := questions[index]
		lines = append(lines, fill(zeroTheme.faint).Render(fmt.Sprintf("question %d of %d", index+1, total)))
		lines = append(lines, fill(zeroTheme.ink).Render(question.Question))
		if len(question.Options) > 0 {
			lines = append(lines, fill(zeroTheme.muted).Render("options: "+strings.Join(question.Options, ", ")))
		}
	}
	// Echo the in-progress answer inside the card so the user sees what they
	// are typing where they are answering, cursor included.
	answer := zeroTheme.userPrompt.Background(lipgloss.Color(colorPanel)).Render("❯ ") +
		fill(zeroTheme.ink).Render(input) + fill(zeroTheme.accent).Render("▌")
	lines = append(lines, answer)
	lines = append(lines, fill(zeroTheme.faint).Render("type an answer, Enter to submit · Esc to skip"))

	return styledBlockFill(width, lines, zeroTheme.line, zeroTheme.panel)
}

// --- Tool cards -------------------------------------------------------------

// cardBodyMaxLines caps every card body; hidden lines collapse into a
// "… N more lines" trailer.
const cardBodyMaxLines = 16

// cardBody is what a result-shape renderer hands back: body lines, an
// optional footer embedded in the bottom border, and optional extra head
// metadata (e.g. a read's line range).
type cardBody struct {
	lines   []string
	footer  string
	headTag string
}

// renderRunningToolCard draws the head-only card for a tool call that has no
// result yet: spinner glyph while ITS run is live, a static placeholder for
// orphans (cancelled/errored turns, rehydrated history) — keying off the
// global pending flag alone would re-animate dead cards on every later run.
func (m model) renderRunningToolCard(row transcriptRow, width int, rc rowContext, opts cardRenderOptions) string {
	glyph := zeroTheme.faintest.Render("…")
	if m.pending && row.runID != 0 && row.runID == m.activeRunID {
		glyph = m.spinner.View()
	}
	// The call row carries its own argHints; rc.hints/args only matter for
	// result rows, whose detail is the tool output.
	hint := strings.TrimSpace(row.detail)
	if hint == "" {
		hint = rc.hints[rcKey(row.runID, row.id)]
	}
	arg := strings.TrimSpace(row.arg)
	if arg == "" {
		arg = rc.args[rcKey(row.runID, row.id)]
	}
	head := toolCardHead(toolRowName(row), hint, arg, "", glyph, rc.auto[rcKey(row.runID, row.id)], width, opts)
	return toolCard(head, nil, "", zeroTheme.cardRun, width)
}

func renderToolResultCard(row transcriptRow, width int, rc rowContext, opts cardRenderOptions) string {
	name := toolRowName(row)
	failed := row.status == tools.StatusError
	glyph := zeroTheme.green.Render("✓")
	borderStyle := zeroTheme.line
	if failed {
		glyph = zeroTheme.red.Render("✗")
		borderStyle = zeroTheme.cardErr
	}
	key := rcKey(row.runID, row.id)
	body := toolCardBody(name, rc.hints[key], row.detail, width, opts)
	head := toolCardHead(name, rc.hints[key], rc.args[key], body.headTag, glyph, rc.auto[key], width, opts)
	return toolCard(head, body.lines, body.footer, borderStyle, width)
}

func toolRowName(row transcriptRow) string {
	if row.tool != "" {
		return row.tool
	}
	name := strings.TrimPrefix(row.text, "tool call: ")
	return strings.TrimPrefix(name, "tool result: ")
}

// toolCardHead composes the border-embedded head: tool name, middle-truncated
// target (hyperlinked when it names a file), the faintest arg column, optional
// extra tag, the auto marker, and the status glyph.
func toolCardHead(name string, target string, arg string, headTag string, glyph string, auto bool, width int, opts cardRenderOptions) string {
	head := zeroTheme.toolName.Render(name)
	if target = strings.TrimSpace(target); target != "" {
		styled := zeroTheme.toolTarget.Render(middleTruncate(target, maxInt(16, width/2)))
		if looksLikePath(target) {
			styled = hyperlink(fileURL(opts.cwd, target), styled)
		}
		head += " " + styled
	}
	// The arg column is the first thing the width tiers drop (below 100 cols).
	if arg = strings.TrimSpace(arg); arg != "" && widthTier(width) == tierFull {
		head += "  " + zeroTheme.toolArg.Render(truncateRunes(arg, maxInt(12, width/3)))
	}
	if headTag != "" {
		head += "  " + zeroTheme.faint.Render(headTag)
	}
	if auto {
		head += "  " + zeroTheme.autoTag.Render("[auto]")
	}
	return head + "  " + glyph
}

// toolCard draws the rounded card: head embedded in the top border, optional
// footer embedded in the bottom border, body lines between on the panel
// surface. Every emitted line is exactly `width` cells. On tiny terminals the
// side borders go away (top/bottom rules stay) so content keeps the columns.
func toolCard(head string, body []string, footer string, borderStyle lipgloss.Style, width int) string {
	tiny := widthTier(width) == tierTiny
	if width < 24 {
		width = 24
	}
	innerWidth := width - 4
	if tiny {
		innerWidth = width
	}

	head = fitStyledLine(head, width-6)
	dashes := maxInt(1, width-4-lipgloss.Width(head))
	top := borderStyle.Render("╭ ") + head + " " + borderStyle.Render(strings.Repeat("─", dashes)+"╮")
	if tiny {
		top = head + " " + borderStyle.Render(strings.Repeat("─", maxInt(1, width-1-lipgloss.Width(head))))
	}

	lines := make([]string, 0, len(body)+2)
	lines = append(lines, top)
	for _, line := range body {
		fitted := fitStyledLine(line, innerWidth)
		if tiny {
			lines = append(lines, fitted)
			continue
		}
		pad := zeroTheme.panel.Render(strings.Repeat(" ", maxInt(0, innerWidth-lipgloss.Width(fitted))))
		lines = append(lines, borderStyle.Render("│ ")+fitted+pad+borderStyle.Render(" │"))
	}

	switch {
	case tiny && strings.TrimSpace(footer) == "":
		lines = append(lines, borderStyle.Render(strings.Repeat("─", width)))
	case tiny:
		footer = fitStyledLine(footer, width-4)
		lines = append(lines, footer+" "+borderStyle.Render(strings.Repeat("─", maxInt(1, width-1-lipgloss.Width(footer)))))
	case strings.TrimSpace(footer) == "":
		lines = append(lines, borderStyle.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	default:
		footer = fitStyledLine(footer, width-6)
		dashes = maxInt(1, width-4-lipgloss.Width(footer))
		lines = append(lines, borderStyle.Render("╰ ")+footer+" "+borderStyle.Render(strings.Repeat("─", dashes)+"╯"))
	}
	return strings.Join(lines, "\n")
}

// toolCardBody delegates result-shape selection to the tool body registry.
func toolCardBody(name string, hint string, detail string, width int, opts cardRenderOptions) cardBody {
	return defaultToolBodyRegistry.render(toolBodyRequest{
		name:   name,
		hint:   hint,
		detail: detail,
		width:  width,
		opts:   opts,
	})
}

// capCardLines applies the body cap, appending the hidden-count trailer when
// lines were dropped.
func capCardLines(lines []string, cap int) []string {
	if cap <= 0 || len(lines) <= cap {
		return lines
	}
	hidden := len(lines) - cap
	lines = lines[:cap]
	return append(lines, zeroTheme.onPanel(zeroTheme.faint).Render(fmt.Sprintf("… %d more lines", hidden)))
}

func genericCardBody(detail string, opts cardRenderOptions) cardBody {
	raw := strings.Split(detail, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, zeroTheme.onPanel(zeroTheme.muted).Render(line))
	}
	return cardBody{lines: capCardLines(lines, opts.bodyCap)}
}

// hunkHeaderPattern extracts the old/new start lines from a unified-diff hunk
// header so the gutter can show real line numbers.
var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func diffCardBody(detail string, width int, opts cardRenderOptions) cardBody {
	rawLines := strings.Split(detail, "\n")

	path := ""
	newFile := false
	adds, dels := 0, 0
	for _, line := range rawLines {
		switch {
		case strings.HasPrefix(line, "+++ "):
			path = strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")), "b/")
		case strings.HasPrefix(line, "--- "):
			if strings.TrimSpace(strings.TrimPrefix(line, "--- ")) == "/dev/null" {
				newFile = true
			}
		case strings.HasPrefix(line, "+"):
			adds++
		case strings.HasPrefix(line, "-"):
			dels++
		}
	}

	innerWidth := width - 4
	// The edited file's path is a clickable OSC 8 link, so the edited place in
	// history opens straight from the terminal.
	headLeft := hyperlink(fileURL(opts.cwd, path),
		zeroTheme.onPanel(zeroTheme.ink).Render(middleTruncate(path, maxInt(16, innerWidth/2))))
	if newFile {
		headLeft += zeroTheme.panel.Render("  ") + zeroTheme.addSign.Render(" NEW FILE ")
	}
	counts := []string{}
	if adds > 0 {
		counts = append(counts, zeroTheme.onPanel(zeroTheme.diffAdd).Render(fmt.Sprintf("+%d", adds)))
	}
	if dels > 0 {
		counts = append(counts, zeroTheme.onPanel(zeroTheme.diffDel).Render(fmt.Sprintf("−%d", dels)))
	}
	lines := []string{joinHeaderLine(headLeft, strings.Join(counts, " "), innerWidth)}

	// The line-number gutter drops below 80 cols (the 60–79 tier). With it,
	// gutter(4) + sign(3) + textBudget == innerWidth; without, sign(3) + text.
	gutter := widthTier(width) >= tierMedium
	gutterWidth := 0
	if gutter {
		gutterWidth = 4
	}
	textBudget := maxInt(8, innerWidth-3-gutterWidth)
	oldLine, newLine := 0, 0
	inHunk := false
	for _, line := range rawLines {
		switch {
		case strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "--- "):
			// Path already in the body head row.
		case strings.HasPrefix(line, "@@"):
			if match := hunkHeaderPattern.FindStringSubmatch(line); match != nil {
				oldLine, _ = strconv.Atoi(match[1])
				newLine, _ = strconv.Atoi(match[2])
				inHunk = true
			}
			lines = append(lines, zeroTheme.onPanel(zeroTheme.diffMeta).Render(truncateRunes(line, innerWidth)))
		case !inHunk, strings.HasPrefix(line, `\`):
			// Preamble ("diff --git", "index …", a stray "stdout:") and the
			// "\ No newline at end of file" marker are not content lines: no
			// gutter number, and the hunk counters must not advance.
			lines = append(lines, zeroTheme.onPanel(zeroTheme.diffMeta).Render(truncateRunes(line, innerWidth)))
		case strings.HasPrefix(line, "+"):
			text := truncateRunes(strings.TrimPrefix(line, "+"), textBudget)
			lines = append(lines, diffBodyLine(newLine, "+", text, true, textBudget, gutter))
			newLine++
		case strings.HasPrefix(line, "-"):
			text := truncateRunes(strings.TrimPrefix(line, "-"), textBudget)
			lines = append(lines, diffBodyLine(oldLine, "−", text, false, textBudget, gutter))
			oldLine++
		default:
			text := truncateRunes(strings.TrimPrefix(line, " "), textBudget)
			row := zeroTheme.panel.Render("   ") + zeroTheme.onPanel(zeroTheme.muted).Render(text)
			if gutter {
				row = zeroTheme.onPanel(zeroTheme.faintest).Render(fmt.Sprintf("%4d", newLine)) + row
			}
			lines = append(lines, row)
			oldLine++
			newLine++
		}
	}
	return cardBody{lines: capCardLines(lines, opts.bodyCap)}
}

// diffBodyLine paints one changed row: optional gutter number, sign column,
// and text padded to the full budget, all on the add/del tint so the row
// reads as one solid band edge to edge.
func diffBodyLine(number int, sign string, text string, added bool, textBudget int, gutter bool) string {
	if pad := textBudget - lipgloss.Width(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	numCol := ""
	if gutter {
		num := fmt.Sprintf("%4d", number)
		if added {
			numCol = zeroTheme.addLineNum.Render(num)
		} else {
			numCol = zeroTheme.delLineNum.Render(num)
		}
	}
	if added {
		return numCol + zeroTheme.addSign.Render(" "+sign+" ") + zeroTheme.addLine.Render(text)
	}
	return numCol + zeroTheme.delSign.Render(" "+sign+" ") + zeroTheme.delLine.Render(text)
}

// readNumberedLinePattern matches read_file's body rows, which the tool emits
// as "<right-aligned N> | <text>" (see internal/tools/read_file.go).
var readNumberedLinePattern = regexp.MustCompile(`^\s*(\d+) \| (.*)$`)

func readCardBody(detail string, width int, opts cardRenderOptions) cardBody {
	// The number gutter drops with the diff gutter below 80 cols.
	gutter := widthTier(width) >= tierMedium
	raw := strings.Split(detail, "\n")
	lines := make([]string, 0, len(raw))
	first, last := 0, 0
	for _, line := range raw {
		if strings.HasPrefix(line, "File: ") || strings.TrimSpace(line) == "" {
			continue
		}
		if match := readNumberedLinePattern.FindStringSubmatch(line); match != nil {
			number, _ := strconv.Atoi(match[1])
			if first == 0 {
				first = number
			}
			last = number
			row := zeroTheme.onPanel(zeroTheme.muted).Render(match[2])
			if gutter {
				row = zeroTheme.onPanel(zeroTheme.faintest).Render(fmt.Sprintf("%4s", match[1])) + zeroTheme.panel.Render(" ") + row
			}
			lines = append(lines, row)
			continue
		}
		lines = append(lines, zeroTheme.onPanel(zeroTheme.muted).Render(line))
	}
	headTag := ""
	if first > 0 && last >= first {
		headTag = fmt.Sprintf("L%d–L%d", first, last)
	}
	return cardBody{lines: capCardLines(lines, opts.bodyCap), headTag: headTag}
}

func bashCardBody(command string, detail string, width int, opts cardRenderOptions) cardBody {
	innerWidth := width - 4
	lines := []string{}
	if command = strings.TrimSpace(command); command != "" {
		lines = append(lines, zeroTheme.onPanel(zeroTheme.bashPrompt).Render("❯ ")+zeroTheme.onPanel(zeroTheme.ink).Render(truncateRunes(command, maxInt(8, innerWidth-2))))
		lines = append(lines, zeroTheme.onPanel(zeroTheme.line).Render(strings.Repeat("─", maxInt(1, innerWidth))))
	}

	footer := ""
	section := "stdout"
	for _, line := range strings.Split(detail, "\n") {
		switch {
		case line == "stdout:":
			section = "stdout"
		case line == "stderr:":
			section = "stderr"
		case strings.HasPrefix(line, "exit_code: "):
			code := strings.TrimPrefix(line, "exit_code: ")
			if code == "0" {
				footer = zeroTheme.green.Render("exit 0")
			} else {
				footer = zeroTheme.red.Render("exit " + code)
			}
		default:
			style := zeroTheme.muted
			if section == "stderr" {
				style = zeroTheme.delText
			}
			lines = append(lines, zeroTheme.panel.Render("  ")+zeroTheme.onPanel(style).Render(line))
		}
	}
	return cardBody{lines: capCardLines(lines, opts.bodyCap), footer: footer}
}

// renderSessionsCards draws the /resume list as stacked cards: id (accent) +
// age (faint) on the top row, title (ink), then the meta line (faint with
// faintest dots). Records arrive as sessionsCardFieldSep-joined fields; a
// record without separators is a plain trailing hint.
func renderSessionsCards(payload string, width int) string {
	blocks := []string{}
	for _, record := range strings.Split(payload, "\n") {
		fields := strings.Split(record, sessionsCardFieldSep)
		if len(fields) < 4 {
			blocks = append(blocks, fitStyledLine(zeroTheme.faint.Render(record), width))
			continue
		}
		id, age, title, meta := fields[0], fields[1], fields[2], fields[3]
		innerWidth := width - 4
		top := joinHeaderLine(zeroTheme.onPanel(zeroTheme.accent).Render(id), zeroTheme.onPanel(zeroTheme.faint).Render(age), innerWidth)
		metaParts := strings.Split(meta, " · ")
		for index := range metaParts {
			metaParts[index] = zeroTheme.onPanel(zeroTheme.faint).Render(metaParts[index])
		}
		lines := []string{
			top,
			zeroTheme.onPanel(zeroTheme.ink).Render(title),
			strings.Join(metaParts, zeroTheme.onPanel(zeroTheme.faintest).Render(" · ")),
		}
		blocks = append(blocks, styledBlockFill(width, lines, zeroTheme.line, zeroTheme.panel))
	}
	return strings.Join(blocks, "\n")
}

// grepMatchPattern matches the grep tool's "path:line: text" content rows.
var grepMatchPattern = regexp.MustCompile(`^(.+?:\d+):\s?(.*)$`)

func grepCardBody(detail string, width int, opts cardRenderOptions) cardBody {
	innerWidth := width - 4
	raw := strings.Split(detail, "\n")
	lines := make([]string, 0, len(raw))
	matches := 0
	for _, line := range raw {
		if match := grepMatchPattern.FindStringSubmatch(line); match != nil {
			matches++
			location := zeroTheme.onPanel(zeroTheme.grepLoc).Render(match[1])
			// match[1] is "path:line" — link the file so a hit is one click away.
			if path, _, ok := strings.Cut(match[1], ":"); ok && path != "" {
				location = hyperlink(fileURL(opts.cwd, path), location)
			}
			budget := maxInt(8, innerWidth-lipgloss.Width(match[1])-2)
			lines = append(lines, location+zeroTheme.panel.Render("  ")+zeroTheme.onPanel(zeroTheme.muted).Render(truncateRunes(match[2], budget)))
			continue
		}
		lines = append(lines, zeroTheme.onPanel(zeroTheme.muted).Render(line))
	}
	footer := ""
	if matches > 0 {
		footer = zeroTheme.faint.Render(fmt.Sprintf("%d matches", matches))
	}
	return cardBody{lines: capCardLines(lines, opts.bodyCap), footer: footer}
}
