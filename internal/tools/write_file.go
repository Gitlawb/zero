package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

type writeFileTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
	formatter     writtenFileFormatter
	readFile      func(string) ([]byte, error)
}

func NewScopedWriteFileTool(workspaceRoot string, scope PathScope) Tool {
	return writeFileTool{
		baseTool: baseTool{
			name:        "write_file",
			description: "Create a new file, refusing to overwrite existing files unless overwrite is true.",
			parameters: Schema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path":      {Type: "string", Description: "Absolute or relative path of the file to write."},
					"content":   {Type: "string", Description: "Full file contents to write."},
					"overwrite": {Type: "boolean", Description: "Whether to allow overwriting an existing file.", Default: false},
				},
				Required:             []string{"path", "content"},
				AdditionalProperties: false,
			},
			safety:       promptSafety(SideEffectWrite, "Creates or overwrites files."),
			capabilities: ToolCapabilities{Effect: EffectWorkspaceWrite, ThreadSafe: false, ResourceKeys: fileResourceKeys},
		},
		workspaceRoot: normalizeWorkspaceRoot(workspaceRoot),
		scope:         scope,
		formatter:     maybeFormatWrittenFile,
	}
}

func (tool writeFileTool) Run(ctx context.Context, args map[string]any) Result {
	return tool.RunWithOptions(ctx, args, RunOptions{})
}

func (tool writeFileTool) RunWithOptions(ctx context.Context, args map[string]any, options RunOptions) Result {
	requestedPath, err := aliasedStringArg(args, []string{"path", "file", "file_path", "filename"}, "", true, false)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_file: " + err.Error())
	}
	content, err := fileContentArg(args)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_file: " + err.Error())
	}
	overwrite, err := boolArg(args, "overwrite", false)
	if err != nil {
		return errorResult("Error: Invalid arguments for write_file: " + err.Error())
	}

	target, err := resolveScopedWriteTarget(tool.workspaceRoot, tool.scope, requestedPath)
	if err != nil {
		return errorResult("Error writing file " + requestedPath + ": " + err.Error())
	}
	absolutePath, relativePath := target.absolute, target.display
	root, err := os.OpenRoot(target.root)
	if err != nil {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}
	defer root.Close()

	existed := false
	writeMode := os.FileMode(0o644)
	var priorInfo os.FileInfo
	if info, err := root.Stat(target.relative); err == nil {
		existed = true
		writeMode = info.Mode()
		priorInfo = info
		if !overwrite {
			return errorResult("Error: " + relativePath + " already exists. Pass overwrite: true to replace it.")
		}
	} else if !os.IsNotExist(err) {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}

	priorContent := ""
	priorContentKnown := !existed
	if existed {
		var current []byte
		var rerr error
		if tool.readFile != nil {
			current, rerr = tool.readFile(absolutePath)
		} else {
			current, priorInfo, rerr = readRootedFile(root, target.relative)
			if rerr != nil {
				return errorResult("Error writing file " + relativePath + ": " + rerr.Error())
			}
		}
		if rerr == nil {
			priorContent, priorContentKnown = string(current), true
		}
		// On overwrite, refuse to clobber a tracked file that changed on disk
		// outside Zero since it was last read.
		if options.FileTracker != nil && !options.FileTracker.SeenWhole(absolutePath) {
			return errorResult(fileUnseenMessage(relativePath))
		}
		if _, tracked := options.FileTracker.Version(absolutePath); tracked {
			// Fail CLOSED: if the tracked file can't be re-read to verify it, refuse
			// the overwrite rather than clobbering a file whose current state is
			// unknown (it may have been replaced or removed out from under us).
			if rerr != nil {
				return errorResult(fileConflictMessage(relativePath))
			}
			if cerr := options.FileTracker.CheckConflict(absolutePath, current); cerr != nil {
				return errorResult(fileConflictMessage(relativePath))
			}
		}
	}

	if err := protectedMutationDenied(absolutePath, tool.workspaceRoot); err != nil {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}
	var expectedContent *string
	if priorContentKnown {
		expectedContent = &priorContent
	}
	if err := commitRootedFileContents(root, target.relative, priorInfo, expectedContent, content); err != nil {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}
	modelKnownContent := content
	// Optional format-on-write (ZERO_FORMAT_ON_WRITE). Must run BEFORE the
	// FileTracker baseline: recording pre-format content would make the very
	// next edit look like an external modification and trip the conflict guard.
	formatting := tool.formatter(ctx, root, target.relative, absolutePath, tool.workspaceRoot, content, writeMode)
	published, newInfo, err := readRootedFile(root, target.relative)
	if err != nil {
		options.FileTracker.Forget(absolutePath)
		return errorResult("Error reading written file " + relativePath + ": " + err.Error())
	}
	content = string(published)
	// Baseline the freshly written content so a later edit/overwrite in this
	// session compares against what is now on disk.
	options.FileTracker.Record(absolutePath, []byte(content), newInfo)
	if content == modelKnownContent {
		options.FileTracker.RecordSeenRange(absolutePath, 1, trackedLineTotal(content), trackedLineTotal(content))
	}
	if !existed {
		options.FileTracker.RecordCreated(absolutePath)
	}

	verb := "Created"
	if existed {
		verb = "Overwrote"
	}
	// Report line count (not bytes): "Wrote 282 lines" reads as real work at a
	// glance, where a byte total is opaque noise.
	lines := strings.Count(content, "\n")
	if content != "" && !strings.HasSuffix(content, "\n") {
		lines++
	}
	summary := fmt.Sprintf("%s %s (%d lines).", verb, relativePath, lines)
	summary += formatting.notice(relativePath)
	summary += inlineDiagnostics(ctx, options, absolutePath, relativePath)
	result := okResult(summary)
	result.ChangedFiles = []string{relativePath}
	// Do not pretend an unreadable overwrite was a creation. The write may be
	// valid, but ACP only receives an exact before/after pair we actually saw.
	if priorContentKnown {
		if diff, ok := boundedFileDiff(absolutePath, priorContent, content, existed, true); ok {
			result.FileDiffs = []FileDiff{diff}
		} else if diffTextRevealsObfuscatedSecret(priorContent) || diffTextRevealsObfuscatedSecret(content) {
			result.Redacted = true
		}
	}
	// Card-only preview: a real unified diff (all-green for a create, red/green for
	// an overwrite) on Display.Preview. Output stays the summary, so the model never
	// re-reads the file — the rich preview costs zero model tokens.
	preview := ""
	if priorContentKnown {
		preview = boundedUnifiedDiff(relativePath, priorContent, content)
	}
	result.Display = Display{Summary: summary, Kind: "file", Preview: preview}
	return result
}

// fileContentArg reads the file body from "content" or a common alias that weaker
// models sometimes use instead (contents/text/body/data/file_content). It
// delegates to the shared aliasedStringArg so the present-but-non-string type
// error ("content must be a string") and the required-but-missing error
// ("content is required") stay consistent with every other tool. An empty string
// is allowed (writing an empty file), so allowEmpty is true.
func fileContentArg(args map[string]any) (string, error) {
	return aliasedStringArg(args, []string{"content", "contents", "text", "body", "data", "file_content"}, "", true, true)
}
