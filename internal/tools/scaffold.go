package tools

// Distribution: scaffold a new plugin-tool skeleton in the user's toolbox dir.
// It writes a plugin manifest (schemaVersion 1, one prompt-gated tool with an
// empty parameter schema and a command pointing at the entry stub) plus a
// runnable stub carrying a clear TODO. After plugin activation the scaffolded
// tool is loadable like any other local plugin. Generated content is data only —
// scaffolding never executes anything.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ScaffoldRuntime selects the entry-stub language. The default is a POSIX shell
// stub, which has no interpreter install dependency; node/python are offered for
// authors who prefer them. Runtimes are deliberately model-provider-agnostic.
type ScaffoldRuntime string

const (
	RuntimeShell  ScaffoldRuntime = "shell"
	RuntimeNode   ScaffoldRuntime = "node"
	RuntimePython ScaffoldRuntime = "python"
)

// scaffoldNamePattern matches a safe single-segment tool name (also a valid
// plugin id and directory component).
var scaffoldNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ScaffoldOptions configures a single tool scaffold.
type ScaffoldOptions struct {
	// Name is the tool/plugin id (a single path segment).
	Name string
	// Dir is the toolbox directory to create the plugin under (typically the user
	// plugins root).
	Dir string
	// Description is an optional human description; a default is used when empty.
	Description string
	// Runtime selects the entry-stub language; defaults to RuntimeShell.
	Runtime ScaffoldRuntime
}

// ScaffoldResult reports the generated layout.
type ScaffoldResult struct {
	Name         string `json:"name"`
	PluginDir    string `json:"pluginDir"`
	ManifestPath string `json:"manifestPath"`
	EntryPath    string `json:"entryPath"`
}

// runtimeSpec describes how to invoke a stub of a given runtime and what stub
// body/filename to emit.
type runtimeSpec struct {
	entryFile string
	command   string
	// argsBefore are command args that precede the entry path (e.g. interpreter
	// scripts pass the script path as an arg; an executable shell script is the
	// command itself and needs no preceding args).
	usesScriptArg bool
	mode          os.FileMode
	body          func(name string, description string) string
}

func runtimeSpecFor(runtime ScaffoldRuntime) (runtimeSpec, error) {
	switch runtime {
	case "", RuntimeShell:
		return runtimeSpec{
			entryFile:     "run.sh",
			command:       "sh",
			usesScriptArg: true,
			mode:          0o755,
			body:          shellStub,
		}, nil
	case RuntimeNode:
		return runtimeSpec{
			entryFile:     "run.mjs",
			command:       "node",
			usesScriptArg: true,
			mode:          0o644,
			body:          nodeStub,
		}, nil
	case RuntimePython:
		return runtimeSpec{
			entryFile:     "run.py",
			command:       "python3",
			usesScriptArg: true,
			mode:          0o644,
			body:          pythonStub,
		}, nil
	default:
		return runtimeSpec{}, fmt.Errorf("unknown runtime %q (use shell, node, or python)", runtime)
	}
}

// Scaffold generates a plugin-tool skeleton under options.Dir/<name>/ and returns
// the created paths. It refuses an invalid name, a missing dir, or an existing
// target so it never clobbers prior work.
func Scaffold(options ScaffoldOptions) (ScaffoldResult, error) {
	name := strings.TrimSpace(options.Name)
	if name == "" {
		return ScaffoldResult{}, fmt.Errorf("a tool name is required")
	}
	if name != filepath.Base(name) || !scaffoldNamePattern.MatchString(name) || strings.Contains(name, "..") {
		return ScaffoldResult{}, fmt.Errorf("invalid tool name %q (use letters, numbers, dots, dashes, or underscores)", name)
	}
	dir := strings.TrimSpace(options.Dir)
	if dir == "" {
		return ScaffoldResult{}, fmt.Errorf("a toolbox directory is required")
	}

	spec, err := runtimeSpecFor(options.Runtime)
	if err != nil {
		return ScaffoldResult{}, err
	}

	pluginDir := filepath.Join(dir, name)
	if _, err := os.Stat(pluginDir); err == nil {
		return ScaffoldResult{}, fmt.Errorf("a tool already exists at %s", pluginDir)
	} else if !os.IsNotExist(err) {
		return ScaffoldResult{}, fmt.Errorf("stat %s: %w", pluginDir, err)
	}

	description := strings.TrimSpace(options.Description)
	if description == "" {
		description = "TODO: describe what the " + name + " tool does."
	}

	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		return ScaffoldResult{}, fmt.Errorf("create tool dir: %w", err)
	}

	entryPath := filepath.Join(pluginDir, spec.entryFile)
	if err := os.WriteFile(entryPath, []byte(spec.body(name, description)), spec.mode); err != nil {
		return ScaffoldResult{}, fmt.Errorf("write entry stub: %w", err)
	}

	// The command points at the stub. Args reference the entry by its path
	// RELATIVE to the plugin dir, matching how the loader resolves plugin tool
	// args (plugin paths stay inside the plugin directory).
	args := []string{}
	if spec.usesScriptArg {
		args = append(args, spec.entryFile)
	}
	manifest := map[string]any{
		"schemaVersion": 1,
		"id":            name,
		"name":          name,
		"version":       "0.1.0",
		"description":   description,
		"tools": []map[string]any{{
			"name":        name,
			"description": description,
			"command":     spec.command,
			"args":        args,
			"inputSchema": map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
			// Prompt-gated by default: a freshly scaffolded tool must never be
			// auto-approved. Activation applies the normal permission flow.
			"permission": "prompt",
		}},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return ScaffoldResult{}, fmt.Errorf("encode manifest: %w", err)
	}
	manifestPath := filepath.Join(pluginDir, "plugin.json")
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		return ScaffoldResult{}, fmt.Errorf("write manifest: %w", err)
	}

	return ScaffoldResult{
		Name:         name,
		PluginDir:    pluginDir,
		ManifestPath: manifestPath,
		EntryPath:    entryPath,
	}, nil
}

func shellStub(name string, description string) string {
	return `#!/bin/sh
# ` + name + ` — Zero plugin tool entry point.
# ` + description + `
#
# Zero invokes this script with the tool-call arguments as a single JSON object
# on stdin and expects the tool result on stdout.
#
# TODO: read the JSON arguments from stdin, do the work, and print a result.
set -eu

input="$(cat)"
echo "TODO: implement ${0##*/}. Received arguments: ${input}"
`
}

func nodeStub(name string, description string) string {
	return `// ` + name + ` — Zero plugin tool entry point.
// ` + description + `
//
// Zero invokes this script with the tool-call arguments as a single JSON object
// on stdin and expects the tool result on stdout.

import process from "node:process";

let raw = "";
process.stdin.on("data", (chunk) => {
  raw += chunk;
});
process.stdin.on("end", () => {
  // TODO: parse the JSON arguments and do the work.
  const args = raw.trim() ? JSON.parse(raw) : {};
  process.stdout.write("TODO: implement ` + name + `. Received: " + JSON.stringify(args) + "\n");
});
`
}

func pythonStub(name string, description string) string {
	return `#!/usr/bin/env python3
"""` + name + ` — Zero plugin tool entry point.

` + description + `

Zero invokes this script with the tool-call arguments as a single JSON object on
stdin and expects the tool result on stdout.
"""
import json
import sys


def main() -> None:
    raw = sys.stdin.read()
    # TODO: parse the JSON arguments and do the work.
    args = json.loads(raw) if raw.strip() else {}
    print("TODO: implement ` + name + `. Received:", json.dumps(args))


if __name__ == "__main__":
    main()
`
}
