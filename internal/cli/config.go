package cli

import (
	"fmt"
	"io"

	"github.com/Gitlawb/zero/internal/config"
)

func runConfigSummary(opt commandCenterOptions, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	resolved, exitCode := resolveCommandCenterConfig(stderr, deps)
	if exitCode != exitSuccess {
		return exitCode
	}

	summary := summarizeConfig(resolved)
	if opt.json {
		if err := writePrettyJSON(stdout, summary); err != nil {
			return exitCrash
		}
		return exitSuccess
	}
	if _, err := fmt.Fprintln(stdout, formatConfigSummary(summary)); err != nil {
		return exitCrash
	}
	return exitSuccess
}

func runConfigCleanup(opt commandCenterOptions, stdout io.Writer, stderr io.Writer, deps appDeps) int {
	workspaceRoot, err := resolveWorkspaceRoot("", deps)
	if err != nil {
		return writeExecUsageError(stderr, err.Error())
	}

	userConfigPath, err := deps.userConfigPath()
	if err != nil {
		return writeAppError(stderr, "failed to resolve user config path: "+err.Error(), exitCrash)
	}

	resolveOptions, err := config.DefaultResolveOptions(workspaceRoot)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}
	projectConfigPath := resolveOptions.ProjectConfigPath

	removed, err := config.CleanupStaleFavorites(userConfigPath, projectConfigPath)
	if err != nil {
		return writeAppError(stderr, err.Error(), exitCrash)
	}

	if opt.json {
		if err := writePrettyJSON(stdout, map[string]any{"removedFavorites": removed}); err != nil {
			return exitCrash
		}
		return exitSuccess
	}

	if _, err := fmt.Fprintf(stdout, "Config cleaned: removed %d favorite model entries that do not match an available provider/model.\n", removed); err != nil {
		return exitCrash
	}
	return exitSuccess
}
