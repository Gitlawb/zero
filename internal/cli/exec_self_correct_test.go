package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseExecSelfCorrectFlag(t *testing.T) {
	t.Run("absent defaults to false", func(t *testing.T) {
		options, help, err := parseExecArgs([]string{"hello"})
		if err != nil {
			t.Fatalf("parseExecArgs returned error: %v", err)
		}
		if help {
			t.Fatal("help = true, want false")
		}
		if options.selfCorrect {
			t.Fatal("selfCorrect = true, want false by default")
		}
	})

	t.Run("flag sets true", func(t *testing.T) {
		options, _, err := parseExecArgs([]string{"--self-correct", "hello"})
		if err != nil {
			t.Fatalf("parseExecArgs returned error: %v", err)
		}
		if !options.selfCorrect {
			t.Fatal("selfCorrect = false, want true")
		}
		if strings.Join(options.promptParts, " ") != "hello" {
			t.Fatalf("promptParts = %#v, want [hello]", options.promptParts)
		}
	})

	t.Run("flag rejects an inline value", func(t *testing.T) {
		_, _, err := parseExecArgs([]string{"--self-correct=yes", "hello"})
		if err == nil {
			t.Fatal("expected an error for --self-correct=yes, got nil")
		}
	})
}

func TestRunExecHelpDocumentsSelfCorrect(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := Run([]string{"exec", "--help"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout.String(), "--self-correct") {
		t.Fatalf("expected exec help to document --self-correct, got %q", stdout.String())
	}
}
