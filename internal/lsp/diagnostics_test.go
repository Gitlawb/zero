package lsp

import "testing"

func TestFormatDiagnosticsShape(t *testing.T) {
	diags := []Diagnostic{
		{Range: Range{Start: Position{Line: 9, Character: 4}}, Severity: SeverityError, Message: "undefined: foo"},
		{Range: Range{Start: Position{Line: 0, Character: 0}}, Severity: SeverityWarning, Message: "unused import"},
	}
	got := FormatDiagnostics("internal/x/a.go", diags)
	want := "internal/x/a.go:10:5: error: undefined: foo\ninternal/x/a.go:1:1: warning: unused import"
	if got != want {
		t.Fatalf("FormatDiagnostics =\n%q\nwant\n%q", got, want)
	}
}

func TestHasErrors(t *testing.T) {
	if hasErrors([]Diagnostic{{Severity: SeverityWarning}}) {
		t.Fatal("warnings are not errors")
	}
	if !hasErrors([]Diagnostic{{Severity: SeverityWarning}, {Severity: SeverityError}}) {
		t.Fatal("an error diagnostic should be detected")
	}
	// Unset severity is treated as an error (conservative).
	if !hasErrors([]Diagnostic{{Message: "no severity"}}) {
		t.Fatal("unset severity should count as an error")
	}
}

func TestFilterBySeverity(t *testing.T) {
	diags := []Diagnostic{
		{Severity: SeverityError, Message: "e"},
		{Severity: SeverityWarning, Message: "w"},
		{Severity: SeverityHint, Message: "h"},
	}
	errorsOnly := FilterBySeverity(diags, SeverityError)
	if len(errorsOnly) != 1 || errorsOnly[0].Message != "e" {
		t.Fatalf("error filter = %#v", errorsOnly)
	}
	upToWarning := FilterBySeverity(diags, SeverityWarning)
	if len(upToWarning) != 2 {
		t.Fatalf("warning filter should include error+warning, got %#v", upToWarning)
	}
}

func TestFormatDiagnosticsCollapsesMultilineMessage(t *testing.T) {
	diags := []Diagnostic{{
		Range:    Range{Start: Position{Line: 0, Character: 0}},
		Severity: SeverityError,
		Message:  "first line\n\tsecond   line",
	}}
	got := FormatDiagnostics("a.go", diags)
	if want := "a.go:1:1: error: first line second line"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
