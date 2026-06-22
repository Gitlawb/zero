// phase_indicator.go renders a compact "↑ from-to/total ↓" scroll indicator for
// the live working status line. It tells the user, at a glance, which slice of
// the in-flight run's transcript the working line is reporting on, using the
// tmux/Emacs mode-line convention: an up arrow when earlier rows exist, a down
// arrow when later rows exist, both blanked at the respective edges.
package tui

import "strings"

// phaseScrollIndicator renders a compact "↑ from-to/total ↓" string. The arrows
// communicate which scroll direction is still reachable from the current
// position:
//
//   - The up-arrow "↑" appears only when from > 1, i.e. there are earlier rows
//     above the current turn boundary. It is replaced by a single space when
//     from == 1 ("at the top, nothing to scroll up to").
//   - The down-arrow "↓" appears only when to < total, i.e. there are later
//     rows below the current position. It is replaced by a single space when
//     to == total ("at the bottom").
//
// Trivial inputs collapse:
//
//   - total <= 0  ->  returns "" (nothing to indicate)
//   - from < 1    ->  clamps from to 1
//   - to > total  ->  clamps to to total
//   - to < from   ->  clamps to to from
//
// The function is intentionally pure: three ints in, a string out, no model
// state and no rendering-style side effects, so it is trivially unit-testable
// and reusable from any caller.
func phaseScrollIndicator(from, to, total int) string {
	if total <= 0 {
		return ""
	}
	if from < 1 {
		from = 1
	}
	if to < from {
		to = from
	}
	if to > total {
		to = total
	}

	var b strings.Builder
	b.WriteString(" ")
	if from > 1 {
		b.WriteString("↑")
	} else {
		b.WriteString(" ")
	}
	b.WriteString(" ")
	b.WriteString(itoa(from))
	b.WriteString("-")
	b.WriteString(itoa(to))
	b.WriteString("/")
	b.WriteString(itoa(total))
	b.WriteString(" ")
	if to < total {
		b.WriteString("↓")
	} else {
		b.WriteString(" ")
	}
	return b.String()
}

// itoa is a bare, dependency-free equivalent of strconv.Itoa, keeping this file
// to a single import ("strings") so the indicator logic is a small auditable
// surface.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// transcriptPhaseIndicator derives (from, to, total) from m.transcript and
// renders phaseScrollIndicator for inline use by the working status line. On an
// empty transcript it returns "" so callers can skip the indicator without a
// second nil check.
func (m model) transcriptPhaseIndicator() string {
	total := len(m.transcript)
	if total == 0 {
		return ""
	}
	// Find the boundary of the turn currently in flight: walk the transcript
	// backwards and stop at the first turn-starting row (rowUser, rowAssistant,
	// rowSystem, rowError per startsTurn). That row's 1-indexed position is
	// "from".
	from := total // fallback: a transcript with no turn start is one phase
	for i := total - 1; i >= 0; i-- {
		if startsTurn(m.transcript[i].kind) {
			from = i + 1 // 1-indexed
			break
		}
	}
	to := total
	return phaseScrollIndicator(from, to, total)
}
