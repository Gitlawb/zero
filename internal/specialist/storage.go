package specialist

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Storage struct {
	paths Paths
}

type CreateInput struct {
	Name            string
	Description     string
	SystemPrompt    string
	Extends         string
	Model           string
	ReasoningEffort string
	Tools           []string
	Location        Location
	Overwrite       bool
}

type DeleteInput struct {
	Name     string
	Location Location
}

func NewStorage(paths Paths) *Storage {
	return &Storage{paths: paths}
}

func (storage *Storage) Create(input CreateInput) (Manifest, error) {
	location := normalizeWritableLocation(input.Location)
	path, err := storage.path(input.Name, location)
	if err != nil {
		return Manifest{}, err
	}
	systemPrompt := strings.TrimSpace(input.SystemPrompt)
	if systemPrompt == "" && strings.TrimSpace(input.Extends) == "" {
		systemPrompt = strings.TrimSpace(input.Description)
	}
	manifest := Manifest{
		Metadata: Metadata{
			Name:            strings.TrimSpace(input.Name),
			Description:     strings.TrimSpace(input.Description),
			Extends:         strings.TrimSpace(input.Extends),
			Model:           strings.TrimSpace(input.Model),
			ReasoningEffort: strings.TrimSpace(input.ReasoningEffort),
			Tools:           trimStringList(input.Tools),
		},
		SystemPrompt: systemPrompt,
		Location:     location,
		FilePath:     path,
	}
	if err := Validate(&manifest); err != nil {
		return Manifest{}, err
	}
	if strings.TrimSpace(manifest.SystemPrompt) == "" && manifest.Metadata.Extends == "" {
		return Manifest{}, fmt.Errorf("specialist %q requires a system prompt", manifest.Metadata.Name)
	}
	content := FormatMarkdown(manifest)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Manifest{}, fmt.Errorf("create specialist directory: %w", err)
	}
	if input.Overwrite {
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return Manifest{}, fmt.Errorf("inspect specialist file: %w", err)
		}
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return Manifest{}, fmt.Errorf("refusing to overwrite symlink specialist file: %s", path)
		}
	}
	flags := os.O_WRONLY | os.O_CREATE
	if input.Overwrite {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return Manifest{}, fmt.Errorf("specialist already exists: %s", manifest.Metadata.Name)
		}
		return Manifest{}, fmt.Errorf("write specialist file: %w", err)
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		return Manifest{}, fmt.Errorf("write specialist file: %w", err)
	}
	if err := file.Close(); err != nil {
		return Manifest{}, fmt.Errorf("close specialist file: %w", err)
	}
	return manifest, nil
}

func (storage *Storage) Delete(input DeleteInput) (string, error) {
	location := normalizeWritableLocation(input.Location)
	path, err := storage.path(input.Name, location)
	if err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("specialist not found: %s", strings.TrimSpace(input.Name))
		}
		return "", fmt.Errorf("delete specialist file: %w", err)
	}
	return path, nil
}

func (storage *Storage) Path(name string, location Location) (string, error) {
	return storage.path(name, normalizeWritableLocation(location))
}

func FormatMarkdown(manifest Manifest) string {
	var builder strings.Builder
	builder.WriteString("---\n")
	writeMetadataString(&builder, "name", manifest.Metadata.Name)
	writeMetadataString(&builder, "description", manifest.Metadata.Description)
	writeMetadataString(&builder, "extends", manifest.Metadata.Extends)
	writeMetadataString(&builder, "model", manifest.Metadata.Model)
	writeMetadataString(&builder, "reasoningEffort", manifest.Metadata.ReasoningEffort)
	if len(manifest.Metadata.Tools) > 0 {
		builder.WriteString("tools:\n")
		for _, tool := range manifest.Metadata.Tools {
			tool = strings.TrimSpace(tool)
			if tool == "" {
				continue
			}
			builder.WriteString("  - ")
			builder.WriteString(strconv.Quote(tool))
			builder.WriteByte('\n')
		}
	}
	builder.WriteString("---\n\n")
	builder.WriteString(strings.TrimSpace(manifest.SystemPrompt))
	builder.WriteByte('\n')
	return builder.String()
}

func (storage *Storage) path(name string, location Location) (string, error) {
	name = strings.TrimSpace(name)
	if !namePattern.MatchString(name) {
		return "", fmt.Errorf("invalid specialist name %q: use lowercase letters, numbers, and dashes", name)
	}
	dir, err := storage.dir(location)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".md"), nil
}

func (storage *Storage) dir(location Location) (string, error) {
	switch location {
	case LocationUser:
		if strings.TrimSpace(storage.paths.UserDir) == "" {
			return "", fmt.Errorf("user specialist directory is not configured")
		}
		return storage.paths.UserDir, nil
	case LocationProject:
		if strings.TrimSpace(storage.paths.ProjectDir) == "" {
			return "", fmt.Errorf("project specialist directory is not configured")
		}
		return storage.paths.ProjectDir, nil
	default:
		return "", fmt.Errorf("invalid specialist location %q", location)
	}
}

func normalizeWritableLocation(location Location) Location {
	if location == "" {
		return LocationUser
	}
	return location
}

func trimStringList(values []string) []string {
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func writeMetadataString(builder *strings.Builder, key string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	builder.WriteString(key)
	builder.WriteString(": ")
	builder.WriteString(strconv.Quote(value))
	builder.WriteByte('\n')
}
