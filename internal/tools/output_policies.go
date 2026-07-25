package tools

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// budgetSemanticOutput applies the deterministic policy for category. The
// caller supplies output after secret redaction. Small results pass through
// byte-identically; only oversized results are semantically reduced.
func budgetSemanticOutput(output string, category outputCategory, budget outputBudget) budgetedOutput {
	if category == "" {
		category = outputCategoryDefault
	}
	if fitsOutputBudget(output, budget) {
		return unchangedBudgetedOutput(output, category)
	}

	var retained string
	switch category {
	case outputCategoryFile:
		retained = budgetFileLines(output, budget)
	case outputCategorySearch:
		retained = budgetSearchLines(output, budget)
	case outputCategoryTest:
		retained = budgetTestLines(output, budget)
	case outputCategoryProcess:
		retained = budgetProcessLines(output, budget)
	case outputCategoryDiff:
		retained = budgetDiffUnits(output, budget)
	case outputCategoryWorker:
		retained = budgetWorkerLines(output, budget)
	default:
		return budgetDefaultOutput(output, budget)
	}

	if retained == "" || !fitsOutputBudget(retained, budget) {
		fallback := budgetDefaultOutput(output, budget)
		fallback.category = category
		return fallback
	}
	return truncatedBudgetedOutput(output, retained, category, "semantic_"+string(category)+"_budget")
}

func unchangedBudgetedOutput(output string, category outputCategory) budgetedOutput {
	estimated := estimateOutputTokens(output)
	return budgetedOutput{
		text:                    output,
		originalBytes:           len(output),
		retainedBytes:           len(output),
		estimatedOriginalTokens: estimated,
		estimatedRetainedTokens: estimated,
		category:                category,
	}
}

func truncatedBudgetedOutput(original, retained string, category outputCategory, reason string) budgetedOutput {
	return budgetedOutput{
		text:                    retained,
		originalBytes:           len(original),
		retainedBytes:           len(retained),
		estimatedOriginalTokens: estimateOutputTokens(original),
		estimatedRetainedTokens: estimateOutputTokens(retained),
		truncated:               true,
		category:                category,
		reason:                  reason,
	}
}

func budgetFileLines(output string, budget outputBudget) string {
	lines := outputLines(output)
	priorities := make([]int, 0, len(lines))
	// Keep the existing file/range header first, then alternate from the start
	// and end so both requested-location boundaries survive.
	if len(lines) > 0 {
		priorities = append(priorities, 0)
	}
	for left, right := 1, len(lines)-1; left <= right; left, right = left+1, right-1 {
		priorities = append(priorities, left)
		if right != left {
			priorities = append(priorities, right)
		}
	}
	units := lineDiffUnits(lines)
	selected := selectPrioritizedUnits(units, priorities, budget)
	for index := range selected {
		// Index zero is the file/range header. If no non-blank requested
		// content line fits as a complete line, let the common caller fall
		// back to its UTF-8-safe head/tail policy rather than returning only
		// a header and omission marker for a minified single-line file.
		if index > 0 && strings.TrimSpace(lines[index]) != "" {
			return renderSelectedUnits(units, selected)
		}
	}
	return ""
}

func budgetSearchLines(output string, budget outputBudget) string {
	lines := collapseConsecutiveDuplicateLines(outputLines(output))
	priorities := make([]int, 0, len(lines))
	for index, line := range lines {
		lower := strings.ToLower(line)
		// A match body naturally contains the search term (often literally
		// "match"); treat only non-result lines as summaries so one busy file
		// cannot consume the budget before cross-file representatives are chosen.
		if searchResultFile(line) == "" && (strings.Contains(lower, "match") || strings.Contains(lower, "truncated") || strings.Contains(lower, "result")) {
			priorities = append(priorities, index)
		}
	}
	if len(lines) > 0 {
		priorities = append(priorities, 0, len(lines)-1)
	}
	seenFiles := map[string]bool{}
	for index, line := range lines {
		if file := searchResultFile(line); file != "" && !seenFiles[file] {
			seenFiles[file] = true
			priorities = append(priorities, index)
		}
	}
	priorities = append(priorities, sequence(len(lines))...)
	return retainPrioritizedLines(lines, priorities, budget)
}

func budgetTestLines(output string, budget outputBudget) string {
	lines := collapseConsecutiveDuplicateLines(outputLines(output))
	priorities := make([]int, 0, len(lines))
	for index, line := range lines {
		if isTestFailureLine(line) {
			for contextIndex := max(0, index-2); contextIndex <= min(len(lines)-1, index+3); contextIndex++ {
				priorities = append(priorities, contextIndex)
			}
		}
	}
	// Final summaries and process status usually live at the tail.
	for index := max(0, len(lines)-16); index < len(lines); index++ {
		priorities = append(priorities, index)
	}
	for index := 0; index < min(8, len(lines)); index++ {
		priorities = append(priorities, index)
	}
	priorities = append(priorities, sequence(len(lines))...)
	return retainPrioritizedLines(lines, priorities, budget)
}

func budgetProcessLines(output string, budget outputBudget) string {
	lines := collapseConsecutiveDuplicateLines(outputLines(output))
	priorities := make([]int, 0, len(lines))
	for index := 0; index < min(10, len(lines)); index++ {
		priorities = append(priorities, index)
	}
	seenDiagnostic := map[string]bool{}
	for index, line := range lines {
		lower := strings.ToLower(line)
		if containsAny(lower, "error", "warning", "warn:", "fatal", "panic", "failed", "denied") && !seenDiagnostic[line] {
			seenDiagnostic[line] = true
			priorities = append(priorities, index)
		}
	}
	for index := max(0, len(lines)-16); index < len(lines); index++ {
		priorities = append(priorities, index)
	}
	priorities = append(priorities, sequence(len(lines))...)
	return retainPrioritizedLines(lines, priorities, budget)
}

func budgetWorkerLines(output string, budget outputBudget) string {
	lines := collapseConsecutiveDuplicateLines(outputLines(output))
	priorities := make([]int, 0, len(lines))
	for index, line := range lines {
		lower := strings.ToLower(line)
		if containsAny(lower, "status", "error", "failed", "failure", "session_id", "changed", "files", "tools executed", "conclusion", "result") {
			priorities = append(priorities, index)
		}
	}
	// A specialist's final conclusion is conventionally at the end.
	for index := max(0, len(lines)-20); index < len(lines); index++ {
		priorities = append(priorities, index)
	}
	for index := 0; index < min(6, len(lines)); index++ {
		priorities = append(priorities, index)
	}
	priorities = append(priorities, sequence(len(lines))...)
	return retainPrioritizedLines(lines, priorities, budget)
}

type diffUnit struct {
	order int
	text  string
	key   bool
}

func budgetDiffUnits(output string, budget outputBudget) string {
	units := splitDiffUnits(output)
	if len(units) == 0 {
		return ""
	}
	priority := make([]int, 0, len(units))
	for index, unit := range units {
		if unit.key {
			priority = append(priority, index)
		}
	}
	priority = append(priority, sequence(len(units))...)
	return retainPrioritizedUnits(units, priority, budget)
}

// splitDiffUnits keeps each hunk indivisible. File headers/stat text are key
// units so broad file coverage is selected before secondary hunks.
func splitDiffUnits(output string) []diffUnit {
	lines := outputLines(output)
	units := make([]diffUnit, 0)
	var current []string
	currentKey := false
	flush := func() {
		if len(current) == 0 {
			return
		}
		units = append(units, diffUnit{order: len(units), text: strings.Join(current, "\n"), key: currentKey})
		current = nil
		currentKey = false
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			flush()
			currentKey = true
			current = append(current, line)
			continue
		}
		if strings.HasPrefix(line, "@@ ") {
			flush()
			current = append(current, line)
			continue
		}
		if len(current) == 0 {
			currentKey = true // diff stat/summary or standalone file headers
		}
		current = append(current, line)
	}
	flush()
	return units
}

func retainPrioritizedLines(lines []string, priorities []int, budget outputBudget) string {
	return retainPrioritizedUnits(lineDiffUnits(lines), priorities, budget)
}

func lineDiffUnits(lines []string) []diffUnit {
	units := make([]diffUnit, 0, len(lines))
	for index, line := range lines {
		units = append(units, diffUnit{order: index, text: line})
	}
	return units
}

func retainPrioritizedUnits(units []diffUnit, priorities []int, budget outputBudget) string {
	return renderSelectedUnits(units, selectPrioritizedUnits(units, priorities, budget))
}

func selectPrioritizedUnits(units []diffUnit, priorities []int, budget outputBudget) map[int]bool {
	selected := map[int]bool{}
	indexes := make([]int, 0, len(priorities))
	cost := retainedUnitCost{}
	for _, index := range priorities {
		if index < 0 || index >= len(units) || selected[index] {
			continue
		}
		position := sort.SearchInts(indexes, index)
		previous, next := -1, len(units)
		if position > 0 {
			previous = indexes[position-1]
		}
		if position < len(indexes) {
			next = indexes[position]
		}

		candidate := cost
		candidate.add(units[index].text)
		if len(indexes) == 0 {
			candidate.addOmission(index)
			candidate.addOmission(len(units) - index - 1)
		} else {
			candidate.removeOmission(next - previous - 1)
			candidate.addOmission(index - previous - 1)
			candidate.addOmission(next - index - 1)
		}
		if !candidate.fits(budget) {
			continue
		}
		selected[index] = true
		indexes = append(indexes, 0)
		copy(indexes[position+1:], indexes[position:])
		indexes[position] = index
		cost = candidate
	}
	return selected
}

// retainedUnitCost tracks the exact rendered size of selected units without
// repeatedly rebuilding the full candidate text. Newlines between rendered
// parts add bytes but no estimated tokens, so the token components remain
// additive even as priorities select units out of source order.
type retainedUnitCost struct {
	textBytes     int
	asciiNonSpace int
	nonASCIIBytes int
	parts         int
}

func (cost *retainedUnitCost) add(text string) {
	ascii, nonASCII := outputTokenComponents(text)
	cost.textBytes += len(text)
	cost.asciiNonSpace += ascii
	cost.nonASCIIBytes += nonASCII
	cost.parts++
}

func (cost *retainedUnitCost) remove(text string) {
	ascii, nonASCII := outputTokenComponents(text)
	cost.textBytes -= len(text)
	cost.asciiNonSpace -= ascii
	cost.nonASCIIBytes -= nonASCII
	cost.parts--
}

func (cost *retainedUnitCost) addOmission(count int) {
	if count > 0 {
		cost.add(omittedSectionsMarker(count))
	}
}

func (cost *retainedUnitCost) removeOmission(count int) {
	if count > 0 {
		cost.remove(omittedSectionsMarker(count))
	}
}

func (cost retainedUnitCost) fits(budget outputBudget) bool {
	bytes := cost.textBytes + max(0, cost.parts-1)
	tokens := (cost.asciiNonSpace+3)/4 + cost.nonASCIIBytes
	return (budget.hardMaxBytes <= 0 || bytes <= budget.hardMaxBytes) &&
		(budget.maxEstimatedTokens <= 0 || tokens <= budget.maxEstimatedTokens)
}

func omittedSectionsMarker(count int) string {
	return fmt.Sprintf("[zero] ... %d section(s) omitted ...", count)
}

func renderSelectedUnits(units []diffUnit, selected map[int]bool) string {
	indexes := make([]int, 0, len(selected))
	for index := range selected {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	if len(indexes) == 0 {
		return ""
	}
	parts := make([]string, 0, len(indexes)*2)
	previous := -1
	for _, index := range indexes {
		if previous >= 0 && index != previous+1 {
			omitted := index - previous - 1
			parts = append(parts, omittedSectionsMarker(omitted))
		} else if previous < 0 && index > 0 {
			parts = append(parts, omittedSectionsMarker(index))
		}
		parts = append(parts, units[index].text)
		previous = index
	}
	if previous < len(units)-1 {
		parts = append(parts, omittedSectionsMarker(len(units)-previous-1))
	}
	return strings.Join(parts, "\n")
}

func outputLines(output string) []string {
	return strings.Split(strings.TrimRight(output, "\r\n"), "\n")
}

func collapseConsecutiveDuplicateLines(lines []string) []string {
	if len(lines) < 2 {
		return lines
	}
	result := make([]string, 0, len(lines))
	for index := 0; index < len(lines); {
		end := index + 1
		for end < len(lines) && lines[end] == lines[index] {
			end++
		}
		result = append(result, lines[index])
		if count := end - index; count > 1 {
			result = append(result, fmt.Sprintf("[zero] previous line repeated %d more time(s)", count-1))
		}
		index = end
	}
	return result
}

func searchResultFile(line string) string {
	// Search output is path:line: text. Locate the first numeric field from
	// the left after an optional Windows drive prefix. Match text may itself
	// contain numeric colon-delimited fields, which are not record structure.
	searchFrom := 0
	if len(line) >= 2 && line[1] == ':' && ((line[0] >= 'A' && line[0] <= 'Z') || (line[0] >= 'a' && line[0] <= 'z')) {
		searchFrom = 2
	}
	for searchFrom < len(line) {
		lineStartOffset := strings.IndexByte(line[searchFrom:], ':')
		if lineStartOffset < 0 {
			return ""
		}
		lineStart := searchFrom + lineStartOffset
		lineEndOffset := strings.IndexByte(line[lineStart+1:], ':')
		if lineEndOffset < 0 {
			return ""
		}
		lineEnd := lineStart + 1 + lineEndOffset
		if _, err := strconv.Atoi(line[lineStart+1 : lineEnd]); err == nil {
			return strings.TrimSpace(line[:lineStart])
		}
		searchFrom = lineStart + 1
	}
	return ""
}

func isTestFailureLine(line string) bool {
	lower := strings.ToLower(line)
	return containsAny(lower, "--- fail:", "failed", "failure", "panic", "assert", "error", "fatal", "expected", "actual:")
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func sequence(length int) []int {
	result := make([]int, length)
	for index := range result {
		result[index] = index
	}
	return result
}
