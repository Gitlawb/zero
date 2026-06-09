package tui

import (
	"fmt"
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

func (m model) runState() string {
	if m.pending {
		return "running"
	}
	return "ready"
}

// pickerBusyText explains that a settings picker (/model, /mode, /effort, /theme)
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

const defaultCommandFooterText = "/help  /model  /provider  /context  /compact  /effort  /style  /tools  /permissions  /clear  /exit  Esc clear  Ctrl+C quit"

func commandFooterText() string {
	return formatCommandFooterText(commandDefinitions, false)
}

func (m model) footerText() string {
	return strings.Join([]string{
		m.runState(),
		displayValue(m.modelName, "model:none"),
		m.usageSummaryText(),
		formatCommandFooterText(commandDefinitions, m.pending),
	}, "  ")
}

func formatCommandFooterText(commands []commandDefinition, pending bool) string {
	if len(commands) == 0 {
		return defaultCommandFooterText
	}

	namesByKind := make(map[commandKind]string, len(commands))
	for _, command := range commands {
		namesByKind[command.kind] = command.name
	}

	featured := []commandKind{
		commandHelp,
		commandModel,
		commandProvider,
		commandContext,
		commandCompact,
		commandEffort,
		commandStyle,
		commandTools,
		commandPermissions,
		commandClear,
		commandExit,
	}
	parts := make([]string, 0, len(featured)+2)
	for _, kind := range featured {
		name := namesByKind[kind]
		if name != "" {
			parts = append(parts, name)
		}
	}
	if len(parts) == 0 {
		return defaultCommandFooterText
	}

	if pending {
		parts = append(parts, "Esc cancel")
	} else {
		parts = append(parts, "Esc clear")
	}
	parts = append(parts, "Ctrl+C quit")
	return strings.Join(parts, "  ")
}

// rowContext carries the cross-row knowledge renderRow needs: which tool
// calls already have results (their call rows collapse into the result card),
// each call's argument hints for the card head, and which calls were
// auto-approved (by permission mode or a stored grant).
type rowContext struct {
	resolved map[string]bool
	hints    map[string]string
	args     map[string]string
	auto     map[string]bool
	decided  map[string]bool
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
				rc.hints[row.id] = strings.TrimSpace(row.detail)
				rc.args[row.id] = strings.TrimSpace(row.arg)
			}
		case rowToolResult:
			if row.id != "" {
				rc.resolved[row.id] = true
			}
		case rowPermission:
			event := row.permission
			if event == nil || event.ToolCallID == "" {
				continue
			}
			switch event.Action {
			case agent.PermissionActionPrompt:
				prompted[event.ToolCallID] = true
				delete(rc.auto, event.ToolCallID)
			case agent.PermissionActionAllow:
				rc.decided[event.ToolCallID] = true
				// "auto" means approved without asking: a mode/policy allow or a
				// stored grant match. Any allow that followed a prompt — including a
				// first-time "always" — was a manual decision, not auto.
				if !prompted[event.ToolCallID] {
					rc.auto[event.ToolCallID] = true
				}
			case agent.PermissionActionDeny:
				rc.decided[event.ToolCallID] = true
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
		return row.id != "" && rc.resolved[row.id]
	case rowPermission:
		event := row.permission
		if event == nil || event.ToolCallID == "" {
			return false
		}
		switch event.Action {
		case agent.PermissionActionPrompt:
			return rc.decided[event.ToolCallID]
		case agent.PermissionActionAllow:
			return rc.auto[event.ToolCallID]
		}
	}
	return false
}

func (m model) renderRow(row transcriptRow, width int, rc rowContext) string {
	switch row.kind {
	case rowWelcome:
		return zeroTheme.muted.Render(row.text)
	case rowUser:
		return renderUserRow(row, width)
	case rowAssistant:
		return renderAssistantRow(row, width)
	case rowSystem:
		if row.tool == "sessions" {
			return renderSessionsCards(row.text, width)
		}
		return renderSystemNote(row.text, width)
	case rowError:
		return renderErrorRow(row, width)
	case rowToolCall:
		return m.renderRunningToolCard(row, width, rc)
	case rowToolResult:
		return renderToolResultCard(row, width, rc)
	case rowPermission:
		return renderPermissionRow(row, width)
	case rowAskUser:
		return renderAskUserRow(row)
	default:
		return row.text
	}
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
// newlines. Words longer than the measure are hard-split so no emitted line
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
		line := ""
		for _, word := range strings.Fields(paragraph) {
			for lipgloss.Width(word) > measure {
				if line != "" {
					out = append(out, line)
					line = ""
				}
				head, tail := splitAtWidth(word, measure)
				out = append(out, head)
				word = tail
			}
			switch {
			case line == "":
				line = word
			case lipgloss.Width(line)+1+lipgloss.Width(word) <= measure:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
		}
		if line != "" {
			out = append(out, line)
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
	lines := wrapPlainText(row.text, sayMeasure(width))
	if !row.final {
		for index := range lines {
			lines[index] = zeroTheme.sayText.Render(lines[index])
		}
		return strings.Join(lines, "\n")
	}
	for index := range lines {
		lines[index] = zeroTheme.finalRail.Render("│ ") + zeroTheme.ink.Render(lines[index])
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

func renderAskUserRow(row transcriptRow) string {
	line := zeroTheme.accent.Render("ask zero") + "  " + zeroTheme.ink.Render(strings.TrimPrefix(row.text, "ask_user: "))
	if detail := strings.TrimSpace(row.detail); detail != "" {
		line += "\n" + indentText(zeroTheme.muted.Render(detail), 2)
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
		if event.Grant != nil {
			label = "always"
		}
		return zeroTheme.green.Render(label) + dot + zeroTheme.green.Render(name)
	case agent.PermissionActionDeny:
		line := zeroTheme.red.Render("denied") + dot + zeroTheme.red.Render(name)
		if event.Risk.Level != "" {
			line += dot + zeroTheme.muted.Render("risk:"+string(event.Risk.Level))
		}
		if reason := strings.TrimSpace(event.Reason); reason != "" {
			line += zeroTheme.faint.Render(" — " + truncateRunes(reason, maxInt(16, width-lipgloss.Width(name)-16)))
		}
		if detail := strings.TrimSpace(row.detail); detail != "" {
			line += "\n" + indentText(zeroTheme.muted.Render(detail), 2)
		}
		return line
	default:
		line := zeroTheme.amber.Render("permission") + "  " + zeroTheme.ink.Render(name) + "  " + zeroTheme.amber.Render("prompt")
		if event.Risk.Level != "" {
			line += "  " + zeroTheme.muted.Render("risk:" + string(event.Risk.Level))
		}
		if detail := strings.TrimSpace(row.detail); detail != "" {
			line += "\n" + indentText(zeroTheme.muted.Render(detail), 2)
		}
		return line
	}
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

	actions := zeroTheme.badge.Render(" [a] allow once ") +
		fill(zeroTheme.ink).Render(" ") +
		fill(zeroTheme.accent).Render("[y]") + fill(zeroTheme.ink).Render(" always ") +
		fill(zeroTheme.red).Render("[d]") + fill(zeroTheme.ink).Render(" deny ") +
		fill(zeroTheme.faint).Render("[esc]")
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
func (m model) renderRunningToolCard(row transcriptRow, width int, rc rowContext) string {
	glyph := zeroTheme.faintest.Render("…")
	if m.pending && row.runID != 0 && row.runID == m.activeRunID {
		glyph = m.spinner.View()
	}
	// The call row carries its own argHints; rc.hints/args only matter for
	// result rows, whose detail is the tool output.
	hint := strings.TrimSpace(row.detail)
	if hint == "" {
		hint = rc.hints[row.id]
	}
	arg := strings.TrimSpace(row.arg)
	if arg == "" {
		arg = rc.args[row.id]
	}
	head := toolCardHead(toolRowName(row), hint, arg, "", glyph, rc.auto[row.id], width)
	return toolCard(head, nil, "", zeroTheme.cardRun, width)
}

func renderToolResultCard(row transcriptRow, width int, rc rowContext) string {
	name := toolRowName(row)
	failed := row.status == tools.StatusError
	glyph := zeroTheme.green.Render("✓")
	borderStyle := zeroTheme.line
	if failed {
		glyph = zeroTheme.red.Render("✗")
		borderStyle = zeroTheme.cardErr
	}
	body := toolCardBody(name, rc.hints[row.id], row.detail, width)
	head := toolCardHead(name, rc.hints[row.id], rc.args[row.id], body.headTag, glyph, rc.auto[row.id], width)
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
// target, the faintest arg column, optional extra tag, the auto marker, and
// the status glyph.
func toolCardHead(name string, target string, arg string, headTag string, glyph string, auto bool, width int) string {
	head := zeroTheme.toolName.Render(name)
	if target = strings.TrimSpace(target); target != "" {
		head += " " + zeroTheme.toolTarget.Render(middleTruncate(target, maxInt(16, width/2)))
	}
	if arg = strings.TrimSpace(arg); arg != "" {
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
// surface. Every emitted line is exactly `width` cells.
func toolCard(head string, body []string, footer string, borderStyle lipgloss.Style, width int) string {
	if width < 24 {
		width = 24
	}
	innerWidth := width - 4

	head = fitStyledLine(head, width-6)
	dashes := maxInt(1, width-4-lipgloss.Width(head))
	top := borderStyle.Render("╭ ") + head + " " + borderStyle.Render(strings.Repeat("─", dashes)+"╮")

	lines := make([]string, 0, len(body)+2)
	lines = append(lines, top)
	for _, line := range body {
		fitted := fitStyledLine(line, innerWidth)
		pad := zeroTheme.panel.Render(strings.Repeat(" ", maxInt(0, innerWidth-lipgloss.Width(fitted))))
		lines = append(lines, borderStyle.Render("│ ")+fitted+pad+borderStyle.Render(" │"))
	}

	if strings.TrimSpace(footer) == "" {
		lines = append(lines, borderStyle.Render("╰"+strings.Repeat("─", width-2)+"╯"))
	} else {
		footer = fitStyledLine(footer, width-6)
		dashes = maxInt(1, width-4-lipgloss.Width(footer))
		lines = append(lines, borderStyle.Render("╰ ")+footer+" "+borderStyle.Render(strings.Repeat("─", dashes)+"╯"))
	}
	return strings.Join(lines, "\n")
}

// toolCardBody picks the body renderer by result shape, reusing the existing
// diff detection; the other shapes key off the core tool names.
func toolCardBody(name string, hint string, detail string, width int) cardBody {
	detail = strings.TrimRight(strings.ReplaceAll(detail, "\r\n", "\n"), "\n")
	if strings.TrimSpace(detail) == "" {
		return cardBody{}
	}
	switch {
	case looksLikeDiff(detail):
		return diffCardBody(detail, width)
	case name == "read_file":
		return readCardBody(detail)
	case name == "bash":
		return bashCardBody(hint, detail, width)
	case name == "grep":
		return grepCardBody(detail, width)
	default:
		return genericCardBody(detail)
	}
}

// capCardLines applies the shared body cap, appending the hidden-count
// trailer when lines were dropped.
func capCardLines(lines []string) []string {
	if len(lines) <= cardBodyMaxLines {
		return lines
	}
	hidden := len(lines) - cardBodyMaxLines
	lines = lines[:cardBodyMaxLines]
	return append(lines, zeroTheme.onPanel(zeroTheme.faint).Render(fmt.Sprintf("… %d more lines", hidden)))
}

func genericCardBody(detail string) cardBody {
	raw := strings.Split(detail, "\n")
	lines := make([]string, 0, len(raw))
	for _, line := range raw {
		lines = append(lines, zeroTheme.onPanel(zeroTheme.muted).Render(line))
	}
	return cardBody{lines: capCardLines(lines)}
}

// hunkHeaderPattern extracts the old/new start lines from a unified-diff hunk
// header so the gutter can show real line numbers.
var hunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

func diffCardBody(detail string, width int) cardBody {
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
	headLeft := zeroTheme.onPanel(zeroTheme.ink).Render(middleTruncate(path, maxInt(16, innerWidth/2)))
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

	// textBudget leaves room for the 4-col gutter, the sign column, and spaces:
	// 4 + 3 + textBudget == innerWidth, so tinted rows span the full card body.
	textBudget := maxInt(8, innerWidth-7)
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
			lines = append(lines, diffBodyLine(newLine, "+", text, true, textBudget))
			newLine++
		case strings.HasPrefix(line, "-"):
			text := truncateRunes(strings.TrimPrefix(line, "-"), textBudget)
			lines = append(lines, diffBodyLine(oldLine, "−", text, false, textBudget))
			oldLine++
		default:
			text := truncateRunes(strings.TrimPrefix(line, " "), textBudget)
			lines = append(lines, zeroTheme.onPanel(zeroTheme.faintest).Render(fmt.Sprintf("%4d", newLine))+zeroTheme.panel.Render("   ")+zeroTheme.onPanel(zeroTheme.muted).Render(text))
			oldLine++
			newLine++
		}
	}
	return cardBody{lines: capCardLines(lines)}
}

// diffBodyLine paints one changed row: gutter number, sign column, and text
// padded to the full budget, all on the add/del tint so the row reads as one
// solid band edge to edge.
func diffBodyLine(number int, sign string, text string, added bool, textBudget int) string {
	gutter := fmt.Sprintf("%4d", number)
	if pad := textBudget - lipgloss.Width(text); pad > 0 {
		text += strings.Repeat(" ", pad)
	}
	if added {
		return zeroTheme.addLineNum.Render(gutter) + zeroTheme.addSign.Render(" "+sign+" ") + zeroTheme.addLine.Render(text)
	}
	return zeroTheme.delLineNum.Render(gutter) + zeroTheme.delSign.Render(" "+sign+" ") + zeroTheme.delLine.Render(text)
}

// readNumberedLinePattern matches read_file's body rows, which the tool emits
// as "<right-aligned N> | <text>" (see internal/tools/read_file.go).
var readNumberedLinePattern = regexp.MustCompile(`^\s*(\d+) \| (.*)$`)

func readCardBody(detail string) cardBody {
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
			lines = append(lines, zeroTheme.onPanel(zeroTheme.faintest).Render(fmt.Sprintf("%4s", match[1]))+zeroTheme.panel.Render(" ")+zeroTheme.onPanel(zeroTheme.muted).Render(match[2]))
			continue
		}
		lines = append(lines, zeroTheme.onPanel(zeroTheme.muted).Render(line))
	}
	headTag := ""
	if first > 0 && last >= first {
		headTag = fmt.Sprintf("L%d–L%d", first, last)
	}
	return cardBody{lines: capCardLines(lines), headTag: headTag}
}

func bashCardBody(command string, detail string, width int) cardBody {
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
	return cardBody{lines: capCardLines(lines), footer: footer}
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
			blocks = append(blocks, zeroTheme.faint.Render(record))
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

func grepCardBody(detail string, width int) cardBody {
	innerWidth := width - 4
	raw := strings.Split(detail, "\n")
	lines := make([]string, 0, len(raw))
	matches := 0
	for _, line := range raw {
		if match := grepMatchPattern.FindStringSubmatch(line); match != nil {
			matches++
			location := zeroTheme.onPanel(zeroTheme.grepLoc).Render(match[1])
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
	return cardBody{lines: capCardLines(lines), footer: footer}
}
