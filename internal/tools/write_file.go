package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type writeFileTool struct {
	baseTool
	workspaceRoot string
	scope         PathScope
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
	if info, err := root.Stat(target.relative); err == nil {
		existed = true
		writeMode = info.Mode()
		if !overwrite {
			return errorResult("Error: " + relativePath + " already exists. Pass overwrite: true to replace it.")
		}
	} else if !os.IsNotExist(err) {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}

	priorContent := ""
	if existed {
		readFile, _, rerr := protectedRootRead(root, target.relative, absolutePath, tool.workspaceRoot)
		if rerr != nil {
			return errorResult("Error writing file " + relativePath + ": " + rerr.Error())
		}
		current, rerr := io.ReadAll(readFile)
		closeErr := readFile.Close()
		if rerr != nil || closeErr != nil {
			return errorResult("Error writing file " + relativePath + ": " + errors.Join(rerr, closeErr).Error())
		}
		priorContent = string(current)
		// On overwrite, refuse to clobber a tracked file that changed on disk
		// outside Zero since it was last read.
		if options.FileTracker != nil && !options.FileTracker.SeenWhole(absolutePath) {
			return errorResult(fileUnseenMessage(relativePath))
		}
		if _, tracked := options.FileTracker.Version(absolutePath); tracked {
			if cerr := options.FileTracker.CheckConflict(absolutePath, current); cerr != nil {
				return errorResult(fileConflictMessage(relativePath))
			}
		}
	}

	// Engine-independent lexical refusal, followed by an atomic rooted publish.
	// If a concurrent writer swaps this name to a token hard link after the
	// check, Rename replaces that directory entry; it never truncates the token
	// inode. A create uses an exclusive no-replace publish for the same reason.
	if err := protectedMutationDenied(absolutePath, tool.workspaceRoot); err != nil {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}
	if _, err := writeRootedFile(root, target.relative, []byte(content), writeMode, !overwrite); err != nil {
		return errorResult("Error writing file " + relativePath + ": " + err.Error())
	}
	modelKnownContent := content
	// Pathname-based formatters and diagnostics reopen the published name. Skip
	// them while a protected token is selected, or they would reintroduce the
	// same swap window the rooted atomic write closes.
	credentialsActive := protectedCredentialsActive(tool.workspaceRoot)
	if !credentialsActive {
		content = maybeFormatWrittenFile(ctx, absolutePath, content)
	}
	// Baseline the freshly written content so a later edit/overwrite in this
	// session compares against what is now on disk.
	newInfo, _ := root.Stat(target.relative)
	options.FileTracker.Record(absolutePath, []byte(content), newInfo)
	if content == modelKnownContent {
		options.FileTracker.RecordSeenRange(absolutePath, 1, lineCount(content), lineCount(content))
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
	if !credentialsActive {
		summary += inlineDiagnostics(ctx, options, absolutePath, relativePath)
	}
	result := okResult(summary)
	result.ChangedFiles = []string{relativePath}
	// Card-only preview: a real unified diff (all-green for a create, red/green for
	// an overwrite) on Display.Preview. Output stays the summary, so the model never
	// re-reads the file — the rich preview costs zero model tokens.
	result.Display = Display{Summary: summary, Kind: "file", Preview: boundedUnifiedDiff(relativePath, priorContent, content)}
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
