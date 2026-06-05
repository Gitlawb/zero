package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Source string
type DiagnosticKind string
type ToolPermission string
type HookEvent string

const (
	SourceUser    Source = "user"
	SourceProject Source = "project"
	SourceCustom  Source = "custom"
)

const (
	DiagnosticIO        DiagnosticKind = "io"
	DiagnosticJSON      DiagnosticKind = "json"
	DiagnosticSchema    DiagnosticKind = "schema"
	DiagnosticDuplicate DiagnosticKind = "duplicate"
)

const (
	PermissionAllow  ToolPermission = "allow"
	PermissionPrompt ToolPermission = "prompt"
	PermissionDeny   ToolPermission = "deny"
)

const (
	HookBeforeTool   HookEvent = "beforeTool"
	HookAfterTool    HookEvent = "afterTool"
	HookSessionStart HookEvent = "sessionStart"
	HookSessionEnd   HookEvent = "sessionEnd"
)

type Root struct {
	Source Source `json:"source"`
	Path   string `json:"path"`
}

type Diagnostic struct {
	Kind         DiagnosticKind `json:"kind"`
	Message      string         `json:"message"`
	Source       Source         `json:"source,omitempty"`
	Root         string         `json:"root,omitempty"`
	PluginPath   string         `json:"pluginPath,omitempty"`
	ManifestPath string         `json:"manifestPath,omitempty"`
	FieldPath    string         `json:"fieldPath,omitempty"`
	PluginID     string         `json:"pluginId,omitempty"`
}

type ToolExtension struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Command     string         `json:"command"`
	Args        []string       `json:"args"`
	InputSchema map[string]any `json:"inputSchema"`
	Permission  ToolPermission `json:"permission"`
}

type PathExtension struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Path        string `json:"path"`
}

type HookExtension struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Event       HookEvent `json:"event"`
	Command     string    `json:"command"`
	Args        []string  `json:"args"`
}

type LoadedPlugin struct {
	SchemaVersion int             `json:"schemaVersion"`
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Version       string          `json:"version"`
	Description   string          `json:"description,omitempty"`
	Enabled       bool            `json:"enabled"`
	Source        Source          `json:"source"`
	Root          string          `json:"root"`
	PluginDir     string          `json:"pluginDir"`
	ManifestPath  string          `json:"manifestPath"`
	Tools         []ToolExtension `json:"tools"`
	Prompts       []PathExtension `json:"prompts"`
	Skills        []PathExtension `json:"skills"`
	Hooks         []HookExtension `json:"hooks"`
}

type LoadResult struct {
	Roots       []Root         `json:"roots"`
	Plugins     []LoadedPlugin `json:"plugins"`
	Diagnostics []Diagnostic   `json:"diagnostics"`
}

type ResolveRootOptions struct {
	Cwd string
	Env map[string]string
}

type LoadOptions struct {
	Roots                         []Root
	Cwd                           string
	Env                           map[string]string
	AllowManifestToolAutoApproval bool
}

type ParseManifestOptions struct {
	Source                        Source
	Root                          string
	PluginDir                     string
	ManifestPath                  string
	AllowManifestToolAutoApproval bool
}

type ManifestError struct {
	FieldPath string
	Message   string
}

func (err ManifestError) Error() string {
	if err.FieldPath == "" {
		return err.Message
	}
	return err.FieldPath + ": " + err.Message
}

var pluginIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func ResolveRoots(options ResolveRootOptions) ([]Root, error) {
	cwd, err := resolveCwd(options.Cwd)
	if err != nil {
		return nil, err
	}

	configHome := strings.TrimSpace(envValue(options.Env, "XDG_CONFIG_HOME"))
	if configHome == "" {
		home := strings.TrimSpace(firstNonEmpty(envValue(options.Env, "HOME"), envValue(options.Env, "USERPROFILE")))
		if home == "" {
			home, err = os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("resolve user home: %w", err)
			}
		}
		configHome = filepath.Join(home, ".config")
	} else if !filepath.IsAbs(configHome) {
		configHome = filepath.Join(cwd, configHome)
	}

	return []Root{
		{Source: SourceUser, Path: filepath.Join(configHome, "zero", "plugins")},
		{Source: SourceProject, Path: filepath.Join(cwd, ".zero", "plugins")},
	}, nil
}

func Load(options LoadOptions) (LoadResult, error) {
	roots := options.Roots
	if len(roots) == 0 {
		resolvedRoots, err := ResolveRoots(ResolveRootOptions{Cwd: options.Cwd, Env: options.Env})
		if err != nil {
			return LoadResult{}, err
		}
		roots = resolvedRoots
	}

	diagnostics := []Diagnostic{}
	discovered := []LoadedPlugin{}
	for _, root := range roots {
		rootPath, err := filepath.Abs(root.Path)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Kind:    DiagnosticIO,
				Source:  root.Source,
				Root:    root.Path,
				Message: err.Error(),
			})
			continue
		}

		entries, err := os.ReadDir(rootPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			diagnostics = append(diagnostics, Diagnostic{
				Kind:    DiagnosticIO,
				Source:  root.Source,
				Root:    rootPath,
				Message: err.Error(),
			})
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			pluginDir := filepath.Join(rootPath, entry.Name())
			manifestPath := filepath.Join(pluginDir, "plugin.json")
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				diagnostics = append(diagnostics, toDiagnostic(err, root, rootPath, pluginDir, manifestPath))
				continue
			}

			var manifest any
			if err := json.Unmarshal(data, &manifest); err != nil {
				diagnostics = append(diagnostics, Diagnostic{
					Kind:         DiagnosticJSON,
					Source:       root.Source,
					Root:         rootPath,
					PluginPath:   pluginDir,
					ManifestPath: manifestPath,
					Message:      err.Error(),
				})
				continue
			}

			plugin, err := ParseManifest(manifest, ParseManifestOptions{
				Source:                        root.Source,
				Root:                          rootPath,
				PluginDir:                     pluginDir,
				ManifestPath:                  manifestPath,
				AllowManifestToolAutoApproval: options.AllowManifestToolAutoApproval,
			})
			if err != nil {
				diagnostics = append(diagnostics, toDiagnostic(err, root, rootPath, pluginDir, manifestPath))
				continue
			}
			discovered = append(discovered, plugin)
		}
	}

	return LoadResult{
		Roots:       roots,
		Plugins:     mergePlugins(discovered, &diagnostics),
		Diagnostics: diagnostics,
	}, nil
}

func ParseManifest(raw any, options ParseManifestOptions) (LoadedPlugin, error) {
	obj, ok := raw.(map[string]any)
	if !ok {
		return LoadedPlugin{}, ManifestError{Message: "Expected plugin manifest to be a JSON object."}
	}

	schemaVersion, err := requiredInt(obj, "schemaVersion")
	if err != nil {
		return LoadedPlugin{}, err
	}
	if schemaVersion != 1 {
		return LoadedPlugin{}, ManifestError{FieldPath: "schemaVersion", Message: "Expected schemaVersion 1."}
	}

	id, err := requiredID(obj, "id")
	if err != nil {
		return LoadedPlugin{}, err
	}
	name, err := requiredString(obj, "name")
	if err != nil {
		return LoadedPlugin{}, err
	}
	version, err := requiredString(obj, "version")
	if err != nil {
		return LoadedPlugin{}, err
	}
	description, err := optionalString(obj, "description")
	if err != nil {
		return LoadedPlugin{}, err
	}
	enabled, err := optionalBool(obj, "enabled", true)
	if err != nil {
		return LoadedPlugin{}, err
	}

	pluginDir, err := filepath.Abs(options.PluginDir)
	if err != nil {
		return LoadedPlugin{}, err
	}
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return LoadedPlugin{}, err
	}
	manifestPath, err := filepath.Abs(options.ManifestPath)
	if err != nil {
		return LoadedPlugin{}, err
	}

	tools, err := parseTools(obj["tools"], options.AllowManifestToolAutoApproval)
	if err != nil {
		return LoadedPlugin{}, err
	}
	prompts, err := parsePathExtensions(obj["prompts"], pluginDir, "prompts")
	if err != nil {
		return LoadedPlugin{}, err
	}
	skills, err := parsePathExtensions(obj["skills"], pluginDir, "skills")
	if err != nil {
		return LoadedPlugin{}, err
	}
	hooks, err := parseHooks(obj["hooks"])
	if err != nil {
		return LoadedPlugin{}, err
	}

	return LoadedPlugin{
		SchemaVersion: 1,
		ID:            id,
		Name:          name,
		Version:       version,
		Description:   description,
		Enabled:       enabled,
		Source:        options.Source,
		Root:          root,
		PluginDir:     pluginDir,
		ManifestPath:  manifestPath,
		Tools:         tools,
		Prompts:       prompts,
		Skills:        skills,
		Hooks:         hooks,
	}, nil
}

func FormatList(plugins []LoadedPlugin, diagnostics []Diagnostic) string {
	lines := []string{}
	if len(plugins) == 0 {
		lines = append(lines, "No local Zero plugins loaded.")
	} else {
		lines = append(lines, "Zero Plugins:")
		for _, plugin := range plugins {
			counts := fmt.Sprintf("%d tools, %d prompts, %d skills, %d hooks", len(plugin.Tools), len(plugin.Prompts), len(plugin.Skills), len(plugin.Hooks))
			state := "enabled"
			if !plugin.Enabled {
				state = "disabled"
			}
			lines = append(lines, fmt.Sprintf("  %s@%s %s [%s] %s - %s", plugin.ID, plugin.Version, plugin.Name, plugin.Source, state, counts))
		}
	}

	if len(diagnostics) > 0 {
		lines = append(lines, "Plugin diagnostics:")
		for _, diagnostic := range diagnostics {
			line := fmt.Sprintf("  [%s] %s", diagnostic.Kind, diagnostic.Message)
			if location := formatDiagnosticLocation(diagnostic); location != "" {
				line += " [" + location + "]"
			}
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func formatDiagnosticLocation(diagnostic Diagnostic) string {
	parts := []string{}
	if diagnostic.ManifestPath != "" {
		parts = append(parts, "manifestPath="+diagnostic.ManifestPath)
	}
	if diagnostic.FieldPath != "" {
		parts = append(parts, "fieldPath="+diagnostic.FieldPath)
	}
	if diagnostic.PluginPath != "" {
		parts = append(parts, "pluginPath="+diagnostic.PluginPath)
	}
	return strings.Join(parts, " ")
}

func parseTools(raw any, allowAutoApproval bool) ([]ToolExtension, error) {
	items, err := optionalArray(raw, "tools")
	if err != nil {
		return nil, err
	}
	tools := make([]ToolExtension, 0, len(items))
	for index, item := range items {
		field := fmt.Sprintf("tools.%d", index)
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, ManifestError{FieldPath: field, Message: "Expected tool extension to be an object."}
		}
		name, err := requiredID(obj, field+".name")
		if err != nil {
			return nil, err
		}
		description, err := optionalString(obj, field+".description")
		if err != nil {
			return nil, err
		}
		command, err := requiredString(obj, field+".command")
		if err != nil {
			return nil, err
		}
		args, err := optionalStringArray(obj["args"], field+".args")
		if err != nil {
			return nil, err
		}
		inputSchema, err := optionalObject(obj["inputSchema"], field+".inputSchema")
		if err != nil {
			return nil, err
		}
		if inputSchema == nil {
			inputSchema = map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": true,
			}
		}
		permission, err := parsePermission(obj["permission"], allowAutoApproval, field+".permission")
		if err != nil {
			return nil, err
		}
		tools = append(tools, ToolExtension{
			Name:        name,
			Description: description,
			Command:     command,
			Args:        args,
			InputSchema: inputSchema,
			Permission:  permission,
		})
	}
	return tools, nil
}

func parsePathExtensions(raw any, pluginDir string, label string) ([]PathExtension, error) {
	items, err := optionalArray(raw, label)
	if err != nil {
		return nil, err
	}
	extensions := make([]PathExtension, 0, len(items))
	for index, item := range items {
		field := fmt.Sprintf("%s.%d", label, index)
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, ManifestError{FieldPath: field, Message: "Expected path extension to be an object."}
		}
		name, err := requiredID(obj, field+".name")
		if err != nil {
			return nil, err
		}
		description, err := optionalString(obj, field+".description")
		if err != nil {
			return nil, err
		}
		path, err := requiredString(obj, field+".path")
		if err != nil {
			return nil, err
		}
		resolved, err := ResolvePluginPath(pluginDir, path, fmt.Sprintf("%s.%s.path", label, name))
		if err != nil {
			return nil, err
		}
		extensions = append(extensions, PathExtension{Name: name, Description: description, Path: resolved})
	}
	return extensions, nil
}

func parseHooks(raw any) ([]HookExtension, error) {
	items, err := optionalArray(raw, "hooks")
	if err != nil {
		return nil, err
	}
	hooks := make([]HookExtension, 0, len(items))
	for index, item := range items {
		field := fmt.Sprintf("hooks.%d", index)
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, ManifestError{FieldPath: field, Message: "Expected hook extension to be an object."}
		}
		name, err := requiredID(obj, field+".name")
		if err != nil {
			return nil, err
		}
		description, err := optionalString(obj, field+".description")
		if err != nil {
			return nil, err
		}
		event, err := parseHookEvent(obj["event"], field+".event")
		if err != nil {
			return nil, err
		}
		command, err := requiredString(obj, field+".command")
		if err != nil {
			return nil, err
		}
		args, err := optionalStringArray(obj["args"], field+".args")
		if err != nil {
			return nil, err
		}
		hooks = append(hooks, HookExtension{Name: name, Description: description, Event: event, Command: command, Args: args})
	}
	return hooks, nil
}

func ResolvePluginPath(pluginDir string, value string, fieldPath string) (string, error) {
	if filepath.IsAbs(value) || isRootedPath(value) || isWindowsAbs(value) {
		return "", ManifestError{FieldPath: fieldPath, Message: "must stay inside the plugin directory."}
	}

	root, err := filepath.Abs(pluginDir)
	if err != nil {
		return "", err
	}
	rootCheck := root
	if realRoot, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		rootCheck = realRoot
	} else if errors.Is(evalErr, os.ErrNotExist) {
		rootCheck, err = resolveMissingPathSymlinks(root)
		if err != nil {
			return "", err
		}
	}
	resolved, err := filepath.Abs(filepath.Join(root, value))
	if err != nil {
		return "", err
	}
	resolvedCheck := resolved
	if realResolved, evalErr := filepath.EvalSymlinks(resolved); evalErr == nil {
		resolvedCheck = realResolved
	} else if errors.Is(evalErr, os.ErrNotExist) {
		resolvedCheck, err = resolveMissingPathSymlinks(resolved)
		if err != nil {
			return "", err
		}
	}
	relative, err := filepath.Rel(rootCheck, resolvedCheck)
	if err != nil {
		return "", err
	}
	if relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." || strings.Contains(relative, `..\`) {
		return "", ManifestError{FieldPath: fieldPath, Message: "must stay inside the plugin directory."}
	}
	return resolved, nil
}

func resolveMissingPathSymlinks(path string) (string, error) {
	existing := path
	missing := []string{}
	for {
		if _, err := os.Stat(existing); err == nil {
			realExisting, err := filepath.EvalSymlinks(existing)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				realExisting = filepath.Join(realExisting, missing[index])
			}
			return realExisting, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return path, nil
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
}

func mergePlugins(discovered []LoadedPlugin, diagnostics *[]Diagnostic) []LoadedPlugin {
	byID := map[string]LoadedPlugin{}
	for _, plugin := range discovered {
		if previous, ok := byID[plugin.ID]; ok {
			*diagnostics = append(*diagnostics, Diagnostic{
				Kind:         DiagnosticDuplicate,
				PluginID:     plugin.ID,
				Source:       plugin.Source,
				Root:         plugin.Root,
				PluginPath:   plugin.PluginDir,
				ManifestPath: plugin.ManifestPath,
				Message:      fmt.Sprintf("Plugin %q from %s overrides %s plugin at %s.", plugin.ID, plugin.Source, previous.Source, previous.PluginDir),
			})
		}
		byID[plugin.ID] = plugin
	}

	plugins := make([]LoadedPlugin, 0, len(byID))
	for _, plugin := range byID {
		plugins = append(plugins, plugin)
	}
	sort.Slice(plugins, func(left int, right int) bool {
		return plugins[left].ID < plugins[right].ID
	})
	return plugins
}

func toDiagnostic(err error, root Root, rootPath string, pluginDir string, manifestPath string) Diagnostic {
	var manifestErr ManifestError
	if errors.As(err, &manifestErr) {
		return Diagnostic{
			Kind:         DiagnosticSchema,
			Source:       root.Source,
			Root:         rootPath,
			PluginPath:   pluginDir,
			ManifestPath: manifestPath,
			FieldPath:    manifestErr.FieldPath,
			Message:      manifestErr.Message,
		}
	}
	return Diagnostic{
		Kind:         DiagnosticIO,
		Source:       root.Source,
		Root:         rootPath,
		PluginPath:   pluginDir,
		ManifestPath: manifestPath,
		Message:      err.Error(),
	}
}

func requiredString(obj map[string]any, field string) (string, error) {
	value, ok := obj[lastPathSegment(field)]
	if !ok {
		return "", ManifestError{FieldPath: field, Message: "Expected a non-empty string."}
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", ManifestError{FieldPath: field, Message: "Expected a non-empty string."}
	}
	return strings.TrimSpace(text), nil
}

func optionalString(obj map[string]any, field string) (string, error) {
	value, ok := obj[lastPathSegment(field)]
	if !ok || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", ManifestError{FieldPath: field, Message: "Expected a non-empty string."}
	}
	return strings.TrimSpace(text), nil
}

func requiredID(obj map[string]any, field string) (string, error) {
	value, err := requiredString(obj, field)
	if err != nil {
		return "", err
	}
	if !pluginIDPattern.MatchString(value) {
		return "", ManifestError{FieldPath: field, Message: "Use letters, numbers, dots, dashes, or underscores."}
	}
	return value, nil
}

func requiredInt(obj map[string]any, field string) (int, error) {
	value, ok := obj[lastPathSegment(field)]
	if !ok {
		return 0, ManifestError{FieldPath: field, Message: "Expected a number."}
	}
	number, ok := value.(float64)
	if !ok || number != float64(int(number)) {
		return 0, ManifestError{FieldPath: field, Message: "Expected a number."}
	}
	return int(number), nil
}

func optionalBool(obj map[string]any, field string, fallback bool) (bool, error) {
	value, ok := obj[lastPathSegment(field)]
	if !ok || value == nil {
		return fallback, nil
	}
	boolValue, ok := value.(bool)
	if !ok {
		return false, ManifestError{FieldPath: field, Message: "Expected a boolean."}
	}
	return boolValue, nil
}

func optionalArray(raw any, field string) ([]any, error) {
	if raw == nil {
		return []any{}, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, ManifestError{FieldPath: field, Message: "Expected an array."}
	}
	return items, nil
}

func optionalObject(raw any, field string) (map[string]any, error) {
	if raw == nil {
		return nil, nil
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, ManifestError{FieldPath: field, Message: "Expected an object."}
	}
	return obj, nil
}

func optionalStringArray(raw any, field string) ([]string, error) {
	if raw == nil {
		return []string{}, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, ManifestError{FieldPath: field, Message: "Expected an array."}
	}
	values := make([]string, 0, len(items))
	for index, item := range items {
		text, ok := item.(string)
		if !ok {
			return nil, ManifestError{FieldPath: fmt.Sprintf("%s.%d", field, index), Message: "Expected a string."}
		}
		values = append(values, text)
	}
	return values, nil
}

func parsePermission(raw any, allowAutoApproval bool, field string) (ToolPermission, error) {
	if raw == nil {
		return PermissionPrompt, nil
	}
	text, ok := raw.(string)
	if !ok {
		return "", ManifestError{FieldPath: field, Message: "Expected allow, prompt, or deny."}
	}
	permission := ToolPermission(strings.TrimSpace(text))
	switch permission {
	case PermissionAllow:
		if !allowAutoApproval {
			return PermissionPrompt, nil
		}
		return PermissionAllow, nil
	case PermissionPrompt, PermissionDeny:
		return permission, nil
	default:
		return "", ManifestError{FieldPath: field, Message: "Expected allow, prompt, or deny."}
	}
}

func parseHookEvent(raw any, field string) (HookEvent, error) {
	text, ok := raw.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", ManifestError{FieldPath: field, Message: "Expected a hook event."}
	}
	event := HookEvent(strings.TrimSpace(text))
	switch event {
	case HookBeforeTool, HookAfterTool, HookSessionStart, HookSessionEnd:
		return event, nil
	default:
		return "", ManifestError{FieldPath: field, Message: "Expected beforeTool, afterTool, sessionStart, or sessionEnd."}
	}
}

func isWindowsAbs(value string) bool {
	return regexp.MustCompile(`^[A-Za-z]:[\\/]|^\\\\`).MatchString(value)
}

func isRootedPath(value string) bool {
	return strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`)
}

func resolveCwd(cwd string) (string, error) {
	if strings.TrimSpace(cwd) == "" {
		return os.Getwd()
	}
	if filepath.IsAbs(cwd) {
		return filepath.Clean(cwd), nil
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func envValue(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func lastPathSegment(field string) string {
	if index := strings.LastIndex(field, "."); index >= 0 {
		return field[index+1:]
	}
	return field
}
