package mcp

import (
	"math"
	"strings"

	"github.com/Gitlawb/zero/internal/tools"
)

func SchemaFromMCP(input map[string]any) tools.Schema {
	schema := tools.Schema{
		Type:                 firstString(input["type"], "object"),
		Properties:           map[string]tools.PropertySchema{},
		AdditionalProperties: boolValue(input["additionalProperties"], false),
	}

	for _, required := range stringSlice(input["required"]) {
		schema.Required = append(schema.Required, required)
	}

	if properties, ok := input["properties"].(map[string]any); ok {
		for name, raw := range properties {
			propertyMap, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			schema.Properties[name] = propertyFromMCP(propertyMap)
		}
	}
	if len(schema.Properties) == 0 {
		schema.Properties = nil
	}
	return schema
}

func propertyFromMCP(input map[string]any) tools.PropertySchema {
	property := tools.PropertySchema{
		Type:        firstString(input["type"], "string"),
		Description: stringValue(input["description"]),
		Enum:        stringSlice(input["enum"]),
		Default:     input["default"],
	}
	if property.Type == "number" {
		property.Type = "number"
	}
	if min, ok := intValue(input["minimum"]); ok {
		property.Minimum = &min
	}
	if max, ok := intValue(input["maximum"]); ok {
		property.Maximum = &max
	}
	return property
}

func firstString(value any, fallback string) string {
	if text := stringValue(value); text != "" {
		return text
	}
	if values, ok := value.([]any); ok {
		for _, item := range values {
			if text := stringValue(item); text != "" {
				return text
			}
		}
	}
	return fallback
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return ""
	}
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string{}, typed...)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if text := stringValue(item); text != "" {
				values = append(values, text)
			}
		}
		return values
	default:
		return nil
	}
}

func boolValue(value any, fallback bool) bool {
	if typed, ok := value.(bool); ok {
		return typed
	}
	return fallback
}

func intValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		if math.Trunc(typed) == typed {
			return int(typed), true
		}
	}
	return 0, false
}
