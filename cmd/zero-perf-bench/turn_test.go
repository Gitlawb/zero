package main

import (
	"strings"
	"testing"
)

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

// The bench validates the profile BEFORE spawning anything: an unknown name
// would otherwise make every child exit with a usage error in milliseconds
// and record those near-zero walls as valid latency samples in an exit-0
// report. Lookup normalization also keeps the stamped name canonical so two
// captures of the same posture compare equal.
func TestParseTurnArgsExecProfileValidatesAndNormalizes(t *testing.T) {
	getenv := func(string) string { return "" }

	options, err := parseTurnArgs([]string{
		"--suite", "suite.json", "--model", "m", "--exec-profile", "FAST",
	}, getenv)
	if err != nil {
		t.Fatalf("parseTurnArgs: %v", err)
	}
	if options.ExecProfile != "fast" {
		t.Fatalf("ExecProfile = %q, want the canonical fast", options.ExecProfile)
	}

	_, err = parseTurnArgs([]string{
		"--suite", "suite.json", "--model", "m", "--exec-profile", "blanced",
	}, getenv)
	if err == nil {
		t.Fatal("an unknown profile must fail parse, before anything spawns")
	}
	for _, name := range []string{"balanced", "fast", "thorough"} {
		if !strings.Contains(err.Error(), name) {
			t.Fatalf("error must list %q, got %q", name, err.Error())
		}
	}
}
