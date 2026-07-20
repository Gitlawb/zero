package tea

import (
	"log"
	"os"
	"path/filepath"
	"testing"
)

func TestLogToFile(t *testing.T) {
	// LogToFile mutates the process-global default logger (output, prefix);
	// SetFlags below mutates its flags too. Restore all three so a later
	// test doesn't inherit this test's prefix/flags, or try to write into
	// the file this test is about to close.
	originalOutput := log.Writer()
	originalPrefix := log.Prefix()
	originalFlags := log.Flags()
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetPrefix(originalPrefix)
		log.SetFlags(originalFlags)
	})

	path := filepath.Join(t.TempDir(), "log.txt")
	prefix := "logprefix"
	f, err := LogToFile(path, prefix)
	if err != nil {
		t.Fatal(err)
	}
	log.SetFlags(log.Lmsgprefix)
	log.Println("some test log")
	if closeErr := f.Close(); closeErr != nil {
		t.Error(closeErr)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != prefix+" some test log\n" {
		t.Fatalf("wrong log msg: %q", string(out))
	}
}
