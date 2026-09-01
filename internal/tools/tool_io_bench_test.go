package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkReadFileLargeRangeSmall(b *testing.B) {
	root := b.TempDir()
	const lines = 200_000
	writeBenchLines(b, filepath.Join(root, "large.txt"), lines, func(i int) string {
		return fmt.Sprintf("line-%06d abcdefghijklmnopqrstuvwxyz 0123456789\n", i)
	})

	tool := NewScopedReadFileTool(root, nil)
	args := map[string]any{
		"path":       "large.txt",
		"start_line": lines / 2,
		"max_lines":  5,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := tool.Run(context.Background(), args)
		if result.Status != StatusOK {
			b.Fatalf("status=%s output=%s", result.Status, result.Output)
		}
	}
}

func BenchmarkGrepLargeTreeHeadLimit(b *testing.B) {
	root := b.TempDir()
	const files = 80
	const linesPerFile = 500
	for fileIndex := 0; fileIndex < files; fileIndex++ {
		path := filepath.Join(root, "pkg", fmt.Sprintf("file-%03d.txt", fileIndex))
		writeBenchLines(b, path, linesPerFile, func(lineIndex int) string {
			return fmt.Sprintf("needle file=%03d line=%03d payload=%s\n", fileIndex, lineIndex, strings.Repeat("x", 32))
		})
	}

	tool := NewScopedGrepTool(root, nil)
	args := map[string]any{
		"pattern":    "needle",
		"path":       ".",
		"head_limit": 10,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		result := tool.Run(context.Background(), args)
		if result.Status != StatusOK {
			b.Fatalf("status=%s output=%s", result.Status, result.Output)
		}
	}
}

func writeBenchLines(tb testing.TB, path string, lines int, line func(int) string) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		tb.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			tb.Fatal(err)
		}
	}()
	for i := 0; i < lines; i++ {
		if _, err := file.WriteString(line(i)); err != nil {
			tb.Fatal(err)
		}
	}
}
