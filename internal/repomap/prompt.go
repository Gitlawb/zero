package repomap

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

const clippedMarker = "\n...[clipped]"

// RenderPrompt renders a compact, deterministic repository map without
// exceeding budget bytes. For very small budgets it returns an empty string.
func RenderPrompt(snapshot Snapshot, budget int) string {
	if budget <= 0 {
		return ""
	}

	files := normalizePromptFiles(snapshot.Root, snapshot.Files)
	fileCount := snapshot.FileCount
	if fileCount <= 0 {
		fileCount = len(files)
	}
	dirCount := snapshot.DirectoryCount
	if dirCount <= 0 {
		dirCount = countPromptDirs(files)
	}

	languageCounts := snapshot.LanguageCounts
	if len(languageCounts) == 0 {
		languageCounts = countLanguages(files)
	}
	extensionCounts := snapshot.ExtensionCounts
	if len(extensionCounts) == 0 {
		extensionCounts = countExtensions(files)
	}
	importantFiles := normalizePromptPaths(snapshot.Root, snapshot.ImportantFiles)
	if len(importantFiles) == 0 {
		importantFiles = importantFilePaths(files)
	}

	var builder strings.Builder
	writePromptLine(&builder, "Repo: %s", repoName(snapshot.Root))
	writePromptLine(&builder, "Counts: files=%d dirs=%d", fileCount, dirCount)
	if snapshot.Truncated {
		writePromptLine(&builder, "Truncated: true")
	}
	writePromptLine(&builder, "Important files: %s", formatStringList(importantFiles))
	writePromptLine(&builder, "Languages: %s", formatCounts(languageCounts))
	writePromptLine(&builder, "Extensions: %s", formatCounts(extensionCounts))
	builder.WriteByte('\n')
	builder.WriteString("Files:")
	for _, file := range files {
		builder.WriteByte('\n')
		builder.WriteString("  ")
		builder.WriteString(file.Path)
	}

	return clampBudget(builder.String(), budget)
}

func normalizePromptFiles(root string, files []File) []File {
	seen := map[string]struct{}{}
	normalized := make([]File, 0, len(files))
	for _, file := range files {
		rel := relativePromptPath(root, file.Path)
		if rel == "" {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		ext := file.Extension
		if ext == "" {
			ext = strings.ToLower(path.Ext(rel))
		}
		lang := file.Language
		if lang == "" {
			lang = languageForExt(ext)
		}
		normalized = append(normalized, File{
			Path:      rel,
			Language:  lang,
			Extension: ext,
			Depth:     fileDepth(rel),
		})
	}
	sortFiles(normalized)
	return normalized
}

func normalizePromptPaths(root string, values []string) []string {
	seen := map[string]struct{}{}
	normalized := make([]File, 0, len(values))
	for _, value := range values {
		rel := relativePromptPath(root, value)
		if rel == "" {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		normalized = append(normalized, File{Path: rel})
	}
	sortFiles(normalized)
	out := make([]string, 0, len(normalized))
	for _, file := range normalized {
		out = append(out, file.Path)
	}
	return out
}

func relativePromptPath(root string, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cleanRoot := filepath.Clean(root)
	cleanValue := filepath.Clean(value)
	if root != "" && filepath.IsAbs(cleanValue) {
		if rel, err := filepath.Rel(cleanRoot, cleanValue); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			return filepath.ToSlash(rel)
		}
	}
	value = strings.ReplaceAll(value, "\\", "/")
	cleanRootSlash := strings.TrimSuffix(strings.ReplaceAll(cleanRoot, "\\", "/"), "/")
	if cleanRootSlash != "" && strings.HasPrefix(value, cleanRootSlash+"/") {
		value = strings.TrimPrefix(value, cleanRootSlash+"/")
	}
	value = path.Clean(value)
	value = strings.TrimPrefix(value, "/")
	for strings.HasPrefix(value, "./") {
		value = strings.TrimPrefix(value, "./")
	}
	if value == "." {
		return ""
	}
	return value
}

func repoName(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return "repo"
	}
	clean := filepath.Clean(root)
	base := filepath.Base(clean)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "repo"
	}
	return base
}

func countPromptDirs(files []File) int {
	dirs := map[string]struct{}{}
	for _, file := range files {
		dir := path.Dir(file.Path)
		for dir != "." && dir != "/" && dir != "" {
			dirs[dir] = struct{}{}
			dir = path.Dir(dir)
		}
	}
	return len(dirs)
}

func formatStringList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}

func formatCounts(counts []Count) string {
	if len(counts) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(counts))
	for _, count := range counts {
		parts = append(parts, fmt.Sprintf("%s=%d", count.Name, count.Count))
	}
	return strings.Join(parts, ", ")
}

func writePromptLine(builder *strings.Builder, format string, args ...any) {
	if builder.Len() > 0 {
		builder.WriteByte('\n')
	}
	builder.WriteString(fmt.Sprintf(format, args...))
}

func clampBudget(text string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(text) <= budget {
		return text
	}
	if budget < len(clippedMarker) {
		return ""
	}
	prefixBudget := budget - len(clippedMarker)
	prefix := trimValidUTF8(text, prefixBudget)
	prefix = strings.TrimRight(prefix, "\n")
	if prefix == "" {
		return strings.TrimSpace(clippedMarker)
	}
	return prefix + clippedMarker
}

func trimValidUTF8(text string, budget int) string {
	if budget <= 0 {
		return ""
	}
	if len(text) <= budget {
		return text
	}
	for budget > 0 && !utf8.ValidString(text[:budget]) {
		budget--
	}
	return text[:budget]
}
