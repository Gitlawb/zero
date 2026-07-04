package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTruncateExecOutputSpillWritesFullOutput(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	long := strings.Repeat("line of build output\n", 5000) // ~100KB > 40KB budget

	truncated, wasTruncated := truncateExecOutputSpill(long, defaultMaxOutputTokens, "bash")
	if !wasTruncated {
		t.Fatal("output over budget must truncate")
	}
	if !strings.Contains(truncated, "full output saved to ") {
		t.Fatalf("truncation notice missing spill path: %q", truncated[:200])
	}
	// Extract the path and verify the file holds the complete output.
	start := strings.Index(truncated, "full output saved to ") + len("full output saved to ")
	end := strings.Index(truncated[start:], " (grep")
	spillPath := truncated[start : start+end]
	content, err := os.ReadFile(spillPath)
	if err != nil {
		t.Fatalf("spill file unreadable: %v", err)
	}
	if string(content) != long {
		t.Fatalf("spill file must hold the full output: got %d bytes, want %d", len(content), len(long))
	}
}

func TestTruncateExecOutputUnderBudgetUnchanged(t *testing.T) {
	output, wasTruncated := truncateExecOutputSpill("short output", defaultMaxOutputTokens, "bash")
	if wasTruncated || output != "short output" {
		t.Fatalf("under-budget output must pass through untouched: %q", output)
	}
}

func TestFormatBashOutputCapsVerboseOutput(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	long := strings.Repeat("x", 200*1024)
	formatted := formatBashOutput(long, "", 0)
	if len(formatted) > defaultMaxOutputTokens*4+1024 {
		t.Fatalf("bash output must be capped near the token budget, got %d bytes", len(formatted))
	}
	if !strings.Contains(formatted, "output truncated") {
		t.Fatal("capped bash output must carry the truncation notice")
	}
}

func TestSweepSpillDirRemovesOnlyOldFiles(t *testing.T) {
	dir := t.TempDir()
	oldFile := filepath.Join(dir, "bash-old.txt")
	newFile := filepath.Join(dir, "bash-new.txt")
	for _, path := range []string{oldFile, newFile} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	stale := time.Now().Add(-spillRetention - time.Hour)
	if err := os.Chtimes(oldFile, stale, stale); err != nil {
		t.Fatal(err)
	}

	sweepSpillDir(dir)

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatal("stale spill file must be removed")
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Fatal("fresh spill file must survive the sweep")
	}
}

func TestSpillTruncatedOutputWritesFile(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	path := spillTruncatedOutput("exec_command", "some output body")
	if path == "" {
		t.Fatal("spill must return a file path")
	}
	if base := filepath.Base(path); !strings.HasPrefix(base, "exec_command-") {
		t.Fatalf("spill file name must carry the tool prefix: %s", base)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "some output body" {
		t.Fatalf("unexpected spill content: %q", content)
	}
}
