package lsp

import (
	"fmt"
	"strings"
)

// severityLabel maps the LSP severity enum to a short human label. An unset
// severity (0) is treated as an error — the conservative choice for a tool whose
// job is to stop broken edits.
func severityLabel(severity DiagnosticSeverity) string {
	switch severity {
	case SeverityWarning:
		return "warning"
	case SeverityInformation:
		return "info"
	case SeverityHint:
		return "hint"
	default:
		return "error"
	}
}

// collapseWhitespace flattens a (possibly multi-line) diagnostic message to a
// single line so each formatted record stays "path:line:col: severity: message"
// and downstream line-based parsing isn't broken by embedded \n / \t.
func collapseWhitespace(message string) string {
	return strings.Join(strings.Fields(message), " ")
}

// hasErrors reports whether any diagnostic is error severity.
func hasErrors(diags []Diagnostic) bool {
	for _, diag := range diags {
		if diag.Severity == SeverityError || diag.Severity == 0 {
			return true
		}
	}
	return false
}

// FilterBySeverity returns only diagnostics at or above (numerically at or below,
// since Error=1 is most severe) the given severity.
func FilterBySeverity(diags []Diagnostic, minimum DiagnosticSeverity) []Diagnostic {
	out := make([]Diagnostic, 0, len(diags))
	for _, diag := range diags {
		sev := diag.Severity
		if sev == 0 {
			sev = SeverityError
		}
		if sev <= minimum {
			out = append(out, diag)
		}
	}
	return out
}

// FormatDiagnostics renders diagnostics as compact, model-readable lines:
//
//	path:line:col: severity: message
//
// Lines/columns are converted from LSP's zero-based positions to the one-based
// form editors and compilers print. The path is passed in because a Diagnostic
// carries only a range, not its document.
func FormatDiagnostics(path string, diags []Diagnostic) string {
	lines := make([]string, 0, len(diags))
	for _, diag := range diags {
		lines = append(lines, fmt.Sprintf("%s:%d:%d: %s: %s",
			path,
			diag.Range.Start.Line+1,
			diag.Range.Start.Character+1,
			severityLabel(diag.Severity),
			collapseWhitespace(diag.Message),
		))
	}
	return strings.Join(lines, "\n")
}
