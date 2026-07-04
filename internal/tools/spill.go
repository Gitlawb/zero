package tools

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gitlawb/zero/internal/redaction"
)

// Spill-to-disk for truncated tool output. When a command produces more than
// its context budget, the tail was previously simply gone — the model's only
// recourse was re-running the (possibly expensive or non-idempotent) command
// with a bigger budget. Instead, the full output is written to a cache file
// whose path is included in the truncation notice, so the model can grep or
// read_file the remainder. Spilling is best-effort: on any error truncation
// behaves exactly as before, just without the file hint.

// spillRetention is how long spilled outputs are kept; files older than this
// are opportunistically removed whenever a new spill happens.
const spillRetention = 7 * 24 * time.Hour

// spillDir returns the shared spill directory, creating it on first use.
func spillDir() (string, error) {
	dir := filepath.Join(os.TempDir(), "zero-tool-output")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// spillTruncatedOutput writes the full pre-truncation output to the spill
// directory and returns the file path, or "" when spilling fails. Output is
// scrubbed with the same configured-key redaction the registry applies at the
// tool boundary, so a spilled file never holds a secret the transcript would
// have hidden.
func spillTruncatedOutput(toolName, output string) string {
	dir, err := spillDir()
	if err != nil {
		return ""
	}
	sweepSpillDir(dir)
	prefix := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '-'
	}, toolName)
	file, err := os.CreateTemp(dir, prefix+"-*.txt")
	if err != nil {
		return ""
	}
	defer file.Close()
	scrubbed := redaction.RedactString(output, redaction.Options{})
	if _, err := file.WriteString(scrubbed); err != nil {
		_ = os.Remove(file.Name())
		return ""
	}
	return file.Name()
}

// sweepSpillDir removes spill files older than spillRetention. Best-effort:
// errors are ignored, the directory is small, and a sweep runs only when a new
// spill is about to happen anyway.
func sweepSpillDir(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-spillRetention)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}
