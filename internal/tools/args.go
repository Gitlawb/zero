package tools

import (
	"fmt"
	"math"
)

func stringArg(args map[string]any, key string, fallback string, required bool) (string, error) {
	return stringArgWithEmpty(args, key, fallback, required, false)
}

func stringArgWithEmpty(args map[string]any, key string, fallback string, required bool, allowEmpty bool) (string, error) {
	value, ok := args[key]
	if !ok || value == nil {
		if required {
			return "", fmt.Errorf("%s is required", key)
		}
		return fallback, nil
	}

	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	if !allowEmpty && text == "" {
		return "", fmt.Errorf("%s must be a non-empty string", key)
	}
	return text, nil
}

func boolArg(args map[string]any, key string, fallback bool) (bool, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return fallback, nil
	}

	boolean, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return boolean, nil
}

func intArg(args map[string]any, key string, fallback int, min int, max int) (int, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return fallback, nil
	}

	var number int
	switch typed := value.(type) {
	case int:
		number = typed
	case int32:
		number = int(typed)
	case int64:
		number = int(typed)
	case float64:
		if math.Trunc(typed) != typed {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		number = int(typed)
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}

	if number < min {
		return 0, fmt.Errorf("%s must be at least %d", key, min)
	}
	if max > 0 && number > max {
		return 0, fmt.Errorf("%s must be at most %d", key, max)
	}
	return number, nil
}

func intPtr(value int) *int {
	return &value
}
