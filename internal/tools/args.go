package tools

import (
	"fmt"
	"math"
	"strconv"
	"strings"
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

// boolArg reads a boolean argument, tolerating the string/number forms models
// commonly emit ("true"/"false", "yes"/"no", "on"/"off", 1/0) since not every
// model sends a JSON boolean.
func boolArg(args map[string]any, key string, fallback bool) (bool, error) {
	value, ok := args[key]
	if !ok || value == nil {
		return fallback, nil
	}

	switch typed := value.(type) {
	case bool:
		return typed, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "yes", "on", "1":
			return true, nil
		case "false", "no", "off", "0":
			return false, nil
		}
	case float64:
		if typed == 1 {
			return true, nil
		}
		if typed == 0 {
			return false, nil
		}
	case int:
		if typed == 1 {
			return true, nil
		}
		if typed == 0 {
			return false, nil
		}
	}
	return false, fmt.Errorf("%s must be a boolean", key)
}

// floatToInt converts a float to int only when it is finite, integral, and in
// range; NaN/Inf/non-integer/out-of-range return ok=false so callers fail closed
// before an implementation-defined cast.
func floatToInt(f float64) (int, bool) {
	// Use >= against float64(math.MaxInt): that constant rounds UP to 2^63, so a
	// strict > would let exactly 2^63 through to an out-of-range int(f) cast. The
	// largest representable in-range float (2^63-1024) is still < it, so nothing
	// valid is excluded. MinInt (-2^63) is exactly representable, so > is correct there.
	if math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f || f >= float64(math.MaxInt) || f < float64(math.MinInt) {
		return 0, false
	}
	return int(f), true
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
		n, ok := floatToInt(typed)
		if !ok {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		number = n
	case string:
		// Some models send numbers as strings ("5"); accept whole numbers.
		trimmed := strings.TrimSpace(typed)
		if parsed, perr := strconv.Atoi(trimmed); perr == nil {
			number = parsed
		} else if f, ferr := strconv.ParseFloat(trimmed, 64); ferr == nil {
			n, ok := floatToInt(f)
			if !ok {
				return 0, fmt.Errorf("%s must be an integer", key)
			}
			number = n
		} else {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
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

func additionalPermissionsProperties() map[string]PropertySchema {
	return requestPermissionsProfileSchema().Properties
}

func sandboxPermissionsArg(args map[string]any) (SandboxPermissionOverride, error) {
	raw, err := stringArgWithEmpty(args, "sandbox_permissions", string(SandboxPermissionsUseDefault), false, false)
	if err != nil {
		return "", err
	}
	switch SandboxPermissionOverride(strings.TrimSpace(raw)) {
	case "", SandboxPermissionsUseDefault:
		return SandboxPermissionsUseDefault, nil
	case SandboxPermissionsWithAdditionalPermissions:
		return SandboxPermissionsWithAdditionalPermissions, nil
	case SandboxPermissionsRequireEscalated:
		return SandboxPermissionsRequireEscalated, nil
	default:
		return "", fmt.Errorf("sandbox_permissions must be %q, %q, or %q", SandboxPermissionsUseDefault, SandboxPermissionsWithAdditionalPermissions, SandboxPermissionsRequireEscalated)
	}
}
