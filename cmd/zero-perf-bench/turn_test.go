package main

import "testing"

func TestParseTurnArgsExecProfile(t *testing.T) {
	getenv := func(string) string { return "" }
	options, err := parseTurnArgs([]string{
		"--suite", "suite.json",
		"--model", "m",
		"--exec-profile", "fast",
	}, getenv)
	if err != nil {
		t.Fatalf("parseTurnArgs: %v", err)
	}
	if options.ExecProfile != "fast" {
		t.Fatalf("ExecProfile = %q, want fast", options.ExecProfile)
	}

	// Inline form.
	options, err = parseTurnArgs([]string{
		"--suite=suite.json",
		"--model=m",
		"--exec-profile=thorough",
	}, getenv)
	if err != nil {
		t.Fatalf("parseTurnArgs inline: %v", err)
	}
	if options.ExecProfile != "thorough" {
		t.Fatalf("ExecProfile = %q, want thorough", options.ExecProfile)
	}
}
