package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSmokeTargetUsesLocalBinary(t *testing.T) {
	if got := smokeTarget(true); got != "local-binary" {
		t.Fatalf("smokeTarget(true) = %q, want local-binary", got)
	}
}

func TestRunSmokeUsesReleasePath(t *testing.T) {
	var stdout bytes.Buffer
	if err := run([]string{"smoke"}, &stdout); err != nil {
		t.Fatalf("run smoke: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "local-binary" {
		t.Fatalf("run smoke output = %q, want local-binary", got)
	}
}

func TestRunBuildUsesReleasePath(t *testing.T) {
	var stdout bytes.Buffer
	if err := run([]string{"build"}, &stdout); err != nil {
		t.Fatalf("run build: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "build-then-smoke" {
		t.Fatalf("run build output = %q, want build-then-smoke", got)
	}
}
